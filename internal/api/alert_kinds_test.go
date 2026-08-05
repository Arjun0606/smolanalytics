package api

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/event"
)

func aev(name string, at time.Time) event.Event {
	return event.Event{Name: name, DistinctID: "u", Timestamp: at}
}

// An alert stored before kinds existed has an empty kind, and it MUST keep watching exactly what
// it watched. Silently changing what a live alert means is the one thing an alert may never do.
func TestOldAlertsKeepTheirOriginalMeaning(t *testing.T) {
	now := time.Now().UTC()
	win := 24 * time.Hour
	cut := now.Add(-win)
	var evs []event.Event
	for i := 0; i < 5; i++ {
		evs = append(evs, aev("signup", now.Add(-time.Hour)))
	}
	a := alert.Alert{Event: "signup", Op: "lt", Threshold: 10, WindowHours: 24} // no KindName
	if a.Kind() != alert.KindCount {
		t.Fatalf("an alert with no kind resolved to %q, want the original count shape", a.Kind())
	}
	v, met, _ := evalAlert(a, evs, now, cut, win)
	if v != 5 || !met {
		t.Errorf("count alert = %v met=%v, want 5 and fired (5 < 10)", v, met)
	}
}

// The fixed threshold is why alerts get muted: it asks you to know your own numbers and to keep
// knowing them. A relative alert compares like for like.
func TestRelativeAlertComparesAgainstThePriorWindow(t *testing.T) {
	now := time.Now().UTC()
	win := 24 * time.Hour
	cut := now.Add(-win)
	var evs []event.Event
	for i := 0; i < 10; i++ { // this window: 10
		evs = append(evs, aev("signup", cut.Add(time.Hour)))
	}
	for i := 0; i < 20; i++ { // previous window: 20 — a 50% drop
		evs = append(evs, aev("signup", cut.Add(-2*time.Hour)))
	}
	a := alert.Alert{KindName: alert.KindRelative, Event: "signup", Op: "lt", Threshold: 30, WindowHours: 24}
	_, met, detail := evalAlert(a, evs, now, cut, win)
	if !met {
		t.Errorf("a 50%% drop did not trip a 30%% relative alert: %s", detail)
	}
	if !strings.Contains(detail, "down") || !strings.Contains(detail, "50") {
		t.Errorf("the detail does not say what moved: %q", detail)
	}
	// a smaller drop must not fire
	a.Threshold = 80
	if _, met, _ := evalAlert(a, evs, now, cut, win); met {
		t.Error("a 50% drop tripped an 80% threshold")
	}
	// and no prior window must not page anybody
	fresh := []event.Event{aev("brand_new", cut.Add(time.Hour))}
	a2 := alert.Alert{KindName: alert.KindRelative, Event: "brand_new", Op: "lt", Threshold: 30, WindowHours: 24}
	_, met2, d2 := evalAlert(a2, fresh, now, cut, win)
	if met2 {
		t.Error("an event firing for the first time paged somebody — there is no change from nothing")
	}
	if !strings.Contains(d2, "no prior window") {
		t.Errorf("the reason is unclear: %q", d2)
	}
}

// The anomaly detector has been in internal/insight the whole time with nothing wired to it: the
// dashboard could say "signups dropped 40%" and there was no way to be TOLD without opening it.
func TestAnomalyAlertUsesTheDetectorAndOnlyFiresOnWarnings(t *testing.T) {
	now := time.Now().UTC()
	win := 24 * time.Hour
	cut := now.Add(-win)
	var evs []event.Event
	// a real baseline, then a collapse in the last 24h
	for d := 2; d <= 8; d++ {
		for i := 0; i < 30; i++ {
			evs = append(evs, aev("signup", now.AddDate(0, 0, -d)))
		}
	}
	for i := 0; i < 2; i++ {
		evs = append(evs, aev("signup", now.Add(-2*time.Hour)))
	}
	a := alert.Alert{KindName: alert.KindAnomaly, WindowHours: 24}
	_, met, detail := evalAlert(a, evs, now, cut, win)
	if !met {
		t.Errorf("a collapse from ~30/day to 2 did not trip the anomaly alert: %s", detail)
	}
	if detail == "" {
		t.Error("the alert fired with no explanation of what moved")
	}

	// good news must NOT page anyone — that is how an alert channel gets muted
	var up []event.Event
	for d := 2; d <= 8; d++ {
		for i := 0; i < 10; i++ {
			up = append(up, aev("signup", now.AddDate(0, 0, -d)))
		}
	}
	for i := 0; i < 200; i++ {
		up = append(up, aev("signup", now.Add(-2*time.Hour)))
	}
	if _, met, d := evalAlert(a, up, now, cut, win); met && !strings.Contains(d, "dropped") {
		t.Errorf("signups going UP paged somebody: %s", d)
	}
}

// Most real health is a share, not a count: not "how many errors" but "what fraction of sessions
// hit one".
func TestRatioAlertWatchesTheShare(t *testing.T) {
	now := time.Now().UTC()
	win := 24 * time.Hour
	cut := now.Add(-win)
	var evs []event.Event
	for i := 0; i < 100; i++ {
		evs = append(evs, aev("$pageview", cut.Add(time.Hour)))
	}
	for i := 0; i < 12; i++ {
		evs = append(evs, aev("$exception", cut.Add(time.Hour)))
	}
	a := alert.Alert{KindName: alert.KindRatio, Event: "$exception", Against: "$pageview",
		Op: "gt", Threshold: 10, WindowHours: 24}
	v, met, detail := evalAlert(a, evs, now, cut, win)
	if v != 12 || !met {
		t.Errorf("ratio = %v met=%v, want 12%% and fired (>10%%)", v, met)
	}
	if !strings.Contains(detail, "12") {
		t.Errorf("detail does not carry the share: %q", detail)
	}
	// a zero denominator is not a 0% error rate
	none := []event.Event{aev("$exception", cut.Add(time.Hour))}
	if _, met, d := evalAlert(a, none, now, cut, win); met {
		t.Errorf("an alert fired on a ratio with no denominator: %s", d)
	}
}

// An alert is read months after it is written, usually by someone deciding whether a 3am page was
// worth it. "gt 40" is not a rule anyone can judge.
func TestEveryKindDescribesItselfInWords(t *testing.T) {
	for _, a := range []alert.Alert{
		{Event: "signup", Op: "lt", Threshold: 40, WindowHours: 24},
		{KindName: alert.KindRelative, Event: "signup", Op: "lt", Threshold: 30, WindowHours: 24},
		{KindName: alert.KindAnomaly, WindowHours: 24},
		{KindName: alert.KindRatio, Event: "$exception", Against: "$pageview", Op: "gt", Threshold: 5, WindowHours: 24},
	} {
		d := a.Describe()
		if d == "" || strings.Contains(d, "gt ") || strings.Contains(d, "lt ") {
			t.Errorf("kind %q describes itself as %q — still operator soup", a.Kind(), d)
		}
	}
}
