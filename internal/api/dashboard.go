package api

import (
	_ "embed"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

//go:embed dashboard.tmpl.html
var dashboardHTML string

var dashTmpl = template.Must(template.New("dash").Parse(dashboardHTML))

type funnelRow struct {
	Event   string
	Count   int
	PctTop  int // conversion from the top step
	PctPrev int // conversion from the previous step
	Dropped int
	First   bool
}

type retCell struct {
	Label string
	Style template.CSS // background intensity for the heatmap
	Empty bool
}

type retRow struct {
	Date  string
	Size  int
	Cells []retCell
}

type trendBar struct {
	Date      string
	Count     int
	HeightPct int
}

type segRow struct {
	Value  string
	Count  int
	Pct    int
	BarPct int // width relative to the top group
}

// segConv is one segment's funnel conversion — the "pro converts 2x free" insight.
type segConv struct {
	Value string
	Users int
	Conv  int // overall funnel conversion %, this segment
}

// funnelBySegment runs the signup→activate→checkout funnel separately for each
// value of a property — segmentation applied to a report, the core Mixpanel move.
func funnelBySegment(evs []event.Event, property string) []segConv {
	vals := map[string]bool{}
	for _, e := range evs {
		if e.Name == "signup" {
			if v, ok := e.Properties[property]; ok {
				vals[toStr(v)] = true
			}
		}
	}
	steps := []funnel.Step{{Event: "signup"}, {Event: "activate"}, {Event: "checkout"}}
	out := make([]segConv, 0, len(vals))
	for v := range vals {
		seg := query.Apply(evs, []query.Filter{{Property: property, Op: query.Eq, Value: v}})
		fr := funnel.Compute(seg, steps, 7*24*time.Hour)
		out = append(out, segConv{Value: v, Users: fr.Steps[0].Count, Conv: pct(fr.OverallConversion)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Users > out[j].Users })
	return out
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

type dashVM struct {
	TotalUsers    int
	Signups       int
	OverallConv   int
	Funnel        []funnelRow
	Retention     []retRow
	RetDayHeaders []string
	Trend         []trendBar
	BySource      []segRow
	ConvByPlan    []segConv
	Events        []string
	Updated       string
	HasData       bool   // false on a fresh install → show the big onboarding
	Base          string // this server's base URL, for ready-to-paste snippets
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	fr := funnel.Compute(evs, []funnel.Step{{Event: "signup"}, {Event: "activate"}, {Event: "checkout"}}, 7*24*time.Hour)
	rr := retentionOf(evs, 7, "open")
	tr := trendOf(evs, "signup")
	names, _ := s.store.Names()

	vm := dashVM{
		TotalUsers:  distinctUsers(evs),
		Signups:     tr.Total,
		OverallConv: pct(fr.OverallConversion),
		Events:      names,
		Updated:     time.Now().UTC().Format("Jan 2, 15:04 MST"),
		HasData:     len(evs) > 0,
		Base:        baseURL(r),
	}

	for i, st := range fr.Steps {
		vm.Funnel = append(vm.Funnel, funnelRow{
			Event:   st.Event,
			Count:   st.Count,
			PctTop:  pct(st.ConversionFromTop),
			PctPrev: pct(st.ConversionFromPrev),
			Dropped: st.DroppedFromPrev,
			First:   i == 0,
		})
	}

	for d := 0; d <= rr.MaxDays; d++ {
		vm.RetDayHeaders = append(vm.RetDayHeaders, fmt.Sprintf("D%d", d))
	}
	// most-recent cohorts first, capped for a clean grid
	start := 0
	if len(rr.Cohorts) > 12 {
		start = len(rr.Cohorts) - 12
	}
	for i := len(rr.Cohorts) - 1; i >= start; i-- {
		c := rr.Cohorts[i]
		row := retRow{Date: c.Date.Format("Jan 2"), Size: c.Size}
		for d := 0; d <= rr.MaxDays; d++ {
			if c.Size == 0 || d >= len(c.Returned) {
				row.Cells = append(row.Cells, retCell{Empty: true})
				continue
			}
			frac := float64(c.Returned[d]) / float64(c.Size)
			row.Cells = append(row.Cells, retCell{
				Label: fmt.Sprintf("%d%%", int(math.Round(frac*100))),
				Style: template.CSS(fmt.Sprintf("background:rgba(245,166,35,%.2f)", 0.08+0.92*frac)),
			})
		}
		vm.Retention = append(vm.Retention, row)
	}

	maxT := 1
	for _, p := range tr.Points {
		if p.Count > maxT {
			maxT = p.Count
		}
	}
	for _, p := range tr.Points {
		vm.Trend = append(vm.Trend, trendBar{
			Date:      p.Date.Format("1/2"),
			Count:     p.Count,
			HeightPct: int(math.Round(float64(p.Count) / float64(maxT) * 100)),
		})
	}

	// Segmentation: signups broken down by acquisition source.
	var signups []event.Event
	for _, e := range evs {
		if e.Name == "signup" {
			signups = append(signups, e)
		}
	}
	groups := query.Breakdown(signups, "source")
	top := 0
	if len(groups) > 0 {
		top = groups[0].Count
	}
	for _, g := range groups {
		row := segRow{Value: g.Value, Count: g.Count}
		if vm.Signups > 0 {
			row.Pct = int(math.Round(float64(g.Count) / float64(vm.Signups) * 100))
		}
		if top > 0 {
			row.BarPct = int(math.Round(float64(g.Count) / float64(top) * 100))
		}
		vm.BySource = append(vm.BySource, row)
	}

	vm.ConvByPlan = funnelBySegment(evs, "plan")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = dashTmpl.Execute(w, vm)
}

func pct(f float64) int { return int(math.Round(f * 100)) }

// baseURL reconstructs this server's externally-visible URL for paste-ready
// snippets (honors a TLS-terminating proxy).
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
