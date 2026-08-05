package query

// Regressions for the segmentation defects found in the 2026-08-04 hunt: an OR row that
// re-applied the dev-traffic exclusion, gt/lt coercing non-numeric values to 0, a JSON null
// getting its own breakdown label, and the acquisition-filter surface split (first-touch on
// /v1+MCP, any-touch on the dashboard and ask bar).

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// countName is how many events of a given name survive a filter path — the shape every
// surface ultimately reports ("signups where device=mobile").
func countName(evs []event.Event, name string) int {
	n := 0
	for _, e := range evs {
		if e.Name == name {
			n++
		}
	}
	return n
}

// The dashboard emits f=env:eq:development together with fm=any whenever "show dev traffic" is
// on while the filter builder is in OR mode. ApplyMode built its OR base with Apply(evs, nil),
// so Keeper saw no env filter and stripped every development event before the OR ran: the page
// blanked instead of showing the dev traffic that was explicitly asked for.
func TestApplyModeAnyHonorsExplicitEnvFilter(t *testing.T) {
	evs := []event.Event{
		{ID: "1", Name: "signup", DistinctID: "u1", Properties: map[string]any{"env": "development", "source": "hn"}},
		{ID: "2", Name: "signup", DistinctID: "u2", Properties: map[string]any{"env": "production", "source": "google"}},
	}
	filters := []Filter{
		{Property: "env", Op: Eq, Value: "development"},
		{Property: "source", Op: Eq, Value: "nope"},
	}

	all := ApplyMode(evs, filters[:1], false)
	if len(all) != 1 {
		t.Fatalf("all-mode env=development matched %d, want 1 (baseline)", len(all))
	}
	any := ApplyMode(evs, filters, true)
	if len(any) != 1 || any[0].ID != "1" {
		t.Errorf("any-mode (env=development OR source=nope) matched %d events %v, want the 1 development event — "+
			"the OR base re-applied the dev exclusion the env filter opts out of", len(any), ids(any))
	}

	// and the default is untouched: with no env filter anywhere, dev traffic still stays hidden
	// in OR mode exactly as it is in AND mode.
	noEnv := ApplyMode(evs, []Filter{
		{Property: "source", Op: Eq, Value: "hn"},
		{Property: "source", Op: Eq, Value: "google"},
	}, true)
	if len(noEnv) != 1 || noEnv[0].ID != "2" {
		t.Errorf("any-mode without an env filter matched %v, want only the production event", ids(noEnv))
	}
}

// gt/lt used to run toNum over the EVENT's value, which answers 0 for "n/a", true and objects,
// so `amount lt 100` matched every non-numeric event and lt/gt no longer partitioned the data.
func TestGtLtIgnoreNonNumericEventValues(t *testing.T) {
	evs := []event.Event{
		{ID: "1", Name: "p", DistinctID: "a", Properties: map[string]any{"amount": 50.0}},
		{ID: "2", Name: "p", DistinctID: "b", Properties: map[string]any{"amount": "n/a"}},
		{ID: "3", Name: "p", DistinctID: "c", Properties: map[string]any{"amount": true}},
		{ID: "4", Name: "p", DistinctID: "d", Properties: map[string]any{"amount": 500.0}},
		{ID: "5", Name: "p", DistinctID: "e", Properties: map[string]any{"amount": map[string]any{"x": 1}}},
		{ID: "6", Name: "p", DistinctID: "f", Properties: map[string]any{"amount": "75"}}, // numeric STRING still counts
	}
	lt := Apply(evs, []Filter{{Property: "amount", Op: Lt, Value: 100.0}})
	if len(lt) != 2 || !ids(lt)["a"] || !ids(lt)["f"] {
		t.Errorf("amount lt 100 matched %v, want {a (50), f (\"75\")} — \"n/a\"/true/{} are not 0", ids(lt))
	}
	gt := Apply(evs, []Filter{{Property: "amount", Op: Gt, Value: 100.0}})
	if len(gt) != 1 || !ids(gt)["d"] {
		t.Errorf("amount gt 100 matched %v, want {d (500)}", ids(gt))
	}
	// lt and gt must describe the same population from opposite sides: no event may be counted
	// by both, and the non-numeric ones by neither.
	for _, e := range lt {
		if ids(gt)[e.DistinctID] {
			t.Errorf("event %q matched BOTH lt 100 and gt 100", e.DistinctID)
		}
	}
}

// One group, two labels: query.Breakdown keyed a JSON null as "" while trends keyed it
// "<nil>", and the drill-down filter the legend generates matched neither. A null means the
// property is absent, so it belongs in "(none)" like every other missing value.
func TestBreakdownBucketsJSONNullAsNone(t *testing.T) {
	evs := []event.Event{
		{ID: "1", Name: "signup", DistinctID: "a", Properties: map[string]any{"plan": nil}},
		{ID: "2", Name: "signup", DistinctID: "b", Properties: map[string]any{"plan": nil}},
		{ID: "3", Name: "signup", DistinctID: "c", Properties: map[string]any{"plan": "pro"}},
		{ID: "4", Name: "signup", DistinctID: "d", Properties: map[string]any{}}, // genuinely missing
	}
	got := map[string]int{}
	for _, g := range Breakdown(evs, "plan") {
		got[g.Value] = g.Count
	}
	if got["(none)"] != 3 || got["pro"] != 1 || len(got) != 2 {
		t.Errorf("breakdown by plan = %v, want {(none):3, pro:1} — a null and a missing property are the same group", got)
	}
	if _, bad := got[""]; bad {
		t.Errorf("breakdown produced an empty-string group for a JSON null: %v", got)
	}
}

// The acquisition covenant, for a user whose attribute CHANGED between sessions. u1 lands on
// desktop, returns on mobile, signs up. First-touch stamping overwrote the mobile pageview with
// desktop, so /v1 + MCP answered 0 while the dashboard (ScopeUsers) and ask bar (segFilterUsers)
// answered 1 for the identical question.
func TestAcquisitionFilterAgreesForUsersWhoseAttributeChanged(t *testing.T) {
	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	evs := []event.Event{
		{ID: "1", Name: "$pageview", DistinctID: "u1", Timestamp: t0, Properties: map[string]any{"device": "desktop"}},
		{ID: "2", Name: "$pageview", DistinctID: "u1", Timestamp: t0.AddDate(0, 0, 2), Properties: map[string]any{"device": "mobile"}},
		{ID: "3", Name: "signup", DistinctID: "u1", Timestamp: t0.AddDate(0, 0, 2), Properties: map[string]any{"plan": "free"}},
		// u2 never changes device — the single-valued case every existing covenant test uses
		{ID: "4", Name: "$pageview", DistinctID: "u2", Timestamp: t0, Properties: map[string]any{"device": "desktop"}},
		{ID: "5", Name: "signup", DistinctID: "u2", Timestamp: t0, Properties: map[string]any{"plan": "pro"}},
	}
	for _, want := range []string{"mobile", "desktop"} {
		fs := []Filter{{Property: "device", Op: Eq, Value: want}}
		v1 := countName(ApplyMode(StampForFilters(evs, fs), fs, false), "signup") // GET /v1 + MCP
		dash := countName(ScopeUsers(evs, fs, false), "signup")                   // dashboard + ask bar
		if v1 != dash {
			t.Errorf("signups where device=%s: /v1+MCP = %d, dashboard/ask = %d — one question, two answers", want, v1, dash)
		}
		if want == "mobile" && v1 != 1 {
			t.Errorf("signups where device=mobile = %d, want 1 (u1 signed up in a mobile session)", v1)
		}
		if want == "desktop" && v1 != 2 {
			t.Errorf("signups where device=desktop = %d, want 2 (u1 landed on desktop, u2 is desktop-only)", v1)
		}
	}

	// the single-valued user must be unaffected: filtering on a device u2 never used excludes them.
	fs := []Filter{{Property: "device", Op: Eq, Value: "tablet"}}
	if n := countName(ApplyMode(StampForFilters(evs, fs), fs, false), "signup"); n != 0 {
		t.Errorf("signups where device=tablet = %d, want 0 — nobody was ever on a tablet", n)
	}
}
