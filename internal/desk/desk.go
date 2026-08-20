// Package desk is the ONE assembly of an investigation, shared by every door that serves it.
//
// It exists because this codebase has now learned the same lesson three times: a question with
// two call sites grows two answers. Attribute() was reached only from the dashboard, so the
// emailed brief said "cause not yet attributed" about a finding the desk had already explained.
// Movements were wired into WithContext only, so the CLI rendered none. The fix each time was to
// collapse the callers into one, and this package is that collapse done in advance rather than
// after a customer notices.
//
// So: /v1/investigate, the MCP investigate tool, the dashboard desk, the share page and the
// daily brief all build their Opts here and read the tracking plan here. Nobody assembles their
// own. The byte-agreement test between the route and the MCP tool is only meaningful because
// there is nothing left for the two of them to disagree about.
//
// It is also where the environment is read. internal/investigate stays pure — no os.Getenv, no
// clock — so its gates are table tests; the switch that turns the newest gate off entirely is a
// deployment concern and lives out here with the other serving-layer decisions.
package desk

import (
	"os"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
	"github.com/Arjun0606/smolanalytics/internal/trackplan"
)

// ReadMeFirst is the contract text every agent reads before the findings themselves. Served
// identically by the HTTP route and the MCP tool, because an agent that learns the rules one way
// over MCP and another way over HTTP is an agent that will eventually quote the wrong ones.
const ReadMeFirst = "Findings are ranked by cost — money when the metric carries revenue, people otherwise. " +
	"needs_you=true is worth interrupting a human for; recovered=true is closed work kept for the record. " +
	"cause is computed correlation and says so; never present it as proof. " +
	"A quiet=true result with a populated scanned list is a real answer: nothing moved enough to matter. " +
	"kind=tracking_broke is the one class that is NOT a product problem: an event in the declared tracking plan " +
	"went silent while a witness metric held, which means a track() call was deleted — its cost is deliberately " +
	"unsized because we stopped counting, and its fix is to put the call back. Every other regression carries " +
	"tracking_ruled_out saying why it is not that."

// TrackingBrokeEnv is the kill switch for DETECTION. Set it to "off" and a regression is never
// promoted to tracking_broke anywhere — dashboard, brief, MCP and API alike — which means the
// cloud's actuator has nothing to act on. Detection-off and action-off are separate levers on
// purpose: "stop opening pull requests" and "stop telling me my tracking is broken" are
// different requests, and a safety feature with one switch gets the whole product switched off.
const TrackingBrokeEnv = "SMOLANALYTICS_TRACKING_BROKE"

// DetectionEnabled reports whether tracking-break promotion may run. Default on; only the exact
// word "off" disables it, so a typo fails loudly toward the documented behaviour.
func DetectionEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv(TrackingBrokeEnv)), "off")
}

// Planned turns the tracking-plan store into the lookup the investigator's G1 gate needs.
//
// Returns nil — which the gate reads as "refuse" — in all three of the cases that mean the same
// thing: no store, an empty plan, or the switch off. Nil is never a permissive default here.
func Planned(tp *trackplan.Store) investigate.PlanLookup {
	if tp == nil || !DetectionEnabled() {
		return nil
	}
	p := tp.Get()
	if len(p.Events) == 0 {
		return nil
	}
	set := make(map[string]bool, len(p.Events))
	for _, e := range p.Events {
		set[e.Name] = true
	}
	return func(name string) bool { return set[name] }
}

// Sources is the instance state an investigation reads beyond the events themselves. Every field
// is optional and nil-safe: a bare instance with no flags, no deploys, no ledger and no plan
// still gets a full investigation, it just gets fewer kinds of answer.
type Sources struct {
	Flags   []flag.Flag
	Deploys []deploys.Deploy
	Acted   func(string) (time.Time, bool)
	Planned investigate.PlanLookup
	// Plan is the DECLARED events themselves. Planned above is a lookup — it answers "is this
	// one declared" and cannot be enumerated — and the ledger has to name every event it is
	// standing over, which is a list. Both come from the same store; neither replaces the other.
	Plan []trackplan.PlannedEvent
	// Alerts and Webhooks are the other two standing orders on an instance: what a human asked
	// to be told about, and whether there is anywhere to tell them.
	Alerts   []alert.Alert
	Webhooks int
}

// Desk is one pass: the investigation, plus the ledger of what the system is standing over and
// what it has already done. Every door builds this, so the page cannot show a ledger the API
// disagrees with — the same collapse this package was created to perform for the investigation.
type Desk struct {
	Investigation investigate.Investigation `json:"investigation"`
	Ledger        Ledger                    `json:"ledger"`
}

// BuildDesk runs the full pass: change findings, cause attribution, tracking-break promotion, the
// kill list, the quarter movements, the outcome-ledger overlay — and then the ledger over the top.
func BuildDesk(evs []event.Event, src Sources, o investigate.Opts) Desk {
	o.Planned = src.Planned
	now := o.Now
	if now.IsZero() {
		now = time.Now().UTC()
		o.Now = now
	}
	inv := investigate.WithContext(evs, src.Flags, src.Deploys, o)
	if src.Acted != nil {
		investigate.ApplyActed(inv.Findings, src.Acted, now)
	}
	return Desk{Investigation: inv, Ledger: buildLedger(inv, evs, src, o)}
}

// Build is BuildDesk's investigation half, kept because the CLI brief and the share page want
// exactly that and nothing more.
func Build(evs []event.Event, src Sources, o investigate.Opts) investigate.Investigation {
	return BuildDesk(evs, src, o).Investigation
}

// Doc is the serialized shape of a desk, identical on both public doors. The ledger travels with
// it so an agent reading over MCP and a browser reading the page are looking at one answer.
func Doc(d Desk) map[string]any {
	return map[string]any{
		"investigation": d.Investigation,
		"ledger":        d.Ledger,
		"read_me_first": ReadMeFirst,
	}
}
