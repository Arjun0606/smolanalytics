// Package whatchanged finds WHEN a metric changed and WHICH commits could explain it.
//
// "Your conversion dropped 40%" is a symptom. "Your conversion dropped 40% on the 14th, and
// these three commits touched the file that fires the event that morning" is an answer, and it
// is the single most useful thing a product-analytics tool can say.
//
// No events-only tool can say it. Answering requires the repository, and a SaaS analytics vendor
// cannot get repo access — it is a security review they do not win. It works here because the
// MCP server runs in the editor, where the coding agent already has the code.
//
// Two halves, deliberately separated: Detect is pure arithmetic over a daily series and is
// tested exhaustively, while the git half is environmental and degrades to nothing when git is
// absent or the repo has no history.
package whatchanged

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Point is one day's count.
type Point struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// Change is a detected step in the series.
type Change struct {
	Found bool `json:"found"`
	// Day is the first day on the NEW level — the day the change is attributed to.
	Day        string  `json:"day,omitempty"`
	Before     float64 `json:"daily_before"`
	After      float64 `json:"daily_after"`
	PercentPct float64 `json:"change_pct"`
	Direction  string  `json:"direction,omitempty"` // "drop" | "spike"
	Verdict    string  `json:"verdict"`
}

// minSide is how many days must sit on each side of a candidate split. Below this, one noisy day
// at the edge of the window reads as a permanent step change, which is the classic way an
// automatic change detector produces confident nonsense.
const minSide = 3

// minRelative is the smallest relative move worth reporting. Real series wobble; a detector that
// fires on 5% teaches people to ignore it.
const minRelative = 0.25

// Series buckets an event into daily counts over the last `days`, oldest first. Days with no
// events are present with a zero count — a metric going to zero is the most important change
// there is, and a series that simply omits those days cannot see it.
func Series(evs []event.Event, name string, days int, now time.Time) []Point {
	if days <= 0 {
		days = 30
	}
	now = now.UTC()
	start := now.AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)
	counts := map[string]int{}
	for _, e := range evs {
		if e.Name != name {
			continue
		}
		t := e.Timestamp.UTC()
		if t.Before(start) || t.After(now) {
			continue
		}
		counts[t.Format("2006-01-02")]++
	}
	out := make([]Point, 0, days)
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		out = append(out, Point{Day: d, Count: counts[d]})
	}
	return out
}

// Detect finds the day the series most clearly stepped to a new level.
//
// Two stages, because locating a change and deciding whether to believe it want opposite
// statistics, and using either one for both produces confident nonsense on real data.
//
// LOCATING uses means, weighted by how balanced the split is (n_before·n_after/n) — the standard
// change-point statistic, which genuinely peaks at the true boundary. Medians cannot locate:
// a clean step from 40/day to 5/day has identical medians on both sides of EVERY split between
// the two levels, so the score ties across all of them and the earliest wins, reporting a change
// days before it happened and sending someone to read the wrong commits.
//
// DECIDING uses medians. Means are what let one loud day — a bot burst, a launch — manufacture a
// step that never happened. Gating on the median difference means that outlier can pull the
// located split around all it likes and still fail to clear the bar, because the typical day
// either side barely moved.
//
// The reported levels are medians too, so "40/day before" means a typical day rather than an
// average one loud afternoon has inflated.
func Detect(series []Point) Change {
	if len(series) < 2*minSide {
		return Change{Verdict: fmt.Sprintf("need at least %d days of history to say when something changed", 2*minSide)}
	}
	n := float64(len(series))
	bestScore, bestIdx := -1.0, -1
	for i := minSide; i <= len(series)-minSide; i++ {
		mb, ma := mean(series[:i]), mean(series[i:])
		d := ma - mb
		score := (float64(i) * (n - float64(i)) / n) * d * d
		if score > bestScore {
			bestScore, bestIdx = score, i
		}
	}
	if bestIdx < 0 {
		return Change{Verdict: "no clear step in this window"}
	}
	before := median(series[:bestIdx])
	after := median(series[bestIdx:])

	c := Change{
		Day:    series[bestIdx].Day,
		Before: round1(before),
		After:  round1(after),
	}
	switch {
	case before == 0 && after == 0:
		return Change{Verdict: "no events in this window at all"}
	case before == 0:
		c.PercentPct = 100
		c.Direction = "spike"
	default:
		c.PercentPct = round1(100 * (after - before) / before)
		if after < before {
			c.Direction = "drop"
		} else {
			c.Direction = "spike"
		}
	}

	rel := math.Abs(after-before) / math.Max(before, 1)
	if rel < minRelative {
		c.Verdict = fmt.Sprintf("no meaningful change: the series holds around %.1f/day across the whole window", round1(median(series)))
		return c
	}
	c.Found = true

	// A metric reaching zero is called out separately. It is almost never a product change and
	// almost always broken instrumentation or a failed deploy, and saying "down 100%" buries
	// that under a number that reads like every other number.
	if after == 0 {
		c.Verdict = fmt.Sprintf("STOPPED. %s was averaging %.1f/day and has recorded nothing since %s. An event that goes to exactly zero is usually broken tracking or a failed deploy rather than a product change.",
			"this event", before, c.Day)
		return c
	}
	c.Verdict = fmt.Sprintf("%s on %s: %.1f/day before, %.1f/day after (%+.0f%%).",
		map[string]string{"drop": "Dropped", "spike": "Jumped"}[c.Direction], c.Day, before, after, c.PercentPct)
	return c
}

// mean locates; median decides. See Detect.
func mean(ps []Point) float64 {
	if len(ps) == 0 {
		return 0
	}
	t := 0
	for _, p := range ps {
		t += p.Count
	}
	return float64(t) / float64(len(ps))
}

// median is the typical day on one side of a candidate split. Used instead of the mean because
// one outlying day must not move the level enough to invent a step change.
func median(ps []Point) float64 {
	if len(ps) == 0 {
		return 0
	}
	v := make([]int, 0, len(ps))
	for _, p := range ps {
		v = append(v, p.Count)
	}
	sort.Ints(v)
	n := len(v)
	if n%2 == 1 {
		return float64(v[n/2])
	}
	return float64(v[n/2-1]+v[n/2]) / 2
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// Commit is one repository change that touched a file capable of firing the event.
type Commit struct {
	SHA     string `json:"sha"`
	When    string `json:"when"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
	Files   string `json:"files,omitempty"`
}

// Window returns the dates to search for commits around a detected change: a few days either
// side, because a deploy on Friday shows up in Monday's numbers and blaming only the exact day
// misses the commit that actually did it.
func Window(day string, pad int) (from, to string, ok bool) {
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return "", "", false
	}
	return d.AddDate(0, 0, -pad).Format("2006-01-02"), d.AddDate(0, 0, pad+1).Format("2006-01-02"), true
}

// Rank orders candidate commits by how likely they are to be the cause: closest to the change
// day first, ties broken by SHA so the output is deterministic.
func Rank(commits []Commit, changeDay string) []Commit {
	target, err := time.Parse("2006-01-02", changeDay)
	if err != nil {
		return commits
	}
	dist := func(c Commit) float64 {
		t, err := time.Parse(time.RFC3339, c.When)
		if err != nil {
			return math.MaxFloat64
		}
		return math.Abs(t.Sub(target).Hours())
	}
	out := append([]Commit(nil), commits...)
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := dist(out[i]), dist(out[j])
		if di != dj {
			return di < dj
		}
		return out[i].SHA < out[j].SHA
	})
	return out
}
