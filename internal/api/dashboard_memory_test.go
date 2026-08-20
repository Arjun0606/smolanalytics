package api

import (
	"fmt"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// The dashboard is the page a trial user opens first, and it is where memory used to
// track TOTAL HISTORY rather than the query: it loaded every event ever, then built a
// SECOND full slice by filtering the first. That is why gtm/SCALE.md measured 1.9 GB at
// rest for 2M events and ~3.9 GB after one query — the doubling was the filter.
//
// These tests measure the real handler, because the only bugs this codebase has ever had
// came from running it rather than reading it.

func loadEvents(t testing.TB, n int) *memory.Store {
	t.Helper()
	st := memory.New()
	base := time.Now().Add(-30 * 24 * time.Hour)
	names := []string{"pageview", "signup", "checkout", "click", "identify"}
	countries := []string{"US", "GB", "DE", "IN", "FR"}
	paths := []string{"/", "/pricing", "/docs", "/blog", "/app"}
	batch := make([]event.Event, 0, 1000)
	for i := 0; i < n; i++ {
		env := "production"
		if i%10 == 0 { // 10% development traffic, the realistic mix
			env = "development"
		}
		batch = append(batch, event.Event{
			ID:         fmt.Sprintf("evt_%d", i),
			Name:       names[i%len(names)],
			DistinctID: fmt.Sprintf("user_%d", i%5000),
			Timestamp:  base.Add(time.Duration(i) * time.Second),
			Properties: map[string]any{
				"env": env, "site": "example.com", "country": countries[i%len(countries)],
				"path": paths[i%len(paths)], "device": "desktop", "browser": "Chrome",
			},
		})
		if len(batch) == cap(batch) {
			if err := st.Ingest(batch...); err != nil {
				t.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if err := st.Ingest(batch...); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// allocatedBy reports how many bytes fn allocates. TotalAlloc is cumulative and counts
// every allocation whether or not it is later freed, which is exactly the question here:
// a garbage-collected second copy of history still had to be built, still cost the time,
// and still set the peak the box has to survive. Measuring HeapAlloc after the fact would
// report a tidy number for a render that briefly doubled memory and then cleaned up.
func allocatedBy(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// TestDashboardDoesNotDoubleMaterialize measures the real handler against the shape it
// replaced. The old dashboard called Range (one full slice of history) and then
// query.Apply on the result (a second full slice). The new one scans once and keeps only
// what passes the filter, so the second copy never exists.
func TestDashboardDoesNotDoubleMaterialize(t *testing.T) {
	const n = 200_000
	st := loadEvents(t, n)
	s := New(st)

	newPath := allocatedBy(func() {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		s.dashboard(w, req)
		if w.Code != 200 {
			t.Fatalf("dashboard returned %d, want 200", w.Code)
		}
	})

	// The exact load the handler used to do, measured on the same data in the same run.
	oldPath := allocatedBy(func() {
		all, err := st.Range(time.Time{}, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		filtered := query.Apply(all, nil)
		runtime.KeepAlive(all)
		runtime.KeepAlive(filtered)
	})

	runtime.KeepAlive(s)
	perEvent := float64(newPath) / n
	t.Logf("%d events — whole page render: %.0f MB (%.1f KB/event)", n, float64(newPath)/1e6, perEvent/1024)
	t.Logf("%d events — the load step alone (Range + Apply): %.0f MB", n, float64(oldPath)/1e6)

	// A budget, measured rather than guessed. Rendering computes roughly fifteen reports, so
	// it legitimately costs more than loading; the question is whether any of them scale by
	// COPYING history. Measured on this fixture: 6.3 KB/event when segmentBlame chained eight
	// first-touch stamps across every event, 3.8 KB/event once stamping was batched and
	// narrowed to the two event names it actually reads.
	//
	// 6.5 KB/event, raised from 5 once the ledger, the tracking detector and the standing-orders
	// composition landed. The raise is measured, not conceded: allocation was checked at three
	// sizes on this fixture and came out flat at 5.22 / 5.41 / 5.05 KB/event for 50k / 100k / 200k,
	// which is exactly what "more reports, none of them copying history" looks like. The old 5 KB
	// was set when the page computed fifteen reports; it now computes more.
	//
	// An absolute ceiling alone is a weak guard, because every honest feature nudges it up and the
	// number gets raised again until it means nothing. The pathology it was written for —
	// per-property stamping chaining copies across every event — is SUPERLINEAR, so the real test
	// is the linearity check below, which no amount of feature growth can quietly satisfy.
	const budgetPerEvent = 6.5 * 1024
	if perEvent > budgetPerEvent {
		t.Errorf("dashboard render allocates %.1f KB/event, budget is %.1f KB — something is copying history per property again",
			perEvent/1024, float64(budgetPerEvent)/1024)
	}

	// THE PROPERTY, not the number: cost per event must not GROW with the number of events.
	// segmentBlame's eight chained first-touch stamps read as 6.3 KB/event at this size and would
	// have read far worse at ten times it; a flat per-event cost is the signature of code that
	// scans history rather than copying it.
	small := loadEvents(t, n/4)
	ss := New(small)
	smallAlloc := allocatedBy(func() {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		ss.dashboard(w, req)
	})
	smallPerEvent := float64(smallAlloc) / float64(n/4)
	if perEvent > smallPerEvent*1.4 {
		t.Errorf("per-event allocation GREW with scale: %.2f KB/event at %d vs %.2f KB/event at %d — that is superlinear, which means something is copying history rather than scanning it",
			perEvent/1024, n, smallPerEvent/1024, n/4)
	}
	t.Logf("linearity: %.2f KB/event at %d vs %.2f KB/event at %d", perEvent/1024, n, smallPerEvent/1024, n/4)
}

// TestKeeperMatchesApply is the parity gate. Keeper exists so a streaming caller can filter
// during a scan; if it ever disagreed with Apply, two screens would quietly report different
// numbers for the same question, which is the single worst failure this product can have.
func TestKeeperMatchesApply(t *testing.T) {
	evs := []event.Event{
		{ID: "1", Name: "a", Properties: map[string]any{"env": "production", "plan": "pro"}},
		{ID: "2", Name: "b", Properties: map[string]any{"env": "development", "plan": "pro"}},
		{ID: "3", Name: "a", Properties: map[string]any{"env": "preview", "plan": "free"}},
		{ID: "4", Name: "c", Properties: map[string]any{"plan": "free"}}, // no env at all
		{ID: "5", Name: "a", Properties: nil},                            // no properties at all
		{ID: "6", Name: "b", Properties: map[string]any{"env": "staging", "plan": "pro"}},
	}
	cases := [][]query.Filter{
		nil,
		{{Property: "plan", Op: query.Eq, Value: "pro"}},
		{{Property: "env", Op: query.Eq, Value: "development"}}, // explicit env opts out of the default hide
		{{Property: "env", Op: query.Eq, Value: "preview"}},
		{{Property: "plan", Op: query.Eq, Value: "free"}, {Property: "name", Op: query.Eq, Value: "a"}},
		{{Property: "missing", Op: query.Eq, Value: "x"}},
	}
	for i, filters := range cases {
		want := query.Apply(evs, filters)
		keep := query.Keeper(filters)
		var got []event.Event
		for _, e := range evs {
			if keep(e) {
				got = append(got, e)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("case %d: Keeper kept %d events, Apply kept %d", i, len(got), len(want))
		}
		for j := range want {
			if got[j].ID != want[j].ID {
				t.Fatalf("case %d position %d: Keeper kept %q, Apply kept %q", i, j, got[j].ID, want[j].ID)
			}
		}
	}
}
