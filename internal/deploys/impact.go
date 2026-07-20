package deploys

import (
	"math"
	"sort"
	"time"
)

// Point is one day of a metric series (mirrors trends.Point; the api/mcp layer converts
// so this package stays dependency-free and the impact math is a pure, testable leaf).
type Point struct {
	Date  time.Time
	Count int
}

// Impact is one deploy with its measured before/after effect on a metric. Nullable fields
// use pointers so they serialize as JSON null (not 0) when there isn't enough data — a
// missing number must never read as "zero change".
type Impact struct {
	Deploy
	ShortSHA    string   `json:"short_sha"`
	Before      *float64 `json:"before"`
	After       *float64 `json:"after"`
	DeltaPct    *float64 `json:"delta_pct"`
	Direction   string   `json:"direction"` // regression | improvement | flat | unknown
	Significant bool     `json:"significant"`
}

const day = "2006-01-02"

// ComputeImpact lines each deploy up against a daily metric series and compares the mean of
// the `window` days AFTER the deploy against the `window` days BEFORE. This is correlation,
// not proof, so a deploy is only flagged Significant when BOTH windows are fully populated
// AND the move is at least `threshold` (e.g. 0.25). Returned newest-first.
func ComputeImpact(series []Point, deps []Deploy, window int, threshold float64) []Impact {
	if window <= 0 {
		window = 3
	}
	if threshold <= 0 {
		threshold = 0.25
	}
	// index the series by UTC day; days sorted ascending (trends.Compute already zero-fills)
	days := make([]string, len(series))
	byDay := make(map[string]int, len(series))
	for i, p := range series {
		d := p.Date.UTC().Format(day)
		days[i] = d
		byDay[d] = p.Count
	}
	sort.Strings(days)

	out := make([]Impact, 0, len(deps))
	for _, dep := range deps {
		imp := Impact{Deploy: dep, ShortSHA: shortSHA(dep.SHA), Direction: "unknown"}
		dd := dep.At.UTC().Format(day)
		i := indexOfDay(days, dd)
		if i < 0 {
			out = append(out, imp)
			continue
		}
		before := mean(counts(days[max0(i-window):i], byDay))
		after := mean(counts(days[i:min(i+window, len(days))], byDay))
		full := i-max0(i-window) == window && min(i+window, len(days))-i == window
		imp.Before, imp.After = before, after
		if before != nil && after != nil {
			if *before == 0 {
				if *after == 0 {
					z := 0.0
					imp.DeltaPct, imp.Direction = &z, "flat"
				} else {
					imp.Direction = "improvement" // 0 -> N has no finite %, leave DeltaPct nil
				}
			} else {
				d := (*after - *before) / *before
				imp.DeltaPct = &d
				switch {
				case math.Abs(d) < threshold:
					imp.Direction = "flat"
				case d < 0:
					imp.Direction = "regression"
				default:
					imp.Direction = "improvement"
				}
			}
		}
		imp.Significant = full && imp.DeltaPct != nil && math.Abs(*imp.DeltaPct) >= threshold
		out = append(out, imp)
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].At.After(out[b].At) })
	return out
}

// Headline picks the one deploy to lead with: the most recent SIGNIFICANT regression, else
// the most recent significant change, else nothing. Regressions are the alarm; a recovery
// is good news but never the headline.
func Headline(impacts []Impact) *Impact {
	var firstSig *Impact
	for i := range impacts {
		if !impacts[i].Significant {
			continue
		}
		if impacts[i].Direction == "regression" {
			return &impacts[i]
		}
		if firstSig == nil {
			firstSig = &impacts[i]
		}
	}
	return firstSig
}

func indexOfDay(days []string, target string) int {
	for i, d := range days {
		if d == target {
			return i
		}
	}
	for i, d := range days {
		if d >= target { // deploy day has no series row → snap to the first day on/after it
			return i
		}
	}
	return -1
}

func counts(days []string, byDay map[string]int) []int {
	out := make([]int, len(days))
	for i, d := range days {
		out[i] = byDay[d]
	}
	return out
}

func mean(xs []int) *float64 {
	if len(xs) == 0 {
		return nil
	}
	sum := 0
	for _, x := range xs {
		sum += x
	}
	m := float64(sum) / float64(len(xs))
	return &m
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
