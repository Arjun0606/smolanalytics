package segment

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/blob"
)

// Concurrent ingest from many goroutines while seals fire constantly (tiny sealAt),
// then assert nothing was lost/duplicated and time-window scans are exact. Run with -race.
func TestConcurrentIngestNoLossOrDup(t *testing.T) {
	dir := t.TempDir()
	b, _ := blob.NewLocal(dir + "/cold")
	s, err := Open(dir+"/hot.data", b, 137) // odd, small sealAt → frequent seals at boundaries
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	const G, per = 8, 1000
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				id := fmt.Sprintf("g%d-%d", g, i)
				ts := base.Add(time.Duration(g*per+i) * time.Second)
				if err := s.Ingest(event.Event{ID: id, Name: "ev", DistinctID: "u", Timestamp: ts}); err != nil {
					t.Errorf("ingest: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	total := G * per
	if c := s.Count(); c != total {
		t.Fatalf("count=%d want %d (loss or dup under concurrency)", c, total)
	}
	// every unique ID present exactly once
	seen := map[string]int{}
	_ = s.Scan(time.Time{}, time.Time{}, func(e event.Event) error { seen[e.ID]++; return nil })
	if len(seen) != total {
		t.Fatalf("distinct ids scanned=%d want %d", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("id %s seen %d times", id, n)
		}
	}
	// a narrow window returns exactly the events in it
	from := base.Add(500 * time.Second)
	to := base.Add(1500 * time.Second)
	win := 0
	_ = s.Scan(from, to, func(e event.Event) error { win++; return nil })
	if win != 1000 {
		t.Fatalf("window scan=%d want 1000", win)
	}

	// durable across reopen
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir+"/hot.data", b, 137)
	if err != nil {
		t.Fatal(err)
	}
	if c := s2.Count(); c != total {
		t.Fatalf("after reopen count=%d want %d", c, total)
	}
}
