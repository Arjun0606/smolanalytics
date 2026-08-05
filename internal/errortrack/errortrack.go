// Package errortrack turns the $exception events the SDK already captures into an error report:
// grouped, ranked by the people they hit, and joined to the funnel they broke.
//
// The join is the whole reason this lives here rather than in a separate tool. Sentry knows your
// errors and nothing about your funnel. Product analytics knows your funnel and nothing about
// your errors. Everyone ends up correlating two dashboards by eye and guessing. This instance
// holds both streams as the same events, so "this exception is on the checkout step and the 41
// people who hit it never came back" is one query rather than a meeting.
package errortrack

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// ExceptionEvent is what sdk.js records for an uncaught error or a rejected promise.
const ExceptionEvent = "$exception"

// Group is one distinct error, however many times it fired.
type Group struct {
	// Fingerprint identifies the error ACROSS occurrences and releases. Stable by construction —
	// it is what makes "is this the same bug as last week" answerable.
	Fingerprint string `json:"fingerprint"`
	// Title is the normalised message: the shape of the error, with the varying parts removed.
	Title   string    `json:"title"`
	Sample  string    `json:"sample"`           // one verbatim message, so nothing is lost to normalisation
	Source  string    `json:"source,omitempty"` // file
	Line    int       `json:"line,omitempty"`
	Kind    string    `json:"kind,omitempty"`  // "unhandledrejection" when it was a promise
	Count   int       `json:"count"`           // occurrences
	Users   int       `json:"users"`           // DISTINCT people — the number that should rank this list
	Paths   []string  `json:"paths,omitempty"` // where it fires, most common first
	FirstAt time.Time `json:"first_at"`
	LastAt  time.Time `json:"last_at"`
	// New says this error was not seen before the window opened. A regression and a long-standing
	// annoyance need opposite reactions, and a list that cannot tell them apart is a backlog.
	New bool `json:"new"`
}

// Result is the error report for a window.
type Result struct {
	Groups []Group `json:"groups"`
	// Users and UsersAffected are the crash-free read: how many people were active, and how many
	// of them hit at least one error. A rate is more honest than a count — 400 errors is
	// meaningless without knowing whether that is four users or four hundred.
	Users         int     `json:"users"`
	UsersAffected int     `json:"users_affected"`
	CrashFreePct  float64 `json:"crash_free_pct"`
	Total         int     `json:"total"` // total exception events in the window
	Note          string  `json:"note,omitempty"`
}

// Normalisation. An error message carries two things: the shape of the failure, which is the
// same every time, and the specifics, which are never the same twice. "User 12345 not found" and
// "User 67890 not found" are one bug; grouping on the raw string makes them two, and a list where
// every occurrence is its own row is the failure mode that makes error trackers unusable.
//
// The risk runs the other way too. Strip too much and genuinely different failures merge into one
// row that cannot be acted on, which is worse because it is silent. These patterns are
// deliberately conservative: identifiers that are obviously values, nothing that could be a name.
var (
	reHex    = regexp.MustCompile(`\b(?:0x)?[0-9a-fA-F]{8,}\b`) // ids, hashes, addresses
	reUUID   = regexp.MustCompile(`\b[0-9a-fA-F-]{32,}\b`)      // uuids in any dialect
	reNum    = regexp.MustCompile(`\b\d+\b`)                    // counters, ids, offsets
	reURL    = regexp.MustCompile(`https?://[^\s'"]+`)          // endpoints differ per env
	reQuoted = regexp.MustCompile(`'[^']{0,80}'|"[^"]{0,80}"`)  // interpolated values
)

// Normalise reduces a message to its shape. Order matters: URLs before numbers, or a URL's port
// and path segments become <n> and the URL stops being recognisable as one.
func Normalise(msg string) string {
	s := strings.TrimSpace(msg)
	s = reURL.ReplaceAllString(s, "<url>")
	s = reUUID.ReplaceAllString(s, "<id>")
	s = reHex.ReplaceAllString(s, "<id>")
	s = reQuoted.ReplaceAllString(s, "<value>")
	s = reNum.ReplaceAllString(s, "<n>")
	s = strings.Join(strings.Fields(s), " ") // collapse whitespace so wrapping cannot split a group
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Fingerprint identifies an error across occurrences.
//
// Source and line are included when present because the same generic message ("Cannot read
// properties of undefined") from two different files is two different bugs, and merging them
// produces a row nobody can fix. The COLUMN is deliberately excluded: it moves when a minifier
// reflows a line, which would split one bug into a new group on every deploy.
func Fingerprint(msg, source string, line int) string {
	parts := []string{Normalise(msg)}
	if source != "" {
		parts = append(parts, baseOf(source))
	}
	if line > 0 {
		parts = append(parts, itoa(line))
	}
	return shortHash(strings.Join(parts, "|"))
}

// Compute builds the error report over [from, to).
//
// `priorFrom` is the start of the preceding equal window, used only to decide New. Zero disables
// the new/known split rather than guessing.
func Compute(evs []event.Event, from, to, priorFrom time.Time) Result {
	type acc struct {
		g       Group
		users   map[string]bool
		paths   map[string]int
		seenPre bool
	}
	groups := map[string]*acc{}
	activeUsers := map[string]bool{}
	affected := map[string]bool{}
	res := Result{}

	inWindow := func(t time.Time) bool {
		return !t.Before(from) && (to.IsZero() || t.Before(to))
	}

	for _, e := range evs {
		ts := e.Timestamp.UTC()
		// Everyone active in the window is the denominator for the crash-free rate. Counting only
		// people who errored would make the rate a tautology.
		if inWindow(ts) {
			activeUsers[e.DistinctID] = true
		}
		if e.Name != ExceptionEvent {
			continue
		}
		msg, _ := e.Properties["message"].(string)
		src, _ := e.Properties["source"].(string)
		kind, _ := e.Properties["kind"].(string)
		path, _ := e.Properties["path"].(string)
		line := intOf(e.Properties["lineno"])
		fp := Fingerprint(msg, src, line)

		// A pre-window occurrence marks the group as known WITHOUT counting toward the window's
		// numbers — that is the only way "new" can mean anything.
		if !priorFrom.IsZero() && ts.Before(from) && !ts.Before(priorFrom) {
			if a := groups[fp]; a != nil {
				a.seenPre = true
			} else {
				groups[fp] = &acc{users: map[string]bool{}, paths: map[string]int{}, seenPre: true}
			}
			continue
		}
		if !inWindow(ts) {
			continue
		}

		res.Total++
		affected[e.DistinctID] = true
		a := groups[fp]
		if a == nil {
			a = &acc{users: map[string]bool{}, paths: map[string]int{}}
			groups[fp] = a
		}
		a.users[e.DistinctID] = true
		if path != "" {
			a.paths[path]++
		}
		a.g.Count++
		if a.g.Fingerprint == "" {
			a.g.Fingerprint, a.g.Title, a.g.Sample = fp, Normalise(msg), msg
			a.g.Source, a.g.Line, a.g.Kind = src, line, kind
			a.g.FirstAt, a.g.LastAt = ts, ts
		}
		if ts.Before(a.g.FirstAt) {
			a.g.FirstAt = ts
		}
		if ts.After(a.g.LastAt) {
			a.g.LastAt = ts
		}
	}

	for fp, a := range groups {
		if a.g.Count == 0 {
			continue // seen only before the window; it is context, not a row
		}
		g := a.g
		g.Fingerprint = fp
		g.Users = len(a.users)
		g.New = !priorFrom.IsZero() && !a.seenPre
		g.Paths = topKeys(a.paths, 5)
		res.Groups = append(res.Groups, g)
	}

	// Ranked by PEOPLE, not occurrences. One user stuck in a retry loop can produce ten thousand
	// events, and a list sorted by count puts them at the top while a bug hitting a quarter of
	// your customers sits below the fold. Ties break on count, then fingerprint, so the order is
	// total and two runs agree.
	sort.Slice(res.Groups, func(i, j int) bool {
		a, b := res.Groups[i], res.Groups[j]
		if a.Users != b.Users {
			return a.Users > b.Users
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Fingerprint < b.Fingerprint
	})

	res.Users, res.UsersAffected = len(activeUsers), len(affected)
	if res.Users > 0 {
		res.CrashFreePct = round1(100 * float64(res.Users-res.UsersAffected) / float64(res.Users))
	} else {
		// No activity at all is not 100% crash-free. A perfect score off an empty window is the
		// kind of number someone screenshots.
		res.Note = "no activity in this window, so there is no crash-free rate to report"
	}
	return res
}

func baseOf(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 && i+1 < len(p) {
		return p[i+1:]
	}
	return p
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func topKeys(m map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	all := make([]kv, 0, len(m))
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v != all[j].v {
			return all[i].v > all[j].v
		}
		return all[i].k < all[j].k
	})
	if len(all) > n {
		all = all[:n]
	}
	out := make([]string, 0, len(all))
	for _, x := range all {
		out = append(out, x.k)
	}
	return out
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// shortHash is a stable 12-hex identity for a fingerprint string. FNV-1a rather than sha256:
// this is a grouping key, not a security boundary, and it is computed once per event on a hot
// path. Pinned here because a fingerprint that changes between versions silently splits every
// existing error into a new group and makes "is this the same bug as last week" unanswerable.
func shortHash(s string) string {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	const hexd = "0123456789abcdef"
	out := make([]byte, 12)
	for i := 11; i >= 0; i-- {
		out[i] = hexd[h&0xf]
		h >>= 4
	}
	return string(out)
}
