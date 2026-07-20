package deploys

// Report is the ONE impact builder both the HTTP endpoint (/v1/deploys?event=) and the MCP
// tool (deploy_impact) call, so the number in the dashboard and the number in the editor are
// literally the same bytes — the agreement test asserts it. It builds the daily series with
// trends.Compute (the dashboard's engine) and runs the deterministic before/after per deploy.

import (
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/trends"
)

func Report(evs []event.Event, deps []Deploy, eventName string, days, window int) map[string]any {
	if days <= 0 {
		days = 30
	}
	if window <= 0 {
		window = 3
	}
	now := time.Now().UTC()
	from := now.Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
	res := trends.Compute(evs, eventName, from, now, false)
	series := make([]Point, len(res.Points))
	for i, p := range res.Points {
		series[i] = Point{Date: p.Date, Count: p.Count}
	}
	impacts := ComputeImpact(series, deps, window, 0.25)
	return map[string]any{
		"event":    eventName,
		"days":     days,
		"window":   window,
		"deploys":  impacts,
		"headline": Headline(impacts),
	}
}
