// Package heatmap turns autocaptured $click events into a click-density grid for a page, plus the
// top clicked elements — computed at query time over the existing events, no new stored config and
// no screenshots. The engine only ever emits integer coordinates; any page image is an iframe
// rendered browser-side in the cloud app, never here (a static Go binary ships no headless browser).
package heatmap

import (
	"sort"
	"strings"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

const (
	maxDocY    = 50000 // cap document-space Y so a rogue scroll value can't blow the grid
	defCols    = 40
	maxCols    = 200
	defRowPx   = 20
	topTargetN = 12
)

// Cell is one grid bucket: Row (docY / row_px), Col (0..cols-1), and N clicks in it.
type Cell struct {
	Row int `json:"row"`
	Col int `json:"col"`
	N   int `json:"n"`
}

// Target is a clicked element rolled up by a stable label, most-clicked first.
type Target struct {
	Label string `json:"label"`
	N     int    `json:"n"`
}

// Result is the heatmap for one page + viewport bucket.
type Result struct {
	Path       string   `json:"path"`
	Viewport   string   `json:"viewport"`
	Cols       int      `json:"cols"`
	RowPx      int      `json:"row_px"`
	Clicks     int      `json:"clicks"`  // total positioned clicks counted
	Max        int      `json:"max"`     // busiest cell (for canvas scaling)
	MaxRow     int      `json:"max_row"` // tallest row index seen (grid height)
	Cells      []Cell   `json:"cells"`
	TopTargets []Target `json:"top_targets"`
	Note       string   `json:"note,omitempty"`
}

// Compute aggregates $click events on `path` into a density grid. viewport is ""/"all"/"mobile"/
// "tablet"/"desktop" (bucketed by the captured viewport width). cols/rowPx default to 40/20. Pure
// and deterministic — cells and targets are emitted in a stable sort order — so /v1/heatmap and the
// MCP heatmap tool agree byte-for-byte, the same contract as every other report.
func Compute(evs []event.Event, path, viewport string, cols, rowPx int) Result {
	if cols <= 0 {
		cols = defCols
	}
	if cols > maxCols {
		cols = maxCols
	}
	if rowPx <= 0 {
		rowPx = defRowPx
	}
	res := Result{Path: path, Viewport: normViewport(viewport), Cols: cols, RowPx: rowPx}
	grid := map[[2]int]int{}
	targets := map[string]int{}

	for _, e := range evs {
		if e.Name != "$click" {
			continue
		}
		if p, _ := e.Properties["path"].(string); p != path {
			continue
		}
		vw := toInt(e.Properties["vw"])
		if vw <= 0 {
			continue // pre-heatmap clicks (no captured viewport) can't be positioned
		}
		if res.Viewport != "all" && bucket(vw) != res.Viewport {
			continue
		}
		x := toInt(e.Properties["x"])
		docY := toInt(e.Properties["y"]) + toInt(e.Properties["sy"])
		if docY < 0 {
			docY = 0
		}
		if docY > maxDocY {
			docY = maxDocY
		}
		col := x * cols / vw
		if col < 0 {
			col = 0
		}
		if col >= cols {
			col = cols - 1
		}
		row := docY / rowPx
		grid[[2]int{row, col}]++
		res.Clicks++
		if row > res.MaxRow {
			res.MaxRow = row
		}
		if lbl := label(e.Properties); lbl != "" {
			targets[lbl]++
		}
	}

	for k, n := range grid {
		res.Cells = append(res.Cells, Cell{Row: k[0], Col: k[1], N: n})
		if n > res.Max {
			res.Max = n
		}
	}
	sort.Slice(res.Cells, func(i, j int) bool {
		if res.Cells[i].Row != res.Cells[j].Row {
			return res.Cells[i].Row < res.Cells[j].Row
		}
		return res.Cells[i].Col < res.Cells[j].Col
	})
	for lbl, n := range targets {
		res.TopTargets = append(res.TopTargets, Target{Label: lbl, N: n})
	}
	sort.Slice(res.TopTargets, func(i, j int) bool {
		if res.TopTargets[i].N != res.TopTargets[j].N {
			return res.TopTargets[i].N > res.TopTargets[j].N
		}
		return res.TopTargets[i].Label < res.TopTargets[j].Label
	})
	if len(res.TopTargets) > topTargetN {
		res.TopTargets = res.TopTargets[:topTargetN]
	}
	if res.Clicks == 0 {
		res.Note = "no positioned clicks for this page + viewport yet — heatmaps need $click events carrying a captured viewport width (SDK v0.9.8+)"
	}
	return res
}

func normViewport(v string) string {
	switch strings.ToLower(v) {
	case "mobile", "tablet", "desktop":
		return strings.ToLower(v)
	default:
		return "all"
	}
}

func bucket(vw int) string {
	switch {
	case vw < 768:
		return "mobile"
	case vw <= 1024:
		return "tablet"
	default:
		return "desktop"
	}
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// label rolls a click up to a stable, readable target: the trimmed text, else tag#id / tag href,
// else the bare tag.
func label(props map[string]any) string {
	if t, _ := props["text"].(string); t != "" {
		return t
	}
	tag, _ := props["tag"].(string)
	if id, _ := props["id"].(string); id != "" {
		return tag + "#" + id
	}
	if href, _ := props["href"].(string); href != "" {
		return tag + " " + href
	}
	return tag
}
