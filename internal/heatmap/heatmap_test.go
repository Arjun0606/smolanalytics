package heatmap

import (
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func click(path string, x, y, vw, sy int, text string) event.Event {
	props := map[string]any{"path": path, "x": float64(x), "y": float64(y), "vw": float64(vw), "tag": "button"}
	if sy > 0 {
		props["sy"] = float64(sy)
	}
	if text != "" {
		props["text"] = text
	}
	return event.Event{Name: "$click", Properties: props}
}

func TestComputeAggregatesAndTargets(t *testing.T) {
	evs := []event.Event{
		click("/pricing", 100, 40, 1000, 0, "Buy"),   // col=4, row=2
		click("/pricing", 110, 45, 1000, 0, "Buy"),   // col=4, row=2 (same cell)
		click("/pricing", 500, 200, 1000, 0, "Docs"), // col=20, row=10
		click("/other", 100, 40, 1000, 0, "x"),       // different path → excluded
	}
	r := Compute(evs, "/pricing", "all", 40, 20)
	if r.Clicks != 3 {
		t.Fatalf("clicks = %d, want 3", r.Clicks)
	}
	if r.Max != 2 {
		t.Fatalf("max = %d, want 2 (the busy cell)", r.Max)
	}
	found := false
	for _, c := range r.Cells {
		if c.Row == 2 && c.Col == 4 {
			found = true
			if c.N != 2 {
				t.Fatalf("cell (2,4) N = %d, want 2", c.N)
			}
		}
	}
	if !found {
		t.Fatal("expected a cell at (row 2, col 4)")
	}
	if len(r.TopTargets) == 0 || r.TopTargets[0].Label != "Buy" || r.TopTargets[0].N != 2 {
		t.Fatalf("top target should be Buy x2, got %+v", r.TopTargets)
	}
}

func TestComputeDeterministic(t *testing.T) {
	evs := []event.Event{
		click("/p", 10, 10, 800, 0, "a"),
		click("/p", 700, 500, 800, 0, "b"),
		click("/p", 400, 300, 800, 100, "a"),
	}
	a := Compute(evs, "/p", "all", 40, 20)
	b := Compute(evs, "/p", "all", 40, 20)
	if len(a.Cells) != len(b.Cells) {
		t.Fatal("cell counts differ between runs")
	}
	for i := range a.Cells {
		if a.Cells[i] != b.Cells[i] {
			t.Fatalf("cells not deterministic at %d: %+v vs %+v", i, a.Cells[i], b.Cells[i])
		}
	}
}

func TestComputeViewportBucket(t *testing.T) {
	evs := []event.Event{
		click("/p", 100, 40, 375, 0, "m"),  // mobile (vw<768)
		click("/p", 100, 40, 1440, 0, "d"), // desktop
	}
	if r := Compute(evs, "/p", "mobile", 40, 20); r.Clicks != 1 {
		t.Fatalf("mobile bucket: clicks = %d, want 1", r.Clicks)
	}
	if r := Compute(evs, "/p", "desktop", 40, 20); r.Clicks != 1 {
		t.Fatalf("desktop bucket: clicks = %d, want 1", r.Clicks)
	}
	if r := Compute(evs, "/p", "all", 40, 20); r.Clicks != 2 {
		t.Fatalf("all: clicks = %d, want 2", r.Clicks)
	}
}

func TestComputeSkipsUncoordinated(t *testing.T) {
	evs := []event.Event{{Name: "$click", Properties: map[string]any{"path": "/p", "x": float64(10), "y": float64(10)}}}
	r := Compute(evs, "/p", "all", 40, 20)
	if r.Clicks != 0 || r.Note == "" {
		t.Fatalf("uncoordinated click should be skipped with a note, got %+v", r)
	}
}

func TestComputeCapsDocY(t *testing.T) {
	evs := []event.Event{click("/p", 100, 40, 1000, 9999999, "x")}
	r := Compute(evs, "/p", "all", 40, 20)
	if r.MaxRow > maxDocY/20 {
		t.Fatalf("docY not capped: maxRow = %d", r.MaxRow)
	}
}
