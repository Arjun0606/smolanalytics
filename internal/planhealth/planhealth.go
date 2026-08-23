// Package planhealth answers one question, in one place: is the instrumentation this project
// declared actually working?
//
// WHY IT IS ITS OWN PACKAGE. The computation lived inline inside the instrumentation_health MCP
// handler, which meant it was reachable by an agent and by nothing else. The dashboard — the screen
// a person actually lands on — had no notion of a tracking plan at all, so the product's central
// claim was invisible in the product. Extracting it is what lets the dashboard render exactly what
// the tool returns.
//
// It is a package rather than a helper on the API type for the reason internal/desk exists: two
// surfaces computing "the same" verdict separately is how they come to disagree, and the disagreement
// is silent. The engine says an event is missing while the screen says it is fine, and nobody finds
// out until a customer does.
//
// NOTHING HERE IS VENDOR-SPECIFIC. It reads the event log, so it works the same whether the events
// arrived from our own SDK or were pulled out of a customer's PostHog by internal/connector. Which
// SDK wrote the call is a question about the REPO and is answered in internal/instrument; this is
// only about whether the events are arriving.

package planhealth

import (
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/trackplan"
)

// Status values. These strings are a WIRE CONTRACT: the npm CLI's `plan check` gates a customer's
// CI build on them, and it once gated on statuses the server has never emitted, so every healthy
// event printed FAIL and the command failed green builds. Changing a value here changes what a
// build does on somebody else's machine — cli/test/plan-status.test.mjs reads these very strings
// out of the source to stop that happening twice.
const (
	// StatusFlowing means the event arrived inside the window with every property the plan expects.
	StatusFlowing = "flowing"
	// StatusMissing means the plan declares it and the log has never seen it in the window.
	StatusMissing = "MISSING — never seen"
)

// EventHealth is one planned event, measured.
type EventHealth struct {
	Event string `json:"event"`
	// Status is StatusFlowing or StatusMissing. An event can be flowing and still unhealthy, when
	// it arrives without properties the plan expects — MissingProperties carries that.
	Status string `json:"status"`
	// Count and LastSeen are zero when the event was never seen; a renderer must not print "0 seen"
	// beside a missing event as though zero were a measurement.
	Count    int       `json:"count,omitempty"`
	LastSeen time.Time `json:"last_seen,omitempty"`
	// MissingProperties are keys the plan declares that never arrived on this event. The event is
	// firing but under-instrumented, which is the failure that hides longest: the number looks
	// right and every breakdown by that property is empty.
	MissingProperties []string `json:"missing_properties,omitempty"`
}

// OK reports whether this event needs nobody's attention.
func (e EventHealth) OK() bool {
	return e.Status == StatusFlowing && len(e.MissingProperties) == 0
}

// Health is the whole verdict.
type Health struct {
	// Declared is false when no tracking plan exists. It is NOT a failure — a project that has
	// never declared a plan has nothing to be unhealthy about — and a renderer must say "no plan
	// declared" rather than "0 of 0 healthy", which reads as broken.
	Declared bool          `json:"declared"`
	Healthy  bool          `json:"healthy"`
	Events   []EventHealth `json:"planned"`
	// Unplanned are events arriving that the plan does not declare, autocapture excluded. Not an
	// error: it is usually a call someone added without updating the plan, and naming it is how the
	// plan and the code converge instead of drifting.
	Unplanned []string `json:"unplanned_events,omitempty"`
	// Window is how far back this looked. Zero means all of history.
	Window time.Duration `json:"-"`
}

// Flowing counts the planned events that need nobody's attention.
func (h Health) Flowing() int {
	n := 0
	for _, e := range h.Events {
		if e.OK() {
			n++
		}
	}
	return n
}

// Broken counts the planned events that do need attention — missing entirely, or arriving without
// properties the plan expects. Flowing + Broken == len(Events), by construction.
func (h Health) Broken() int { return len(h.Events) - h.Flowing() }

// Percent is the share of the declared plan that is working, rounded to the nearest whole number.
//
// It returns 100 for an empty plan on purpose, and callers must check Declared before rendering it:
// "0%" against a project that never declared a plan is a made-up indictment, and the meter that
// shows it is the first thing a new customer sees.
func (h Health) Percent() int {
	if len(h.Events) == 0 {
		return 100
	}
	return int(float64(h.Flowing())/float64(len(h.Events))*100 + 0.5)
}

// Scanner is the slice of the event store this needs: a range scan. Taking the narrow interface
// rather than the store keeps this testable without one, and keeps the dependency pointing the
// right way.
type Scanner interface {
	Scan(from, to time.Time, fn func(event.Event) error) error
}

// Compute measures a plan against the log.
//
// window of 0 means all of history. The caller decides: the MCP tool exposes it as a parameter, the
// dashboard picks a window wide enough that a low-traffic project does not read as broken overnight.
func Compute(sc Scanner, plan trackplan.Plan, window time.Duration) (Health, error) {
	h := Health{Declared: len(plan.Events) > 0, Healthy: true, Window: window}
	if !h.Declared {
		// No plan is not an unhealthy plan. Healthy stays true so a caller that only checks the
		// boolean does not raise an alarm about instrumentation nobody has declared yet.
		return h, nil
	}

	from := time.Time{}
	if window > 0 {
		from = time.Now().UTC().Add(-window)
	}

	type stat struct {
		count    int
		lastSeen time.Time
		props    map[string]bool
	}
	seen := map[string]*stat{}
	if err := sc.Scan(from, time.Time{}, func(e event.Event) error {
		st := seen[e.Name]
		if st == nil {
			st = &stat{props: map[string]bool{}}
			seen[e.Name] = st
		}
		st.count++
		if e.Timestamp.After(st.lastSeen) {
			st.lastSeen = e.Timestamp
		}
		for k := range e.Properties {
			st.props[k] = true
		}
		return nil
	}); err != nil {
		return Health{}, err
	}

	planned := map[string]bool{}
	for _, pe := range plan.Events {
		planned[pe.Name] = true
		row := EventHealth{Event: pe.Name}
		st := seen[pe.Name]
		if st == nil {
			row.Status = StatusMissing
			h.Healthy = false
		} else {
			row.Status = StatusFlowing
			row.Count = st.count
			row.LastSeen = st.lastSeen
			for _, prop := range pe.Properties {
				if !st.props[prop] {
					row.MissingProperties = append(row.MissingProperties, prop)
				}
			}
			if len(row.MissingProperties) > 0 {
				h.Healthy = false
			}
		}
		h.Events = append(h.Events, row)
	}

	for name := range seen {
		// $-prefixed names are autocapture, which nobody declares and everybody receives. Listing
		// them as unplanned would bury the one or two real ones under pageviews.
		if !planned[name] && !strings.HasPrefix(name, "$") {
			h.Unplanned = append(h.Unplanned, name)
		}
	}
	sort.Strings(h.Unplanned)
	return h, nil
}

// Headline is the sentence a person reads, and it is deliberately the same words on the dashboard
// and in the terminal.
//
// It never reports instrumentation loss as user loss. "signed_up stopped arriving" is a statement
// about our measurement; "you lost signups" is a statement about the business, and only one of them
// is supported by the fact that an event went quiet.
func (h Health) Headline() string {
	if !h.Declared {
		return "No tracking plan declared, so there is nothing to verify yet. Declare one and this becomes the list of events we watch for you."
	}
	n := len(h.Events)
	switch {
	case h.Broken() == 0 && n == 1:
		return "The one event in your tracking plan is arriving, with every property it declares."
	case h.Broken() == 0:
		return plural(n, "event") + " in your tracking plan, all arriving with every property they declare."
	case h.Broken() == n:
		return "None of " + plural(n, "event") + " in your tracking plan is arriving as declared. If the product is live, the tracking is broken rather than the product."
	default:
		return plural(h.Broken(), "event") + " of " + itoa(n) + " in your tracking plan " + isAre(h.Broken()) + " not arriving as declared."
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(n) + " " + word + "s"
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
