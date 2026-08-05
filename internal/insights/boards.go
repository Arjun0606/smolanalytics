package insights

import (
	"fmt"
	"strings"
	"time"
)

// Board is one dashboard: a named collection of saved insights plus the defaults
// every insight on it inherits when it is rendered — the board's own filters and
// its time range. That inheritance is the whole point of a board: "Growth, last 7
// days, plan=pro" is one setting on the board, not a setting repeated on each of
// its nine cards and forgotten on the tenth.
type Board struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	// Filters are applied to every insight on the board. An insight that sets the
	// same key wins — a board default must never silently overwrite a filter the
	// user typed into the report itself. MergeFilters is that rule; call it rather
	// than re-implementing the precedence per surface.
	Filters map[string]string `json:"filters,omitempty"`
	Range   TimeRange         `json:"range"`
}

// TimeRange is a board's default window, stored the way the user chose it: either
// relative ("last 30 days") or a fixed pair of instants. It is deliberately NOT
// resolved at rest — a board saved as "last 30 days" has to mean the 30 days
// before the morning you open it, not the 30 days before the afternoon you created
// it, which is what storing absolute instants for a relative choice would freeze it
// into.
type TimeRange struct {
	Days int       `json:"days,omitempty"`
	From time.Time `json:"from,omitempty"`
	To   time.Time `json:"to,omitempty"`
}

// ResolvedRange is a TimeRange turned into the actual half-open window [From, To)
// a query runs over, carrying enough of its own provenance that a response can
// state it: which instants, how wide, and whether they came from the board's
// relative setting, its fixed dates, or the store default. A dashboard that shows
// numbers without saying what window produced them is unciteable.
type ResolvedRange struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Days   int       `json:"days,omitempty"`
	Source string    `json:"source"` // "relative" | "absolute" | "default"
}

// defaultRangeDays is what a board with no range of its own resolves to. Stated in
// the resolved window as source "default" rather than left implicit, so nobody has
// to read this constant to know what they're looking at.
const defaultRangeDays = 30

// maxRangeDays bounds a stored relative range. It is a validation limit, not a
// query cap: individual reports clamp their own windows, but a board persisting
// days=100000 would resolve to an instant before the Unix epoch and quietly render
// every board that inherits it as "all time".
const maxRangeDays = 3650

// Resolve turns the stored range into the concrete window to query. now is a
// parameter and never time.Now(): the resolved window is echoed back in the API
// response and pinned by tests, and a function that reads the clock itself can
// deliver neither a reproducible test nor a window the caller can verify.
//
// The window is half-open — [From, To) — matching every other window in this
// codebase, so an event landing exactly on the boundary is counted once rather
// than in both of two adjacent ranges.
func (r TimeRange) Resolve(now time.Time) (ResolvedRange, error) {
	if err := r.validate(); err != nil {
		return ResolvedRange{}, err
	}
	now = now.UTC()
	switch {
	case r.Days > 0:
		// AddDate, not a 24h multiplication: on a UTC clock they agree, but callers
		// hand us local instants and a day is not always 24 hours.
		return ResolvedRange{From: now.AddDate(0, 0, -r.Days), To: now, Days: r.Days, Source: "relative"}, nil
	case !r.From.IsZero() || !r.To.IsZero():
		from, to := r.From.UTC(), r.To.UTC()
		return ResolvedRange{From: from, To: to, Days: int(to.Sub(from).Hours() / 24), Source: "absolute"}, nil
	default:
		return ResolvedRange{From: now.AddDate(0, 0, -defaultRangeDays), To: now, Days: defaultRangeDays, Source: "default"}, nil
	}
}

// validate rejects a range that would render as an empty or nonsense board. It runs
// on write as well as on Resolve, so a bad range is refused at the moment it is set
// rather than surfacing later as a board that mysteriously shows nothing.
func (r TimeRange) validate() error {
	if r.Days < 0 {
		return fmt.Errorf("range days must not be negative, got %d", r.Days)
	}
	if r.Days > maxRangeDays {
		return fmt.Errorf("range days must be at most %d, got %d", maxRangeDays, r.Days)
	}
	mixed := r.Days > 0 && (!r.From.IsZero() || !r.To.IsZero())
	if mixed {
		// Refuse rather than pick one: whichever we silently preferred, half the
		// users would be looking at the window they didn't set.
		return fmt.Errorf("range is either relative (days) or absolute (from/to), not both")
	}
	if r.From.IsZero() != r.To.IsZero() {
		return fmt.Errorf("an absolute range needs both from and to")
	}
	if !r.From.IsZero() && !r.To.After(r.From) {
		// Half-open means from == to is empty, not "everything".
		return fmt.Errorf("range to must be after from")
	}
	return nil
}

// Boards returns every board, default first. Order is stable across loads because
// adoptOrphans always prepends the default board and creation appends.
func (s *Store) Boards() []Board {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyBoards(s.boards)
}

// GetBoard returns one board by id.
func (s *Store) GetBoard(id string) (Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.boards {
		if b.ID == id {
			return copyBoard(b), nil
		}
	}
	return Board{}, fmt.Errorf("%w: %q", ErrNoSuchBoard, id)
}

// CountByBoard returns how many insights sit on each board, including zero for
// empty boards. The board list is the first thing the dashboard draws, and without
// this it would fan out one ListBoard per board to print "4 insights".
func (s *Store) CountByBoard() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.boards))
	for _, b := range s.boards {
		out[b.ID] = 0
	}
	for _, it := range s.items {
		out[it.BoardID]++
	}
	return out
}

// CreateBoard adds a board and persists. The caller's ID and Created are ignored —
// like Save, the store owns identity, so a client cannot mint a second board with
// an id that already means something else.
func (s *Store) CreateBoard(b Board) (Board, error) {
	name := strings.TrimSpace(b.Name)
	if name == "" {
		return Board{}, fmt.Errorf("name is required")
	}
	if err := b.Range.validate(); err != nil {
		return Board{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nameTaken(name, "") {
		return Board{}, fmt.Errorf("%w: %q", ErrBoardNameTaken, name)
	}
	b.Name = name
	b.ID = newID()
	b.Created = now()
	b.Filters = copyMap(b.Filters)
	old := s.boards
	s.boards = append(append([]Board{}, s.boards...), b)
	if err := s.persist(); err != nil {
		s.boards = old // roll back so memory matches disk
		return Board{}, err
	}
	return copyBoard(b), nil
}

// UpdateBoard renames a board and/or replaces its defaults. Name, Filters and Range
// are taken from in; ID and Created are not editable. An empty name is rejected
// rather than treated as "leave it alone" — a board whose name vanished is a board
// the user cannot find in the switcher.
//
// The default board can be renamed (its name is just a label) but not deleted; see
// DeleteBoard.
func (s *Store) UpdateBoard(id string, in Board) (Board, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Board{}, fmt.Errorf("name is required")
	}
	if err := in.Range.validate(); err != nil {
		return Board{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i := range s.boards {
		if s.boards[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Board{}, fmt.Errorf("%w: %q", ErrNoSuchBoard, id)
	}
	if s.nameTaken(name, id) {
		return Board{}, fmt.Errorf("%w: %q", ErrBoardNameTaken, name)
	}
	old := copyBoards(s.boards)
	s.boards[idx].Name = name
	s.boards[idx].Filters = copyMap(in.Filters)
	s.boards[idx].Range = in.Range
	if err := s.persist(); err != nil {
		s.boards = old // roll back so memory matches disk
		return Board{}, err
	}
	return copyBoard(s.boards[idx]), nil
}

// DeleteBoard removes an empty board.
//
// The orphan question has exactly two honest answers — refuse, or reparent — and
// this store REFUSES: deleting a board that still holds insights returns
// ErrBoardNotEmpty and changes nothing. Reparenting silently would make one click
// on "delete board" scatter a user's saved work onto a board they weren't looking
// at, and they'd have no way to tell it apart from the work being gone. The
// deliberate path is MoveAllInsights (or Delete per insight) first, which makes the
// caller say where the work should go.
//
// The default board is never deletable: every insight must name a board that
// exists, and the default is the board that is always there to name.
func (s *Store) DeleteBoard(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == DefaultBoardID {
		return fmt.Errorf("%w: %q", ErrDefaultBoard, id)
	}
	idx := -1
	for i := range s.boards {
		if s.boards[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: %q", ErrNoSuchBoard, id)
	}
	for _, it := range s.items {
		if it.BoardID == id {
			return fmt.Errorf("%w: %q — move or delete them first", ErrBoardNotEmpty, id)
		}
	}
	old := s.boards
	out := make([]Board, 0, len(old)-1)
	out = append(out, old[:idx]...)
	out = append(out, old[idx+1:]...)
	s.boards = out
	if err := s.persist(); err != nil {
		s.boards = old // roll back so memory matches disk
		return err
	}
	return nil
}

// hasBoard reports whether a board id resolves. Caller holds the lock.
func (s *Store) hasBoard(id string) bool {
	for _, b := range s.boards {
		if b.ID == id {
			return true
		}
	}
	return false
}

// nameTaken compares case-insensitively and ignoring surrounding space, because
// "Growth" and "growth " are the same board to the human reading the switcher.
// exceptID lets a rename keep its own name. Caller holds the lock.
func (s *Store) nameTaken(name, exceptID string) bool {
	want := strings.ToLower(name)
	for _, b := range s.boards {
		if b.ID == exceptID {
			continue
		}
		if strings.ToLower(strings.TrimSpace(b.Name)) == want {
			return true
		}
	}
	return false
}

// copyBoard deep-copies Filters for the same reason copyInsight copies Params: a
// caller editing the map it got back would otherwise mutate the store's copy behind
// its lock, producing a change that never reaches disk.
func copyBoard(b Board) Board {
	b.Filters = copyMap(b.Filters)
	return b
}

func copyBoards(in []Board) []Board {
	out := make([]Board, len(in))
	for i, b := range in {
		out[i] = copyBoard(b)
	}
	return out
}
