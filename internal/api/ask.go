package api

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/brief"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/retention"
	"github.com/Arjun0606/smolanalytics/internal/trends"
)

// ask answers a plain-English question about the data with zero dependencies —
// it routes common questions (conversion, retention, signups, channels, active
// users, the weekly brief) deterministically, right in the dashboard, no model
// required. For arbitrary questions the user connects smolanalytics to their
// OWN Claude / Cursor over MCP (we never call a model ourselves) — see internal/mcp.
func (s *Server) ask(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var req struct {
		Question string `json:"question"`
	}
	_ = json.Unmarshal(body, &req)
	q := strings.ToLower(strings.TrimSpace(req.Question))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "ask a question")
		return
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	evs = query.Apply(evs, nil) // production scope: dev-env events excluded by default

	writeJSON(w, http.StatusOK, map[string]string{"answer": answer(q, evs, time.Now().UTC())})
}

// askIntent is the deterministic route the ask bar picked for a question — a
// named type (not bare strings) so the router and its tests cannot drift apart.
type askIntent string

const (
	intentAction    askIntent = "action"
	intentBrief     askIntent = "brief"
	intentRetention askIntent = "retention"
	intentChannels  askIntent = "channels"
	intentFunnel    askIntent = "funnel"
	intentActive    askIntent = "active"
	intentSignups   askIntent = "signups"
	intentUnknown   askIntent = "unknown"
)

// classifyAsk routes a lowercased question to one intent. Order is the whole
// game: action-y asks must never fall through to a metric ("alert me if signups
// drop" mentions both), and specific phrasings outrank generic keywords —
// "which channel converts best" is a channel question, not a funnel one, and
// "how many active users" is a user count, not a signup trend.
func classifyAsk(q string) askIntent {
	switch {
	case isAction(q):
		return intentAction
	case hasAny(q, "how are things", "how's it going", "how is it going", "what happened",
		"weekly report", "week in review", "summary", "overview", "digest", "brief"):
		return intentBrief
	case hasAny(q, "retention", "retain", "come back", "comeback", "returning", "stick"):
		return intentRetention
	case hasAny(q, "channel", "source", "acquisition", "referr", "utm", "campaign", "traffic",
		"come from", "coming from", "came from"):
		return intentChannels
	case hasAny(q, "convert", "conversion", "funnel", "drop", "checkout", "activat"):
		return intentFunnel
	case hasAny(q, "active", "dau", "wau", "total users", "how many users", "user count"):
		return intentActive
	case hasAny(q, "signup", "sign up", "sign-up", "new user", "growth", "trend", "how many"):
		return intentSignups
	default:
		return intentUnknown
	}
}

// isAction spots asks that want to CHANGE something. The ask bar is read-only —
// these must never come back as a metric that pretends the change happened.
func isAction(q string) bool {
	if hasAny(q, "alert", "notify", "rename", "configure", "turn on", "turn off",
		"retention to", "set retention", "change retention") {
		return true
	}
	for _, verb := range []string{"set ", "change ", "enable ", "disable ", "delete ",
		"create ", "add ", "save ", "update ", "remove "} {
		if strings.HasPrefix(q, verb) {
			return true
		}
	}
	return false
}

func answer(q string, evs []event.Event, now time.Time) string {
	intent := classifyAsk(q)
	switch intent {
	case intentAction:
		return answerAction(q)
	case intentBrief:
		return answerBrief(evs, briefDays(q), now)
	}

	win, unsupported := parseWindow(q, now)
	if unsupported != "" {
		return fmt.Sprintf("I can't scope to %q from the ask bar — supported windows: today, yesterday, "+
			"this/last week, this/last month, and \"last N days\". Re-ask with one of those, or drop the "+
			"time phrase for all recorded history. For arbitrary ranges, ask your agent over MCP — the "+
			"trends and funnel tools take exact dates.", unsupported)
	}
	volAll := eventsByVolume(evs) // event NAMES come from the full schema, metrics from the window
	scoped := scope(evs, win)

	switch intent {
	case intentRetention:
		return answerRetention(scoped, volAll, win, now)
	case intentChannels:
		return answerChannels(scoped, volAll, win)
	case intentFunnel:
		return answerFunnel(scoped, volAll, win)
	case intentActive:
		return answerActive(scoped, win, now)
	case intentSignups:
		return answerSignups(scoped, volAll, win)
	default:
		return "I can answer about your conversion funnel, channels (with per-source conversion), retention, " +
			"signups/growth, active users, and \"what happened this week\" right here — scoped to today, " +
			"yesterday, this/last week, this/last month, or last N days. For anything else, connect " +
			"smolanalytics to your own Claude or Cursor over MCP and just ask — your model reads the same " +
			"data through our tools."
	}
}

// answerAction is honest guidance for change requests: the ask bar computes
// answers, it does not flip switches. Every branch names where the change
// actually lives — the Settings section and the MCP tool an agent can call.
func answerAction(q string) string {
	switch {
	case hasAny(q, "alert", "notify", "watch"):
		return "I can't create alerts from here — the ask bar only reads data. Set one up in Settings → Alerts " +
			"(wire a Slack/HTTPS destination under Settings → Webhooks first). In Cursor/Claude Code your " +
			"agent can do it directly: create_alert (\"tell me if signups drop below 10/day\")."
	case hasAny(q, "retention", "retain"):
		return "I can't change settings from here — data retention lives in Settings → Retention. " +
			"In Cursor/Claude Code your agent can: set_retention."
	case strings.Contains(q, "rename"):
		return "I can't rename events from here — event names come from your instrumentation. Rename it where " +
			"you call track(), then declare the new name in your tracking plan (set_tracking_plan in " +
			"Cursor/Claude Code) so drift gets flagged under Settings → Events tracked."
	default:
		return "The ask bar computes answers from your data — it can't change anything. Settings has the knobs " +
			"(retention, alerts, webhooks, API keys), and in Cursor/Claude Code your agent has the action " +
			"tools: create_alert, set_retention, create_goal, save_report."
	}
}

// briefDays maps the ask's phrasing to the brief's pulse width: "today" → 1,
// month phrasing → 30, else the weekly 7. brief.Build derives the
// current-vs-prior comparison windows from this.
func briefDays(q string) int {
	switch {
	case strings.Contains(q, "today"):
		return 1
	case strings.Contains(q, "month"):
		return 30
	default:
		return 7
	}
}

// answerBrief renders the SAME computation as `smolanalytics brief` (pulse +
// deltas + the verdict engine's findings) tight enough for the ask panel — one
// Brief struct feeds both, so the ask bar and the morning digest can never disagree.
func answerBrief(evs []event.Event, days int, now time.Time) string {
	b := brief.Build(evs, days, now)
	var s strings.Builder
	fmt.Fprintf(&s, "Last %d days: %d visitors · %d events", b.Days, b.Visitors, b.Events)
	if b.PriorEvents == 0 {
		s.WriteString(" (no prior window to compare).")
	} else {
		visitors := "new"
		if b.PriorVisitors > 0 {
			visitors = fmt.Sprintf("%+d%%", pctChange(b.Visitors, b.PriorVisitors))
		}
		fmt.Fprintf(&s, " (vs prior %d days: visitors %s, events %+d%%).", b.Days,
			visitors, pctChange(b.Events, b.PriorEvents))
	}
	if len(b.Findings) == 0 {
		s.WriteString(" Nothing notable — no big swings, funnel leaks, or retention flags.")
		return s.String()
	}
	s.WriteString("\nWhat to look at:")
	for _, f := range b.Findings {
		mark := "•"
		if f.Severity == "warn" {
			mark = "⚠"
		}
		fmt.Fprintf(&s, "\n%s %s — %s", mark, f.Title, f.Detail)
	}
	return s.String()
}

// pctChange mirrors brief's signed delta — direction must be unmissable.
func pctChange(cur, prior int) int {
	return int(math.Round(float64(cur-prior) / float64(prior) * 100))
}

// askWindow is an honest time scope parsed from the question. Zero from/to =
// all recorded history (the ask bar's long-standing default). The label is
// woven into every scoped answer so "this week" can never silently mean
// something else.
type askWindow struct {
	from, to time.Time
	label    string
}

func (w askWindow) scoped() bool { return !w.from.IsZero() || !w.to.IsZero() }

// windowClause weaves the computed window into a sentence: " this week (since
// Mon Jun 22, UTC)" — or "" for all-time.
func windowClause(w askWindow) string {
	if !w.scoped() {
		return ""
	}
	return " " + w.label
}

var lastNDaysRe = regexp.MustCompile(`(?:last|past)\s+(\d+)\s+days?`)

// parseWindow maps time phrases to real windows (UTC, like every computation
// here). Recognized: today, yesterday, this/last week (calendar, Monday start),
// this/last month (calendar), and "last/past N days" (rolling, consistent with
// the engine). Anything else time-shaped (quarters, years, named months) comes
// back as unsupported so the caller can SAY so instead of silently answering
// over a different range.
func parseWindow(q string, now time.Time) (win askWindow, unsupported string) {
	const day = 24 * time.Hour
	today := now.Truncate(day)
	switch {
	case strings.Contains(q, "yesterday"):
		return askWindow{from: today.Add(-day), to: today, label: "yesterday (UTC)"}, ""
	case strings.Contains(q, "today"):
		return askWindow{from: today, to: now, label: "today (UTC)"}, ""
	case strings.Contains(q, "this week"):
		monday := today.AddDate(0, 0, -mondayOffset(today))
		return askWindow{from: monday, to: now,
			label: "this week (since Mon " + monday.Format("Jan 2") + ", UTC)"}, ""
	case strings.Contains(q, "last week"):
		monday := today.AddDate(0, 0, -mondayOffset(today))
		from := monday.AddDate(0, 0, -7)
		return askWindow{from: from, to: monday,
			label: "last week (Mon " + from.Format("Jan 2") + " – Sun " + monday.AddDate(0, 0, -1).Format("Jan 2") + ", UTC)"}, ""
	case strings.Contains(q, "this month"):
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return askWindow{from: first, to: now,
			label: "this month (since " + first.Format("Jan 2") + ", UTC)"}, ""
	case strings.Contains(q, "last month"):
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return askWindow{from: first.AddDate(0, -1, 0), to: first,
			label: "last month (" + first.AddDate(0, -1, 0).Format("January 2006") + ", UTC)"}, ""
	}
	if m := lastNDaysRe.FindStringSubmatch(q); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return askWindow{from: now.AddDate(0, 0, -n), to: now,
				label: fmt.Sprintf("the last %d days (UTC)", n)}, ""
		}
	}
	if tok := unsupportedTimePhrase(q); tok != "" {
		return askWindow{}, tok
	}
	return askWindow{}, ""
}

// mondayOffset is days since the most recent Monday (0 on a Monday) — Go's
// time.Weekday puts Sunday at 0, one off from a Monday-start week.
func mondayOffset(t time.Time) int { return (int(t.Weekday()) + 6) % 7 }

// unsupportedTimePhrase returns the first time-shaped token the parser does NOT
// support, so the answer can name it instead of quietly widening the window.
func unsupportedTimePhrase(q string) string {
	for _, tok := range []string{"quarter", "year", "month", "week", "hour", "minute",
		"q1", "q2", "q3", "q4", "january", "february", "march", "april", "june", "july",
		"august", "september", "october", "november", "december"} {
		if containsWord(q, tok) {
			return tok
		}
	}
	return ""
}

// containsWord reports whether tok appears as a whole word — "month" must not
// fire on "monthly", or every trend question would bounce as an unsupported window.
func containsWord(q, tok string) bool {
	isSep := func(r rune) bool { return !('a' <= r && r <= 'z' || '0' <= r && r <= '9') }
	for _, w := range strings.FieldsFunc(q, isSep) {
		if w == tok {
			return true
		}
	}
	return false
}

// scope keeps events inside [from, to) — the end is exclusive so "yesterday"
// and "last week" never double-count the boundary instant.
func scope(evs []event.Event, w askWindow) []event.Event {
	if !w.scoped() {
		return evs
	}
	out := make([]event.Event, 0, len(evs))
	for _, e := range evs {
		if !w.from.IsZero() && e.Timestamp.Before(w.from) {
			continue
		}
		if !w.to.IsZero() && !e.Timestamp.Before(w.to) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func answerFunnel(evs []event.Event, vol []string, win askWindow) string {
	fsteps, ftitle := detectFunnel(evs, vol)
	fr := funnel.Compute(evs, fsteps, 7*24*time.Hour)
	if len(fr.Steps) == 0 || fr.Steps[0].Count == 0 {
		if win.scoped() {
			return "No events " + win.label + " to build a funnel from — widen the window, or drop the time phrase for all history."
		}
		return "No events yet to build a funnel from."
	}
	worst, worstDrop := "", -1
	for _, st := range fr.Steps[1:] {
		if st.DroppedFromPrev > worstDrop {
			worstDrop, worst = st.DroppedFromPrev, st.Event
		}
	}
	return fmt.Sprintf("%d of %d users (%d%%) complete %s%s. The biggest drop-off is at \"%s\" — %d users fall off there.",
		fr.Steps[len(fr.Steps)-1].Count, fr.Steps[0].Count, pct(fr.OverallConversion), ftitle, windowClause(win), worst, worstDrop)
}

func answerRetention(evs []event.Event, vol []string, win askWindow, now time.Time) string {
	rr := retention.Compute(evs, 7, pickEvent(vol, "open"))
	// honest denominators: only cohorts old enough to observe day N (retention.DayN)
	d1, size1 := retention.DayN(rr, 1, now)
	if size1 == 0 {
		if win.scoped() {
			return "Not enough history " + win.label + " to measure retention — day-1 retention needs users past " +
				"their first day. Widen the window, or drop the time phrase for all history."
		}
		return "Not enough history yet to measure retention — check back once users are past their first day."
	}
	out := fmt.Sprintf("Day-1 retention is %d%% (of %d users past day 1).",
		int(float64(d1)/float64(size1)*100+0.5), size1)
	if d7, size7 := retention.DayN(rr, 7, now); size7 > 0 {
		out = fmt.Sprintf("Day-1 retention is %d%% and day-7 is %d%% (of %d and %d users old enough to measure).",
			int(float64(d1)/float64(size1)*100+0.5), int(float64(d7)/float64(size7)*100+0.5), size1, size7)
	}
	if win.scoped() {
		out += " Cohorts scoped to first activity " + win.label + "."
	}
	return out
}

// answerChannels answers "which channel converts best" the way goal_report does:
// first-touch attribution per user, then per-source conversion to the site's
// conversion event — NOT the funnel, which ignores sources entirely.
func answerChannels(evs []event.Event, vol []string, win askWindow) string {
	if len(evs) == 0 {
		if win.scoped() {
			return "No events " + win.label + " to attribute — widen the window, or drop the time phrase for all history."
		}
		return "No events recorded yet."
	}
	srcProp := detectProp(evs, "source")
	if srcProp == "" {
		return "Your events don't carry any properties to attribute a channel from yet — send a " +
			"source/referrer/utm_source property with your events and ask again."
	}
	conv := pickConversion(evs, vol)

	// first-touch: a user's channel is srcProp on their FIRST event in the window
	firstTS := map[string]time.Time{}
	firstSrc := map[string]string{}
	converted := map[string]bool{}
	for _, e := range evs {
		if t, ok := firstTS[e.DistinctID]; !ok || e.Timestamp.Before(t) {
			firstTS[e.DistinctID] = e.Timestamp
			src, _ := e.Properties[srcProp].(string)
			if src == "" {
				src = "direct"
			}
			firstSrc[e.DistinctID] = src
		}
		if e.Name == conv {
			converted[e.DistinctID] = true
		}
	}
	type row struct {
		src              string
		users, converted int
	}
	bySrc := map[string]*row{}
	for id, src := range firstSrc {
		r := bySrc[src]
		if r == nil {
			r = &row{src: src}
			bySrc[src] = r
		}
		r.users++
		if converted[id] {
			r.converted++
		}
	}
	rows := make([]*row, 0, len(bySrc))
	for _, r := range bySrc {
		rows = append(rows, r)
	}
	// listing is volume-ordered (name breaks ties → deterministic run to run)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].users != rows[j].users {
			return rows[i].users > rows[j].users
		}
		return rows[i].src < rows[j].src
	})
	parts := []string{}
	for i, r := range rows {
		if i >= 3 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d users → %d %s (%d%%)",
			r.src, r.users, r.converted, conv, int(float64(r.converted)/float64(r.users)*100+0.5)))
	}
	listing := fmt.Sprintf("By %s, first-touch%s: %s.", srcProp, windowClause(win), strings.Join(parts, ", "))
	if len(converted) == 0 {
		return listing + fmt.Sprintf(" No %q conversions in this data yet, so there is no honest \"best\" to rank.", conv)
	}
	// "best" = highest conversion rate; users break ties so a bigger sample wins
	best := rows[0]
	rate := func(r *row) float64 { return float64(r.converted) / float64(r.users) }
	for _, r := range rows[1:] {
		if rate(r) > rate(best) || (rate(r) == rate(best) && r.users > best.users) {
			best = r
		}
	}
	return fmt.Sprintf("%s converts best to \"%s\": %d of %d first-touch users (%d%%). %s",
		best.src, conv, best.converted, best.users, int(rate(best)*100+0.5), listing)
}

// pickConversion picks the event "converts" means for this dataset: the
// conventional conversion names when present, else the deepest step of the
// auto-detected journey funnel. The answer always names it, so the user sees
// exactly what was computed.
func pickConversion(evs []event.Event, vol []string) string {
	for _, want := range []string{"signup", "purchase", "checkout", "subscribe", "convert", "activate"} {
		if hasName(vol, want) {
			return want
		}
	}
	if steps, _ := detectFunnel(evs, vol); len(steps) > 0 {
		return steps[len(steps)-1].Event
	}
	return ""
}

func answerSignups(evs []event.Event, vol []string, win askWindow) string {
	ev := pickEvent(vol, "signup")
	if ev == "" {
		return "No events recorded yet."
	}
	tr := trends.Compute(evs, ev, win.from, win.to, false)
	days := len(tr.Points)
	if days == 0 {
		return "No events recorded yet."
	}
	if tr.Total == 0 && win.scoped() {
		return fmt.Sprintf("No \"%s\" events %s — widen the window, or drop the time phrase for all history.", ev, win.label)
	}
	if win.scoped() {
		return fmt.Sprintf("%d \"%s\" events %s — about %d/day.", tr.Total, ev, win.label, tr.Total/days)
	}
	return fmt.Sprintf("%d \"%s\" events over the last %d days — about %d/day.", tr.Total, ev, days, tr.Total/days)
}

func answerActive(evs []event.Event, win askWindow, now time.Time) string {
	if win.scoped() {
		users := distinctUsers(evs)
		if users == 0 {
			return "No active users " + win.label + " — widen the window, or drop the time phrase for all history."
		}
		return fmt.Sprintf("%d active users %s, across %d events.", users, win.label, len(evs))
	}
	total := distinctUsers(evs)
	cutoff := now.AddDate(0, 0, -7)
	recent := map[string]bool{}
	for _, e := range evs {
		if !e.Timestamp.Before(cutoff) { // inclusive "last 7 days", consistent with the engine
			recent[e.DistinctID] = true
		}
	}
	return fmt.Sprintf("%d total users, %d active in the last 7 days.", total, len(recent))
}

func hasAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
