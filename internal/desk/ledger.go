package desk

// THE LEDGER — what is standing over this product right now, and what it has already done.
//
// The desk used to be a queue of findings: things the system NOTICED. That is the half of the
// product PostHog and Amplitude give away, and the half they do better. The half nobody else has
// is the one that ACTS — a guardrail that pulls a breaching flag with no human in the loop and
// leaves a receipt on the operator's own timeline, and a tracking break the cloud can restore by
// pull request. Both of those rendered as a footnote UNDER the list of things we merely noticed.
//
// So the subject changes. The page is now a ledger of ACTIONS, and this file composes it — here,
// in the seam, so /v1/investigate, the MCP tool, the share page and the dashboard read one
// ledger and cannot drift. Composing it in the template is how the desk grew three finding
// systems in three styles the first time.
//
// THE HARD CONSTRAINT is that on most days there are no actions. Autonomous acting is off by
// default, guardrail reverts are rare by design, and a healthy product breaks nothing. An empty
// ledger is therefore the NORMAL screen, and it must be the best one in the product rather than
// an apology. The answer is Standing: every condition currently being checked on this instance,
// with the trigger arithmetic in the instance's own numbers and the action in the future tense.
// Not one row of it is invented — every input below is already computed and already persisted.
//
// The rule the whole file obeys: never describe an act that did not happen, and never promise an
// action this binary cannot itself perform. A watch says what WE do. The pull-request half runs
// in the cloud behind switches this package cannot see, so it is never promised on a row.

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
)

// RevertEnv is the kill switch for ACTING on a guardrail breach. Detection and action are
// separate levers on purpose: "stop turning my flags off" and "stop telling me a guardrail
// failed" are different requests, and a safety feature with one switch gets switched off whole.
//
// It lives here, beside TrackingBrokeEnv, because this package's own doc comment says the
// environment is read at the seam — internal/api used to read it, which meant the only place
// that knew whether the product was armed was the package that could not tell the page.
const RevertEnv = "SMOLANALYTICS_AUTO_REVERT"

// RevertEvent is the receipt written onto the operator's OWN event log when a flag is pulled.
// Moved here from internal/api for the same reason: the ledger has to find these receipts, and
// the ledger is composed here.
const RevertEvent = "$experiment_reverted"

const (
	// RevertWarmup is how long an experiment must have run before a breach may pull it, and
	// RevertMinGap is the minimum distance between the two failing checks that confirm a breach.
	//
	// Exported and quoted by GuardrailTrigger so the sentence the page prints is built from the
	// same constants confirmedBreach enforces. The tracking detector already learned this lesson
	// the expensive way: a promise restated from memory is a promise that drifts the first time
	// somebody edits the number.
	RevertWarmup = 15 * time.Minute
	RevertMinGap = 5 * time.Minute
	// SafetyPassEvery is how often the unprompted pass runs — alerts, then guardrails. The slab
	// says "rechecked every 5 minutes" and reads it from here, so the claim cannot outlive the
	// ticker in cmd/smolanalytics.
	SafetyPassEvery = 5 * time.Minute
)

// maxActs caps the acted section. A desk drowning in old receipts stops reading as a desk, and
// ActedCount is len(Acted) precisely so the figure on the slab and the rows beneath it can never
// disagree — a count that exceeds its own list is the first thing a sceptic catches.
const maxActs = 10

// RevertEnabled reports whether auto-revert may act. Default on; only the exact word "off"
// disables it, so a typo fails loudly toward the documented behaviour.
func RevertEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv(RevertEnv)), "off")
}

// Act is one thing the system did without being asked, backed by a receipt on the user's own
// timeline. There are exactly two kinds, and both are built from a persisted event — never from
// configuration, an intention, or a capability. If it did not happen, there is no Act.
type Act struct {
	Kind string    `json:"kind"` // guardrail_revert | tracking_pr
	Chip string    `json:"chip"`
	Head string    `json:"head"`
	Sub  string    `json:"sub"`
	At   time.Time `json:"at"`
	When string    `json:"when"`
	// Evidence is where the reader lands to check the claim. Exactly one per row, always.
	EvText string `json:"evidence_text"`
	EvHref string `json:"evidence"`
	// EvExternal marks an off-instance destination (the pull request itself), which the page
	// renders target=_blank rel=noopener.
	EvExternal bool `json:"evidence_external,omitempty"`
}

// Watch is one condition being checked on this instance: what trips it, and what happens then.
//
// Armed means the condition is live. Not-armed is NOT disabled chrome — it is the product saying
// what it would take, with one concrete next step, which is the half of an empty state that is
// worth anything.
type Watch struct {
	Kind        string `json:"kind"` // step_change | guardrail | tracking | alert | floor
	Armed       bool   `json:"armed"`
	Subject     string `json:"subject"`
	CheckedText string `json:"checked"`
	Trigger     string `json:"trigger"`
	Action      string `json:"action,omitempty"`
	Blocked     string `json:"blocked,omitempty"`
	EvText      string `json:"evidence_text"`
	EvHref      string `json:"evidence"`
}

// Sub is the row's one full-width sentence. Armed rows read "{trigger} → {action}"; not-armed
// rows read the refusal followed by the single step that would clear it.
func (w Watch) Sub() string {
	if w.Armed {
		if w.Action == "" {
			return w.Trigger
		}
		return w.Trigger + " → " + w.Action
	}
	if w.Blocked == "" {
		return w.Trigger
	}
	return w.Trigger + " " + w.Blocked
}

// Ledger is the page's subject.
type Ledger struct {
	ArmedCount int `json:"armed_count"`
	ActedCount int `json:"acted_count"`
	// RevertMode and TrackingMode are "armed" or "off", read from the two environment switches.
	// The page prints both verbatim: an operator who switched something off must be able to see
	// that from the screen, and a reader must never be told a flag will be pulled on an instance
	// where it will not.
	RevertMode   string `json:"revert_mode"`
	TrackingMode string `json:"tracking_mode"`
	// CheckedAt is the sweep's clock reading. Deliberately NOT serialized: it is the same value
	// as investigation.generated_at, and shipping the one clock reading twice is how the
	// two-doors agreement test would start failing on a minute boundary for no real reason.
	CheckedAt time.Time `json:"-"`

	Acted    []Act                 `json:"acted,omitempty"`
	Closed   []investigate.Finding `json:"closed,omitempty"`
	Open     []investigate.Finding `json:"open,omitempty"`
	Standing []Watch               `json:"standing,omitempty"`
}

// Armed and Cold split Standing for rendering. Kept as methods rather than two stored slices so
// the JSON carries one list and the two halves can never disagree about which row is in which.
func (l Ledger) Armed() []Watch { return filterWatches(l.Standing, true) }
func (l Ledger) Cold() []Watch  { return filterWatches(l.Standing, false) }

func filterWatches(ws []Watch, armed bool) []Watch {
	var out []Watch
	for _, w := range ws {
		if w.Armed == armed {
			out = append(out, w)
		}
	}
	return out
}

// Claim is the slab's one sentence. Three states, and the unit never changes between them.
func (l Ledger) Claim() string {
	switch {
	case l.ActedCount > 0:
		return fmt.Sprintf("%s done without you. %s still armed.",
			count(l.ActedCount, "thing", "things"), count(l.ArmedCount, "condition", "conditions"))
	case l.ArmedCount > 0:
		return fmt.Sprintf("Nothing has tripped yet. %s armed, rechecked every %s.",
			isAre(l.ArmedCount, "condition", "conditions"), humanDur(SafetyPassEvery))
	default:
		return "Nothing is standing over your product yet."
	}
}

// Support prints the two real modes, read from the environment rather than from a hope.
func (l Ledger) Support() string {
	var parts []string
	if l.RevertMode == "armed" {
		parts = append(parts, "auto-revert armed")
	} else {
		parts = append(parts, "auto-revert off, "+RevertEnv+"=off. breaches are recorded, flags are left alone.")
	}
	if l.TrackingMode == "armed" {
		parts = append(parts, "tracking-break detection armed")
	} else {
		parts = append(parts, "tracking-break detection off, "+TrackingBrokeEnv+
			"=off. a silent event is reported as a plain regression, never as broken instrumentation.")
	}
	return strings.Join(parts, " · ")
}

// Subject is the slab's permanent unit. Acts are the body, never the figure, so the readout
// cannot swap units under the reader between one visit and the next.
func (l Ledger) Subject() string {
	s := "standing orders"
	if l.ActedCount > 0 {
		s += fmt.Sprintf(", %d acted", l.ActedCount)
	}
	return s
}

// HasTracking reports whether any tracking watch is armed, which is the only condition under
// which the page may say anything at all about where the restore half runs.
func (l Ledger) HasTracking() bool {
	for _, w := range l.Standing {
		if w.Armed && w.Kind == "tracking" {
			return true
		}
	}
	return false
}

// buildLedger composes the whole thing from one investigation and the instance's own state.
func buildLedger(inv investigate.Investigation, evs []event.Event, src Sources, o investigate.Opts) Ledger {
	now := o.Now
	if now.IsZero() {
		now = inv.GeneratedAt
	}
	l := Ledger{
		RevertMode:   armedOff(RevertEnabled()),
		TrackingMode: armedOff(DetectionEnabled()),
		CheckedAt:    inv.GeneratedAt,
		Acted:        actsFrom(evs, now, o.Days),
		Standing:     watchesFrom(inv, src, now),
	}
	l.ActedCount = len(l.Acted)
	for _, w := range l.Standing {
		if w.Armed {
			l.ArmedCount++
		}
	}
	for _, f := range inv.Findings {
		if f.ActedAt.IsZero() {
			l.Open = append(l.Open, f)
		} else {
			l.Closed = append(l.Closed, f)
		}
	}
	// NeedsYou first, everything else in the cost order the investigator already ranked by.
	sort.SliceStable(l.Open, func(i, j int) bool { return l.Open[i].NeedsYou && !l.Open[j].NeedsYou })
	return l
}

// ---- acts -------------------------------------------------------------------------------

// actsFrom reads the receipts. Two event names, both written by something that already acted:
// RevertEvent by (*api.Server).autoRevert, investigate.TrackingPrEvent by the cloud's
// tracking-fix route. Nothing here is inferred from configuration — a capability that has never
// run produces no row, which is the whole "never render an action that did not happen" rule
// expressed as code rather than as a comment.
func actsFrom(evs []event.Event, now time.Time, days int) []Act {
	if days <= 0 {
		days = 30
	}
	cutoff := now.AddDate(0, 0, -days)
	var out []Act
	for _, e := range evs {
		if e.Timestamp.Before(cutoff) {
			continue
		}
		var a Act
		var ok bool
		switch e.Name {
		case RevertEvent:
			a, ok = revertAct(e)
		case investigate.TrackingPrEvent:
			a, ok = trackingAct(e)
		}
		if ok {
			out = append(out, a)
		}
	}
	// Sorted here rather than trusting the caller's slice order: the events reach this package
	// straight off the store, and "newest last" is an assumption, not a contract.
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > maxActs {
		out = out[:maxActs]
	}
	return out
}

func revertAct(e event.Event) (Act, bool) {
	flagKey := propStr(e, "flag")
	if flagKey == "" {
		return Act{}, false // a receipt with no subject names nothing; better silent than vague
	}
	head := flagKey + " turned off"
	if g := propStr(e, "guardrail"); g != "" {
		head += " · guardrail " + g
	}
	sub := "nobody was asked."
	if r := strings.TrimRight(strings.TrimSpace(propStr(e, "reason")), "."); r != "" {
		sub = r + ". " + sub
	}
	at := e.Timestamp.UTC()
	return Act{
		Kind: "guardrail_revert", Chip: "acted", Head: head, Sub: sub,
		At: at, When: at.Format("Jan 2 15:04"),
		EvText: "open the experiment", EvHref: "#pane-experiments",
	}, true
}

func trackingAct(e event.Event) (Act, bool) {
	name, url := propStr(e, "event"), propStr(e, "pr_url")
	// No URL, no row. The evidence affordance is a property of EVERY row, and a "pull request
	// opened" line the reader cannot open is exactly the unfalsifiable claim this design forbids.
	if name == "" || !isHTTP(url) {
		return Act{}, false
	}
	head := name + " · pull request opened"
	if repo := propStr(e, "repo"); repo != "" {
		head += " on " + repo
	}
	var b strings.Builder
	fmt.Fprintf(&b, "restored the track(%q) call", name)
	if c := propStr(e, "commit"); c != "" {
		b.WriteString(" deleted in " + c)
	}
	if f := propStr(e, "file"); f != "" {
		b.WriteString(", " + f)
		if ln := propStr(e, "line"); ln != "" {
			b.WriteString(":" + ln)
		}
	}
	b.WriteString(". nothing was generated: the line came out of your own git history.")
	at := e.Timestamp.UTC()
	return Act{
		Kind: "tracking_pr", Chip: "pr opened", Head: head, Sub: b.String(),
		At: at, When: at.Format("Jan 2 15:04"),
		EvText: "view the pull request", EvHref: url, EvExternal: true,
	}, true
}

// ---- watches ----------------------------------------------------------------------------

func watchesFrom(inv investigate.Investigation, src Sources, now time.Time) []Watch {
	var armed, cold []Watch

	// 1. STEP-CHANGE. Present on every instance the ledger renders on at all, which is what makes
	// the empty ledger populated rather than apologetic: it names the reader's own metrics.
	if n := len(inv.Scanned); n > 0 {
		armed = append(armed, Watch{
			Kind: "step_change", Armed: true, Subject: "step-change detection",
			CheckedText: checkedAt(inv.GeneratedAt),
			Trigger: fmt.Sprintf("%s swept against their own trailing baselines: %s",
				count(n, "metric", "metrics"), nameList(inv.Scanned, 5)),
			Action: "we raise it as a finding, sized by people, ranked by cost",
			EvText: "check the rows", EvHref: "/v1/investigate",
		})
	}

	// 2. GUARDRAILS, one per RUNNING experiment. A stopped or already-reverted experiment is not
	// standing over anything: its act, if it had one, is in the acted section above.
	running := 0
	for _, f := range src.Flags {
		e := f.Experiment
		if e == nil || len(e.Guardrails) == 0 || e.Started.IsZero() ||
			!e.Stopped.IsZero() || !e.RevertedAt.IsZero() {
			continue
		}
		running++
		armed = append(armed, Watch{
			Kind: "guardrail", Armed: true, Subject: f.Key,
			CheckedText: checkedSince(e.GuardrailCheckedAt, now),
			Trigger:     GuardrailTrigger(e.Resolve()),
			Action:      revertAction(),
			EvText:      "open the experiment", EvHref: "#pane-experiments",
		})
	}
	if running == 0 {
		cold = append(cold, Watch{
			Kind: "guardrail", Subject: "guardrail auto-revert",
			Trigger: "no experiment is running, so there is no guardrail to check.",
			Blocked: "needs one running experiment with a guardrail.",
			EvText:  "open the experiment", EvHref: "#pane-experiments",
		})
	}

	// 3. TRACKING BREAKS, one per planned event that is currently arriving. Armed only when the
	// investigator's G1 gate would actually pass — the lookup is the gate, so reading it here is
	// reading the same decision rather than restating it.
	if src.Planned != nil {
		seen := map[string]bool{}
		for _, n := range inv.Scanned {
			seen[n] = true
		}
		for _, p := range src.Plan {
			if !seen[p.Name] || !src.Planned(p.Name) {
				continue // declared but never arriving: nothing to watch going silent
			}
			armed = append(armed, Watch{
				Kind: "tracking", Armed: true, Subject: p.Name,
				CheckedText: checkedAt(inv.GeneratedAt),
				Trigger:     investigate.TrackingTrigger(p.Name),
				// Deliberately promises only what THIS binary does. Opening the pull request is
				// the cloud's half, gated on switches this package cannot see.
				Action: "we raise it as a tracking break carrying the silent-since day and the " +
					"witness metric, which is what the restore robot acts on",
				EvText: "open instrumentation", EvHref: "#connect",
			})
		}
	} else if !DetectionEnabled() {
		// A nil lookup with the switch off means something different from a nil lookup with no
		// plan, and saying the wrong one of those would be a false statement about the instance.
		cold = append(cold, Watch{
			Kind: "tracking", Subject: "tracking-break watch",
			Trigger: "tracking-break detection is switched off, so a silent event is reported as a " +
				"plain regression and never as broken instrumentation.",
			Blocked: "needs " + TrackingBrokeEnv + " unset on this instance.",
			EvText:  "open instrumentation", EvHref: "#connect",
		})
	} else {
		cold = append(cold, Watch{
			Kind: "tracking", Subject: "tracking-break watch",
			Trigger: "no tracking plan is declared, so a silent event and a deliberately retired " +
				"feature look identical, and we refuse to call either one broken.",
			Blocked: "needs a declared tracking plan. your agent can set one over MCP: set_tracking_plan.",
			EvText:  "open instrumentation", EvHref: "#connect",
		})
	}

	// 4. ALERTS, one per enabled rule, described in its own words rather than as "gt 40".
	for _, a := range src.Alerts {
		if !a.Enabled {
			continue // a rule someone switched off is not a gap to nag about
		}
		armed = append(armed, Watch{
			Kind: "alert", Armed: true, Subject: alertSubject(a),
			CheckedText: checkedSince(a.LastChecked, now),
			Trigger:     a.Describe(),
			Action:      alertAction(src.Webhooks),
			EvText:      "open the alerts pane", EvHref: "#pane-alerts",
		})
	}

	// 5. BELOW THE FLOOR. These were small print at the bottom of the page, and they are the
	// exact material an empty ledger needs: what we watch, and the number that would let it speak.
	for _, n := range inv.BelowFloor {
		cold = append(cold, Watch{
			Kind: "floor", Subject: n.Event,
			Trigger: fmt.Sprintf("%s runs at ~%s/day. step-change detection needs %d/day before a "+
				"percentage means anything, so it is watched and cannot yet produce a finding.",
				n.Event, trimNum(n.PerDay), n.NeedPerDay),
			EvText: "chart it", EvHref: "/v1/trends?event=" + n.Event + "&days=30",
		})
	}

	return append(armed, cold...)
}

// GuardrailTrigger states a running experiment's breach condition in the same arithmetic
// confirmedBreach enforces, built from the exported constants rather than restated from memory.
func GuardrailTrigger(e flag.Experiment) string {
	events := make([]string, 0, len(e.Guardrails))
	margin := ""
	for _, g := range e.Guardrails {
		if g.Event == "" {
			continue
		}
		events = append(events, g.Event)
		if margin == "" {
			margin = marginText(g)
		}
	}
	subject := strings.Join(events, " or ")
	if subject == "" {
		subject = "a guardrail metric"
	}
	control := e.Control
	if control == "" {
		control = "control"
	}
	return fmt.Sprintf("%s more than %s worse than %s on two checks at least %s apart, after %s of warm-up",
		subject, margin, control, humanDur(RevertMinGap), humanDur(RevertWarmup))
}

func marginText(g flag.Guardrail) string {
	s := trimNum(g.Margin()) + "%"
	if g.MarginPP != nil && *g.MarginPP > 0 {
		s += fmt.Sprintf(" (or %spp when the control rate is near zero)", trimNum(*g.MarginPP))
	}
	return s
}

// revertAction is the whole point of the RevertMode field: the promised action tracks the real
// switch, so an instance with acting turned off never tells its reader a flag will be pulled.
func revertAction() string {
	if RevertEnabled() {
		return "we turn the flag off and write " + RevertEvent + " onto your timeline"
	}
	return "we record the FAIL and leave the flag alone. " + RevertEnv + " is off."
}

func alertAction(webhooks int) string {
	if webhooks > 0 {
		return fmt.Sprintf("we post it to your %s", count(webhooks, "webhook", "webhooks"))
	}
	return "we record it. no webhook is connected, so nothing is sent."
}

func alertSubject(a alert.Alert) string {
	if s := strings.TrimSpace(a.Name); s != "" {
		return s
	}
	if s := strings.TrimSpace(a.Event); s != "" {
		return s
	}
	return "alert " + a.ID
}

// ---- small renderers --------------------------------------------------------------------

// checkedSince never collapses "never checked" into a pass. That silent collapse is the exact
// defect guardrail_eval.go exists to fix — a check nobody ran reading as a check that succeeded.
func checkedSince(t, now time.Time) string {
	if t.IsZero() {
		return "not checked yet"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "checked " + t.UTC().Format("15:04") + " UTC"
	case d < time.Minute:
		return "checked just now"
	case d < time.Hour:
		return fmt.Sprintf("checked %dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("checked %dh ago", int(d.Hours()))
	default:
		return "checked " + t.UTC().Format("Jan 2 15:04") + " UTC"
	}
}

// checkedAt is for the conditions recomputed at request time — the sweep the reader just caused.
func checkedAt(t time.Time) string {
	if t.IsZero() {
		return "not checked yet"
	}
	return "checked " + t.UTC().Format("15:04") + " UTC"
}

func armedOff(on bool) string {
	if on {
		return "armed"
	}
	return "off"
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func isAre(n int, one, many string) string {
	if n == 1 {
		return "1 " + one + " is"
	}
	return fmt.Sprintf("%d %s are", n, many)
}

func humanDur(d time.Duration) string {
	m := int(d.Minutes())
	return count(m, "minute", "minutes")
}

// nameList names the first few and COUNTS the rest. A truncated list that is silent about its
// tail reads identically to a complete one, which is how "top pages" showing six rows looked the
// same on a site with six pages and one with six hundred.
func nameList(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(names[:max], ", "), len(names)-max)
}

// trimNum renders a rate the way a person would say it: "4", "0.4", "12.5" — never
// "4.033333333333333", which is what a bare float interpolation put on the page.
func trimNum(f float64) string {
	if f >= 10 || f == math.Trunc(f) {
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(f, 'f', 1, 64), "0"), ".")
}

func propStr(e event.Event, k string) string {
	switch v := e.Properties[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return trimNum(v)
	case int:
		return strconv.Itoa(v)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// isHTTP keeps a receipt's URL out of the page unless it is a real web destination. html/template
// would otherwise rewrite an odd scheme to #ZgotmplZ, producing a "view the pull request" link
// that lands nowhere — a dead affordance on the one row that exists to be checked.
func isHTTP(u string) bool {
	u = strings.TrimSpace(u)
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}
