package api

import (
	_ "embed"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/funnel"
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

type dashVM struct {
	TotalUsers    int
	Signups       int
	OverallConv   int
	Funnel        []funnelRow
	Retention     []retRow
	RetDayHeaders []string
	Trend         []trendBar
	Events        []string
	Updated       string
}

func (s *Server) dashboard(w http.ResponseWriter, _ *http.Request) {
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

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = dashTmpl.Execute(w, vm)
}

func pct(f float64) int { return int(math.Round(f * 100)) }
