package api

// Helpers the /v1 API endpoints share, kept when the dashboard was removed.
//
// THE DASHBOARD IS GONE. This instance used to serve a full product-analytics UI — funnels,
// retention, heatmaps, cohorts, a chart deck — from a 5,188-line template. That was the shape of
// the old product. It is now a data layer: it ingests events, answers /v1 reports, serves MCP, and
// reads a customer's existing PostHog. The screen a person looks at is the project page on the
// control plane, which knows their GitHub installation, their org and their billing — none of
// which was ever available here.
//
// These twelve functions were defined in dashboard.go and used by the API handlers around it, so
// they outlived it. Nothing here renders anything.

import (
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/desk"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// baseURL reconstructs this server's externally-visible URL for paste-ready
// snippets (honors a TLS-terminating proxy).
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func deploysFor(s *Server) []deploys.Deploy {
	if s.deploys == nil {
		return nil
	}
	return s.deploys.List()
}

func detectFunnel(evs []event.Event, vol []string) ([]funnel.Step, string) {
	if hasName(vol, "signup") && hasName(vol, "activate") && hasName(vol, "checkout") {
		return []funnel.Step{{Event: "signup"}, {Event: "activate"}, {Event: "checkout"}}, "signup → activate → checkout"
	}
	// ONE JOURNEY DETECTOR. This ranked candidate steps by raw VOLUME while insight.Generate --
	// which produces the "fix this first" verdict at the top of the same page, and the MCP
	// whats_notable tool -- ranked them by COVERAGE. Two detectors, two funnels, two verdicts
	// about one product, from the two surfaces most likely to be compared side by side.
	//
	// Measured: 60 users through pageview -> read_docs -> start_trial -> subscribe, plus 3 power
	// users firing `search` 200x. /v1/notable said "biggest drop-off: after they viewed a page --
	// only 5% go on to search". MCP whats_notable, at the same instant, said "after they read_docs
	// -- only 30% go on to start_trial". The volume detector had been captured by three people.
	//
	// insight.DetectJourney is the intended semantic and says so in its own comment: order the
	// widest-COVERAGE events by when users actually first do them, so the funnel follows the
	// product's real flow rather than whichever event is hammered hardest. This keeps building the
	// human label, and defers the step choice.
	top := journeyNames(evs, vol)
	steps := make([]funnel.Step, len(top))
	labels := make([]string, len(top))
	for i, n := range top {
		steps[i] = funnel.Step{Event: n}
		labels[i] = EventLabel(n)
	}
	return steps, strings.Join(labels, " → ")
}

func detectProp(evs []event.Event, preferred string) string {
	c := map[string]int{}
	for _, e := range evs {
		for k := range e.Properties {
			c[k]++
		}
	}
	if c[preferred] > 0 {
		return preferred
	}
	best, bestN := "", 0
	for k, n := range c {
		if n > bestN || (n == bestN && best != "" && k < best) {
			best, bestN = k, n
		}
	}
	return best
}

func eventsByVolume(evs []event.Event) []string {
	c := map[string]int{}
	for _, e := range evs {
		c[e.Name]++
	}
	ns := make([]string, 0, len(c))
	for n := range c {
		ns = append(ns, n)
	}
	sort.Slice(ns, func(i, j int) bool {
		if c[ns[i]] != c[ns[j]] {
			return c[ns[i]] > c[ns[j]]
		}
		return ns[i] < ns[j]
	})
	return ns
}

// flagsFor and deploysFor are nil-safe accessors: both stores are optional on a self-hosted
// instance, and the investigation degrades to the change findings rather than failing.
func flagsFor(s *Server) []flag.Flag {
	if s.flags == nil {
		return nil
	}
	return s.flags.List()
}

func hasName(names []string, n string) bool {
	for _, x := range names {
		if x == n {
			return true
		}
	}
	return false
}

// notFound renders a clean branded 404 instead of the catch-all dashboard.
func (s *Server) notFound(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8">`+
		`<title>not found · smolanalytics</title>`+
		`<style>html{background:#12100C;color:#FAFAFA;font-family:ui-monospace,Menlo,monospace}`+
		`body{min-height:100vh;margin:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:14px}`+
		`a{color:#FFC900;text-decoration:none}.b{font-weight:800;letter-spacing:-.02em;font-size:18px;font-family:Inter,sans-serif}.b i{color:#FFC900;font-style:normal}</style>`+
		`<div class="b">smol<i>analytics</i></div><div style="color:#8E8E8E">404 · nothing here</div><a href="/">← back to dashboard</a>`)
}

// parseChip decodes one ?f token: "prop:value" (eq) or "prop:op:value"; set/notset
// need no value ("prop:set:"). Caps guard against abuse, not honest use.
func parseChip(raw string) (prop string, op query.Op, val string, ok bool) {
	if len(raw) > 300 {
		return "", "", "", false
	}
	parts := strings.SplitN(raw, ":", 3)
	switch len(parts) {
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return "", "", "", false
		}
		op = query.Eq
		if parts[0] == "referrer" {
			op = query.Contains
		}
		return parts[0], op, parts[1], true
	case 3:
		o := query.Op(parts[1])
		switch o {
		case query.Eq, query.Neq, query.Contains, query.NotContains, query.Regex, query.Gt, query.Lt, query.Set, query.NotSet:
		default:
			return "", "", "", false
		}
		if parts[0] == "" || (parts[2] == "" && o != query.Set && o != query.NotSet) {
			return "", "", "", false
		}
		return parts[0], o, parts[2], true
	}
	return "", "", "", false
}

func pct(f float64) int { return int(math.Round(f * 100)) }

func pickEvent(vol []string, preferred string) string {
	if hasName(vol, preferred) {
		return preferred
	}
	if len(vol) > 0 {
		return vol[0]
	}
	return ""
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// detectFunnel uses the conventional signup→activate→checkout when present, else
// the top events ordered by how soon users do them after first contact.
// journeyNames picks the funnel's step names from the one shared detector, falling back to the
// old volume ordering only when the detector declines to name a journey (too little data), so a
// thin instance still renders something rather than an empty pane.
func journeyNames(evs []event.Event, vol []string) []string {
	if js := insight.DetectJourney(evs); len(js) >= 2 {
		out := make([]string, len(js))
		for i, s := range js {
			out[i] = s.Event
		}
		return out
	}
	top := vol
	if len(top) > 3 {
		top = top[:3]
	}
	return orderByJourney(evs, top)
}

// orderByJourney sorts events by mean delay from each user's first event, so the
// auto-funnel follows the typical sequence rather than raw volume.
func orderByJourney(evs []event.Event, want []string) []string {
	first := map[string]time.Time{}
	for _, e := range evs {
		if t, ok := first[e.DistinctID]; !ok || e.Timestamp.Before(t) {
			first[e.DistinctID] = e.Timestamp
		}
	}
	type acc struct {
		sum time.Duration
		n   int
	}
	delay := map[string]*acc{}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	for _, e := range evs {
		if !wantSet[e.Name] {
			continue
		}
		a := delay[e.Name]
		if a == nil {
			a = &acc{}
			delay[e.Name] = a
		}
		a.sum += e.Timestamp.Sub(first[e.DistinctID])
		a.n++
	}
	mean := func(n string) time.Duration {
		if a := delay[n]; a != nil && a.n > 0 {
			return a.sum / time.Duration(a.n)
		}
		return 0
	}
	// `want` arrives volume-ordered; a stable sort keeps that order on ties (e.g.
	// identical timestamps from a backfill) so the auto-funnel is deterministic.
	out := append([]string{}, want...)
	sort.SliceStable(out, func(i, j int) bool { return mean(out[i]) < mean(out[j]) })
	return out
}

// investigation computes the desk's investigation and overlays the outcome ledger. One function,
// so the dashboard, /v1/brief and the MCP investigate tool cannot drift on whether acted state
// is present — the exact three-surface split this codebase keeps re-learning.
func (s *Server) investigation(evs []event.Event, now time.Time) investigate.Investigation {
	return s.desk(evs, now).Investigation
}

// desk computes the whole desk — the investigation AND the ledger of standing orders and acts.
//
// desk.BuildDesk, not WithContext directly: the tracking plan has to reach the investigator here
// too, or the page says "checkout fell 100%" while GET /v1/investigate says "tracking broke"
// about the same event on the same instance — the two-doors-one-computation failure this file's
// own comments record having fixed twice already. The ledger rides along for the same reason:
// the page's SUBJECT is now the ledger, so composing it here rather than in the template is what
// keeps the route and the front page from growing two answers to "what is armed".
func (s *Server) desk(evs []event.Event, now time.Time) desk.Desk {
	return desk.BuildDesk(evs, s.deskSources(), investigate.Opts{Now: now})
}
