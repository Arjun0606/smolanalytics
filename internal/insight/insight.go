// Package insight produces the proactive "what's broken / what to look at" digest
// — the verdict founders actually want instead of a dashboard. Every finding is
// computed exactly from the deterministic engine, so it can't be hallucinated.
// Shared by the dashboard, the /v1/notable API, the MCP tool, and the daily brief.
package insight

import (
	"fmt"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/retention"
)

// Finding is one notable thing, ranked by severity ("warn" before "info").
type Finding struct {
	Severity string `json:"severity"` // warn | info
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Generate returns the digest: the biggest funnel leak, the headline event's
// week-over-week change, and the retention read — computed exactly.
func Generate(evs []event.Event) []Finding {
	var out []Finding
	if len(evs) == 0 {
		return out
	}
	count := map[string]int{}
	for _, e := range evs {
		count[e.Name]++
	}
	names := make([]string, 0, len(count))
	for n := range count {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if count[names[i]] != count[names[j]] {
			return count[names[i]] > count[names[j]]
		}
		return names[i] < names[j]
	})
	has := func(n string) bool { _, ok := count[n]; return ok }

	// 1) biggest funnel leak
	var steps []funnel.Step
	if has("signup") && has("activate") && has("checkout") {
		steps = []funnel.Step{{Event: "signup"}, {Event: "activate"}, {Event: "checkout"}}
	} else {
		top := names
		if len(top) > 3 {
			top = top[:3]
		}
		for _, n := range top {
			steps = append(steps, funnel.Step{Event: n})
		}
	}
	if len(steps) >= 2 {
		fr := funnel.Compute(evs, steps, 7*24*time.Hour)
		worstDrop, worstFrom, worstTo, worstPct := -1, "", "", 0
		for i := 1; i < len(fr.Steps); i++ {
			if fr.Steps[i].DroppedFromPrev > worstDrop {
				worstDrop = fr.Steps[i].DroppedFromPrev
				worstFrom, worstTo = fr.Steps[i-1].Event, fr.Steps[i].Event
				worstPct = int(fr.Steps[i].ConversionFromPrev*100 + 0.5)
			}
		}
		if worstDrop > 0 {
			out = append(out, Finding{
				Severity: "warn",
				Title:    fmt.Sprintf("Biggest drop-off: %s → %s", worstFrom, worstTo),
				Detail: fmt.Sprintf("only %d%% continue; %d users fall off here. Overall %s→%s conversion is %d%%.",
					worstPct, worstDrop, fr.Steps[0].Event, fr.Steps[len(fr.Steps)-1].Event, int(fr.OverallConversion*100+0.5)),
			})
		}
	}

	// 2) headline event, week-over-week
	head := "signup"
	if !has(head) {
		head = names[0]
	}
	now := time.Now().UTC()
	var last7, prev7 int
	for _, e := range evs {
		if e.Name != head {
			continue
		}
		switch age := now.Sub(e.Timestamp); {
		case age < 7*24*time.Hour:
			last7++
		case age < 14*24*time.Hour:
			prev7++
		}
	}
	if prev7 > 0 {
		change := int(float64(last7-prev7) / float64(prev7) * 100)
		sev, dir := "info", "up"
		if change < 0 {
			dir = "down"
			if change <= -15 {
				sev = "warn"
			}
		}
		out = append(out, Finding{
			Severity: sev,
			Title:    fmt.Sprintf("%s is %s %d%% week-over-week", head, dir, absInt(change)),
			Detail:   fmt.Sprintf("%d in the last 7 days vs %d the week before.", last7, prev7),
		})
	}

	// 3) retention read
	retEv := "open"
	if !has(retEv) {
		retEv = names[0]
	}
	rr := retention.Compute(evs, 7, retEv)
	var size, d1, d7 int
	for _, c := range rr.Cohorts {
		size += c.Size
		if len(c.Returned) > 1 {
			d1 += c.Returned[1]
		}
		if len(c.Returned) > 7 {
			d7 += c.Returned[7]
		}
	}
	if size > 0 {
		p1 := int(float64(d1)/float64(size)*100 + 0.5)
		p7 := int(float64(d7)/float64(size)*100 + 0.5)
		sev := "info"
		if p1 < 20 {
			sev = "warn"
		}
		out = append(out, Finding{
			Severity: sev,
			Title:    fmt.Sprintf("Day-1 retention %d%%, day-7 %d%%", p1, p7),
			Detail:   fmt.Sprintf("based on %q activity across %d users.", retEv, size),
		})
	}

	// warnings first
	sort.SliceStable(out, func(i, j int) bool { return out[i].Severity == "warn" && out[j].Severity != "warn" })
	return out
}

// Text renders the digest as a plain-text brief (for the daily webhook/email).
func Text(findings []Finding) string {
	if len(findings) == 0 {
		return "No activity yet."
	}
	s := ""
	for _, f := range findings {
		mark := "•"
		if f.Severity == "warn" {
			mark = "⚠"
		}
		s += fmt.Sprintf("%s %s — %s\n", mark, f.Title, f.Detail)
	}
	return s
}
