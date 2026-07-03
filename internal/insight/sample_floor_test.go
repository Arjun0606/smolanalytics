package insight

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// A week-over-week percentage on a tiny base reads as noise ("up 50%" when it
// went 2→3). Below minSample the finding must vanish entirely; past the floor
// but under smallSample it must carry the explicit qualifier; on a real base it
// stands alone.
func TestWeekOverWeekSampleGuard(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name          string
		prev7, last7  int
		wantFinding   bool
		wantQualifier bool
	}{
		{"2to3 jump suppressed", 2, 3, false, false},
		{"40to60 jump qualified", 40, 60, true, true},
		{"400to600 jump clean", 400, 600, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var evs []event.Event
			for i := 0; i < tc.prev7; i++ {
				evs = append(evs, ev("signup", now.Add(-10*24*time.Hour)))
			}
			for i := 0; i < tc.last7; i++ {
				evs = append(evs, ev("signup", now.Add(-3*24*time.Hour)))
			}
			fs := Generate(evs)
			var wow Finding
			found := false
			for _, f := range fs {
				if strings.Contains(f.Title, "week-over-week") {
					wow, found = f, true
				}
			}
			if found != tc.wantFinding {
				t.Fatalf("week-over-week finding present=%v, want %v (%+v)", found, tc.wantFinding, wow)
			}
			if !found {
				// nothing else may dress the tiny base up as a percentage swing either
				if f, ok := findAnomaly(fs); ok {
					t.Fatalf("tiny base must not surface as an anomaly: %+v", f)
				}
				return
			}
			if !strings.Contains(wow.Title, "up 50%") {
				t.Fatalf("want a 50%% jump, got %q", wow.Title)
			}
			note := fmt.Sprintf("(n=%d — small sample)", tc.prev7)
			if got := strings.Contains(wow.Detail, note); got != tc.wantQualifier {
				t.Fatalf("qualifier %q present=%v, want %v: %q", note, got, tc.wantQualifier, wow.Detail)
			}
		})
	}
}
