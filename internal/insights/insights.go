// Package insights persists saved reports and the boards they live on — the
// "pin this report" feature that turns ad-hoc Explore into a dashboard you open
// every morning. Config-shaped (small, mutable), so it's a single JSON file
// rewritten atomically on change, separate from the append-only event log.
package insights

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Insight is one saved report: a type (funnel|trend|breakdown|retention) plus the
// params Explore needs to re-run it. BoardID says which board it appears on; it is
// never empty in the store (Save fills it, load adopts orphans), so a board render
// never has to guess where a row belongs.
type Insight struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Params  map[string]string `json:"params"`
	BoardID string            `json:"board_id"`
	Created time.Time         `json:"created"`
}

// DefaultBoardID is the board every insight falls back to: the home for a Save
// that names no board, and the board an existing installation's saved reports
// join on upgrade. It is a fixed string rather than a generated id precisely so
// the upgrade is idempotent — re-opening an already-migrated file finds this same
// board instead of minting a second "default" every launch.
const DefaultBoardID = "default"

const defaultBoardName = "All insights"

// fileVersion 2 = {version, boards, insights}. Version 1 was a bare JSON array of
// insights with no boards at all, and is still what a pre-boards install has on
// disk; load reads both.
const fileVersion = 2

// Sentinel errors so callers (the HTTP layer especially) can map a refusal to the
// right status code instead of string-matching a message.
var (
	ErrNoSuchBoard    = errors.New("board not found")
	ErrNoSuchInsight  = errors.New("insight not found")
	ErrBoardNotEmpty  = errors.New("board still has insights")
	ErrDefaultBoard   = errors.New("the default board cannot be deleted")
	ErrBoardNameTaken = errors.New("a board with that name already exists")
)

// fileFormat is the on-disk shape. Written whole on every change (this is config,
// measured in kilobytes) via temp-file + rename.
type fileFormat struct {
	Version  int       `json:"version"`
	Boards   []Board   `json:"boards"`
	Insights []Insight `json:"insights"`
}

// Store holds saved insights and boards in memory and (when path != "") persists
// them.
type Store struct {
	mu     sync.Mutex
	path   string
	boards []Board
	items  []Insight
}

// Open loads saved insights from path (empty/missing = start fresh). An empty path
// means in-memory only (used by the throwaway demo).
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if path == "" {
		s.adoptOrphans()
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.adoptOrphans()
			return s, nil
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := s.load(b); err != nil {
			return nil, err
		}
	}
	s.adoptOrphans()
	return s, nil
}

// load parses either on-disk format. A pre-boards install has a bare JSON array;
// dropping those rows on upgrade would be destroying a real user's saved work, so
// an array is read as version 1 and handed to adoptOrphans, which gives every one
// of its insights the default board. Anything else is version 2.
func (s *Store) load(b []byte) error {
	trimmed := bytes.TrimLeft(b, " \t\r\n")
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '[' {
		var legacy []Insight
		if err := json.Unmarshal(trimmed, &legacy); err != nil {
			return fmt.Errorf("insights file corrupt: %w", err)
		}
		s.items = legacy
		return nil
	}
	var f fileFormat
	if err := json.Unmarshal(trimmed, &f); err != nil {
		return fmt.Errorf("insights file corrupt: %w", err)
	}
	s.boards, s.items = f.Boards, f.Insights
	return nil
}

// adoptOrphans establishes the two invariants every other method relies on: the
// default board exists, and every insight names a board that exists. It runs on
// load only — a read must not rewrite the user's file, so the migration is instead
// re-derived identically on every Open, and the file switches to the new format
// the first time something actually changes.
//
// The second half also catches a hand-edited (or partially written) file whose
// insights point at a board that is gone: those rows are reparented to the default
// board so they still appear somewhere, rather than being invisible on a board id
// nothing resolves. Caller holds the lock, or is Open (pre-publication).
func (s *Store) adoptOrphans() {
	known := make(map[string]bool, len(s.boards)+1)
	for _, b := range s.boards {
		known[b.ID] = true
	}
	if !known[DefaultBoardID] {
		// Prepend: the default board is the one that always exists, so it reads first.
		s.boards = append([]Board{{
			ID:      DefaultBoardID,
			Name:    defaultBoardName,
			Created: s.earliestCreated(),
		}}, s.boards...)
		known[DefaultBoardID] = true
	}
	for i := range s.items {
		if !known[s.items[i].BoardID] {
			s.items[i].BoardID = DefaultBoardID
		}
	}
}

// earliestCreated dates a migrated default board from the oldest insight it
// inherits, so the board never claims to be newer than the reports sitting on it
// (which is what a naive now() would print for work saved months ago).
func (s *Store) earliestCreated() time.Time {
	var t time.Time
	for _, it := range s.items {
		if !it.Created.IsZero() && (t.IsZero() || it.Created.Before(t)) {
			t = it.Created
		}
	}
	if t.IsZero() {
		return now()
	}
	return t
}

// List returns every insight across every board, oldest first. Kept board-agnostic
// because the MCP/API "list saved reports" surfaces predate boards and must not
// start hiding rows.
func (s *Store) List() []Insight {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyInsights(s.items)
}

// ListBoard returns the insights pinned to one board, in save order. An unknown
// board is an error rather than an empty list: silently rendering "no insights
// yet" for a board that does not exist is how a typo reads as data loss.
func (s *Store) ListBoard(boardID string) ([]Insight, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasBoard(boardID) {
		return nil, fmt.Errorf("%w: %q", ErrNoSuchBoard, boardID)
	}
	out := make([]Insight, 0, len(s.items))
	for _, it := range s.items {
		if it.BoardID == boardID {
			out = append(out, it)
		}
	}
	return copyInsights(out), nil
}

var now = func() time.Time { return time.Now().UTC() }

// Save validates and stores an insight (assigning an id), then persists. An empty
// BoardID means the default board; a named board must already exist. Returns the
// stored copy.
func (s *Store) Save(in Insight) (Insight, error) {
	if in.Name == "" {
		return Insight{}, fmt.Errorf("name is required")
	}
	switch in.Type {
	case "funnel", "trend", "breakdown", "retention", "paths", "lifecycle", "stickiness", "groups":
	default:
		return Insight{}, fmt.Errorf("unknown report type %q", in.Type)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	board := strings.TrimSpace(in.BoardID)
	if board == "" {
		board = DefaultBoardID
	}
	// Refuse rather than silently defaulting: a client that sends a stale board id
	// (board deleted in another tab) would otherwise have its report quietly land
	// somewhere it will never look for it.
	if !s.hasBoard(board) {
		return Insight{}, fmt.Errorf("%w: %q", ErrNoSuchBoard, board)
	}
	in.BoardID = board
	in.ID = newID()
	in.Created = now()
	if in.Params == nil {
		in.Params = map[string]string{}
	}
	s.items = append(s.items, in)
	if err := s.persist(); err != nil {
		s.items = s.items[:len(s.items)-1] // roll back
		return Insight{}, err
	}
	return in, nil
}

// Delete removes an insight by id (no error if it's already gone). found is true
// only when a row actually went away, so callers never claim a removal that did
// not occur.
func (s *Store) Delete(id string) (found bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.items
	out := make([]Insight, 0, len(old))
	for _, it := range old {
		if it.ID != id {
			out = append(out, it)
		} else {
			found = true
		}
	}
	if !found {
		return false, nil
	}
	s.items = out
	if err := s.persist(); err != nil {
		s.items = old // roll back so memory matches disk
		return false, err
	}
	return true, nil
}

// MoveInsight repins one insight to another board. Moving to the board it already
// sits on is a no-op that does not touch the file — rewriting on a no-op drag is
// pointless disk churn and a pointless chance to fail.
func (s *Store) MoveInsight(insightID, boardID string) (Insight, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasBoard(boardID) {
		return Insight{}, fmt.Errorf("%w: %q", ErrNoSuchBoard, boardID)
	}
	idx := -1
	for i := range s.items {
		if s.items[i].ID == insightID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Insight{}, fmt.Errorf("%w: %q", ErrNoSuchInsight, insightID)
	}
	if s.items[idx].BoardID == boardID {
		return copyInsight(s.items[idx]), nil
	}
	old := copyInsights(s.items)
	s.items[idx].BoardID = boardID
	if err := s.persist(); err != nil {
		s.items = old // roll back so memory matches disk
		return Insight{}, err
	}
	return copyInsight(s.items[idx]), nil
}

// MoveAllInsights repins every insight on one board to another, returning how many
// moved. This is the deliberate way to empty a board before deleting it — see
// DeleteBoard, which refuses while insights remain.
func (s *Store) MoveAllInsights(fromBoard, toBoard string) (moved int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasBoard(fromBoard) {
		return 0, fmt.Errorf("%w: %q", ErrNoSuchBoard, fromBoard)
	}
	if !s.hasBoard(toBoard) {
		return 0, fmt.Errorf("%w: %q", ErrNoSuchBoard, toBoard)
	}
	if fromBoard == toBoard {
		return 0, nil
	}
	old := copyInsights(s.items)
	for i := range s.items {
		if s.items[i].BoardID == fromBoard {
			s.items[i].BoardID = toBoard
			moved++
		}
	}
	if moved == 0 {
		return 0, nil
	}
	if err := s.persist(); err != nil {
		s.items = old // roll back so memory matches disk
		return 0, err
	}
	return moved, nil
}

// persist writes the whole file via temp-file + rename (atomic). Caller holds lock.
func (s *Store) persist() error {
	if s.path == "" {
		return nil
	}
	// Marshal from non-nil slices: an empty board list would otherwise serialise as
	// null and read back as "no boards", which adoptOrphans would then have to
	// repair on every load.
	f := fileFormat{Version: fileVersion, Boards: s.boards, Insights: s.items}
	if f.Boards == nil {
		f.Boards = []Board{}
	}
	if f.Insights == nil {
		f.Insights = []Insight{}
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// copyInsight deep-copies Params so a caller ranging over List() and editing a
// param cannot mutate the store's copy behind its lock — an edit that would never
// reach disk and would silently diverge memory from the file.
func copyInsight(in Insight) Insight {
	in.Params = copyMap(in.Params)
	return in
}

func copyInsights(in []Insight) []Insight {
	out := make([]Insight, len(in))
	for i, it := range in {
		out[i] = copyInsight(it)
	}
	return out
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
