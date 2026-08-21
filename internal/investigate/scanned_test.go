package investigate

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Scanned is a list of METRIC NAMES and may contain nothing else.
//
// It carried the string "N experiments" for as long as the page only printed len(Scanned). The
// moment the ledger started NAMING the metrics it swept, a fresh instance read "2 metrics swept
// against their own trailing baselines: signup, 0 experiments" — a count sentence rendered as if
// it were one of the reader's own events. Caught by looking at the page, not the code.
func TestScannedContainsOnlyEventNames(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for d := 20; d >= 0; d-- {
		for i := 0; i < 10; i++ {
			evs = append(evs, event.Event{
				ID: fmt.Sprintf("e%d_%d", d, i), Name: "signup",
				DistinctID: fmt.Sprintf("u%d_%d", d, i),
				Timestamp:  now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Hour),
			})
		}
	}
	names := map[string]bool{}
	for _, e := range evs {
		names[e.Name] = true
	}
	// WithContext is the path that used to contaminate the list; Run alone never did.
	inv := WithContext(evs, nil, nil, Opts{Now: now})
	for _, s := range inv.Scanned {
		if !names[s] {
			t.Errorf("Scanned holds %q, which is not an event in this instance — a list of names may only contain names", s)
		}
		if strings.ContainsAny(s, " ") {
			t.Errorf("Scanned holds %q: an event name with a space in it is almost certainly a rendered sentence", s)
		}
	}
	if len(inv.Scanned) == 0 {
		t.Fatal("nothing was scanned, so this guard proved nothing")
	}
}
