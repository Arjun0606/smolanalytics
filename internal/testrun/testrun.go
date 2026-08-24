// Package testrun is the record of the agent using your app: what it tried, what it found, and
// whether it needed a model to do it.
//
// This is the product's primary object now. The dashboard was built around a different one —
// events — because this was an analytics tool, and every screen answered "what did your users do".
// The question a customer opens this product to ask is "did the thing I just shipped break
// anything", and until this store there was nowhere for the answer to live.
//
// DELIBERATELY TINY, like the acted and alert stores beside it. A run is a name, a verdict, a
// reason, and the steps that got there. There is no queue, no scheduler and no retry policy in
// here: those belong to whatever is driving the runner, and a storage package that grows a
// workflow engine becomes the thing nobody can change.
//
// TWO FIELDS CARRY THE WHOLE ECONOMIC ARGUMENT. Mode says whether this run needed a model, and
// DurationMs says how long it took. A customer looking at a wall of `replay · 0.6s` rows and one
// `agent · 48s` row can see, without being told, that they are paying for intelligence only when
// something actually changed. That is the difference between us and the incumbent, and it is
// visible in the data rather than asserted in the copy.
package testrun

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"
)

// Mode is how a run was executed.
const (
	// ModeAgent means a model drove it: the first run of a test, or one where the recording no
	// longer fit the app.
	ModeAgent = "agent"
	// ModeReplay means the recorded plan ran with no model at all.
	ModeReplay = "replay"
)

// Status is the verdict.
const (
	// StatusPassed means the agent directly observed what the test asked it to verify.
	StatusPassed = "passed"
	// StatusFailed means the app did not do what the test describes. This is a bug report.
	StatusFailed = "failed"
	// StatusStale means a recorded plan no longer fits the app — a control was renamed, or removed.
	//
	// It is a THIRD status on purpose, and the distinction is the one this product must never blur:
	// a replay cannot tell "renamed" from "gone", and reporting a rename as a failure pages someone
	// at 2am over a copy change. Stale means "we do not know yet, the agent is looking".
	StatusStale = "stale"
	// StatusErrored means the runner itself failed — no browser, no network, no API key. Never a
	// statement about the customer's app, and never rendered as one.
	StatusErrored = "errored"
)

// Step is one thing the agent did, kept for the timeline on a failed run.
type Step struct {
	N int `json:"n"`
	// Do is the human sentence: `click button "Proceed to checkout"`.
	Do string `json:"do"`
	// Why is what the agent expected it to accomplish. Empty on a replay, which has no reasoning.
	Why string `json:"why,omitempty"`
	OK  bool   `json:"ok"`
	// Detail is the error when OK is false.
	Detail string `json:"detail,omitempty"`
	Ms     int    `json:"ms"`
}

// Run is one execution of one test.
type Run struct {
	ID   string `json:"id"`
	Test string `json:"test"`
	// Status is one of the constants above.
	Status string `json:"status"`
	// Mode is agent or replay — the field that shows what a run cost.
	Mode string `json:"mode"`
	// Reason is the sentence a person reads. For a failure it is the bug report: what was
	// expected, what happened, and where.
	Reason     string    `json:"reason"`
	StartedAt  time.Time `json:"started_at"`
	DurationMs int       `json:"duration_ms"`
	// URL the test ran against.
	URL string `json:"url,omitempty"`
	// Commit and PR tie a run to what shipped, so a failure can name the change that caused it.
	Commit string `json:"commit,omitempty"`
	PR     int    `json:"pr,omitempty"`
	// Steps are kept for failures and dropped for passes — see Append. A passing run's timeline is
	// noise, and storing every step of every green run is how this file grows without bound.
	Steps []Step `json:"steps,omitempty"`
}

// Took renders the duration the way a person reads it.
//
// Raw milliseconds are fine for a replay and unreadable for an agent run: "47210ms" is the number
// that proves replaying is worth it, and printing it in the unit nobody counts in wastes the
// comparison. Under ten seconds keeps millisecond precision, because that is where the interesting
// difference lives.
func (r Run) Took() string {
	ms := r.DurationMs
	switch {
	case ms <= 0:
		return ""
	case ms < 10_000:
		return itoa(ms) + "ms"
	case ms < 60_000:
		return itoa(ms/1000) + "." + itoa((ms%1000)/100) + "s"
	default:
		return itoa(ms/60_000) + "m " + itoa((ms%60_000)/1000) + "s"
	}
}

// OK reports whether this run needs nobody's attention. Stale does NOT count: it means we do not
// know yet.
func (r Run) OK() bool { return r.Status == StatusPassed }

// Store holds runs. Same shape and persistence discipline as the acted and alert stores.
type Store struct {
	mu    sync.Mutex
	path  string
	items []Run
	// cap bounds the file. Runs arrive on every push to every branch, so an unbounded log would be
	// the largest thing in a tenant's data directory within a month.
	cap int
}

// DefaultCap is how many runs are kept. Enough to see a week of a busy repository, and small
// enough that the whole file is read and written without thinking about it.
const DefaultCap = 500

// Open loads the store at path.
//
// An EMPTY PATH MEANS IN-MEMORY, which is the convention every sidecar here follows and which the
// acted store learned the hard way: it tried to persist to "" and crashed the demo on the first
// write with `rename .tmp: no such file`. The demo, the tests, and any read-only instance all pass
// "" and must get a working store, not an error.
func Open(path string) (*Store, error) {
	s := &Store{path: path, cap: DefaultCap}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.items); err != nil {
		// A corrupt sidecar must not take the instance down. The runs are a record, not the
		// product's source of truth, and starting empty is recoverable where refusing to boot is
		// not.
		s.items = nil
	}
	return s, nil
}

// Append records a run.
//
// It DROPS the steps of a passing run. A green run's timeline is forty lines nobody will ever
// read, and keeping them is how this file becomes the biggest thing on the disk. A failure keeps
// everything, because the timeline is the bug report.
func (s *Store) Append(r Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Status == StatusPassed {
		r.Steps = nil
	}
	s.items = append(s.items, r)
	if len(s.items) > s.cap {
		s.items = s.items[len(s.items)-s.cap:]
	}
	return s.persist()
}

// Recent returns the newest runs first, at most n.
func (s *Store) Recent(n int) []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Run(nil), s.items...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// Summary is the headline over a set of runs.
type Summary struct {
	Total   int
	Passed  int
	Failed  int
	Stale   int
	Errored int
	// Replayed is how many needed no model. The number that shows what the suite costs to run.
	Replayed int
	// SavedCalls is runs that did not pay for a model because a recording existed. Same number as
	// Replayed, named for what it means rather than for how it was counted.
	SavedCalls int
}

// Summarize counts a set of runs.
func Summarize(runs []Run) Summary {
	s := Summary{Total: len(runs)}
	for _, r := range runs {
		switch r.Status {
		case StatusPassed:
			s.Passed++
		case StatusFailed:
			s.Failed++
		case StatusStale:
			s.Stale++
		case StatusErrored:
			s.Errored++
		}
		if r.Mode == ModeReplay {
			s.Replayed++
		}
	}
	s.SavedCalls = s.Replayed
	return s
}

// Headline is the sentence over the runs list.
//
// It never reports a runner failure as an app failure, and it never counts a stale run as either
// passing or broken — those are the two ways this sentence could lie about someone's product.
func (s Summary) Headline() string {
	if s.Total == 0 {
		return "No test runs yet. Write one sentence describing something that should work, and the agent will go and check it."
	}
	switch {
	case s.Failed == 1:
		return "1 test failed. The app did not do what that test describes."
	case s.Failed > 1:
		return itoa(s.Failed) + " tests failed. The app did not do what those tests describe."
	case s.Stale > 0 && s.Errored > 0:
		return "Nothing failed, but " + plural(s.Stale, "recording") + " no longer fit the app and " + plural(s.Errored, "run") + " could not complete."
	case s.Stale > 0:
		return "Nothing failed. " + plural(s.Stale, "recording") + " no longer fit the app, so the agent is working out whether that is a rename or a bug."
	case s.Errored > 0:
		return "Nothing failed, but " + plural(s.Errored, "run") + " could not complete — that is our side, not your app."
	case s.Total == 1:
		return "The one test passed."
	default:
		return "All " + itoa(s.Total) + " tests passed."
	}
}

// CostNote says what the suite cost, in the only terms that matter: how many runs needed a model.
func (s Summary) CostNote() string {
	if s.Total == 0 {
		return ""
	}
	agent := s.Total - s.Replayed
	switch {
	case s.Replayed == 0:
		return plural(agent, "run") + " used the agent. Once a test passes it is recorded, and every run after that replays with no model at all."
	case agent == 0:
		return "Every run replayed from a recording — no model was called at all."
	default:
		return plural(s.Replayed, "run") + " replayed with no model; " + itoa(agent) +
			" needed the agent, because the recording no longer fit or the test had never passed before."
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(n) + " " + word + "s"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func (s *Store) persist() error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(s.items)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
