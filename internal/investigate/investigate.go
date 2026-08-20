// Package investigate is the part of a product manager's week that is mechanical.
//
// A PM's week is mostly FINDING OUT: noticing a number moved, slicing until it explains itself,
// working out which of six problems is worth doing, and chasing whether last month's ship
// actually worked. The judgment — what should this company build — is a small fraction of it, and
// it stays with the human. The finding out is a loop over an event log, and this is that loop.
//
// The output is deliberately not a dashboard. It is the thing a PM sends on a Monday: a short
// ranked list where every line carries a number, a cause, and a next move. A chart is raw
// material; this is the conclusion drawn from it.
//
// THREE RULES, because they are what separate this from a generated summary:
//
//  1. RANK BY MONEY, NOT BY p-VALUE. A PM prioritises in currency. Where revenue is not
//     instrumented we rank by people affected and SAY that is what we did — never a made-up
//     dollar figure.
//  2. NEVER INVENT A NUMBER. Every finding carries the query that proves it. If a cost cannot be
//     estimated, the finding says so rather than guessing, exactly like every other surface here.
//  3. A QUIET DAY IS AN ANSWER. If nothing is notable, say nothing is notable. Filler findings
//     are how a daily brief teaches someone to stop reading it, and an unread brief replaces
//     nobody.
package investigate

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/whatchanged"
)

// minDailyBefore is the daily rate a metric must have been running at before a percentage move
// in it counts as news. Below this, ordinary variation produces double-digit percentages every
// week and the brief becomes something people skim past.
const minDailyBefore = 5

// Kind classifies a finding. The set is small on purpose: each kind must map to a next move a
// person can actually take, or it is a chart wearing a sentence.
type Kind string

const (
	// KindRegression — a number stepped down and stayed down.
	KindRegression Kind = "regression"
	// KindSurge — a number stepped up. Worth knowing WHY, because it is usually repeatable.
	KindSurge Kind = "surge"
	// KindDeadShip — something shipped and moved nothing. Nobody does this review, which is why
	// products accumulate surfaces that cost maintenance and earn nothing.
	KindDeadShip Kind = "dead_ship"
)

// Basis records HOW a cost was arrived at, and travels with the number everywhere it is shown.
//
// A figure whose derivation is invisible gets either over-trusted or ignored, and both are worse
// than a smaller number that explains itself.
type Basis string

const (
	// BasisPeople — no revenue is instrumented, so the size is people affected.
	BasisPeople Basis = "people affected — no revenue tracked on this event"
	// BasisUnknown — not enough data to size it at all.
	BasisUnknown Basis = "not enough data to size this"
)

// BasisRevenue names the property the money figure was computed from. The basis travels with the
// number everywhere it is shown, so a reader can see that "$14,880/mo" came from summing
// `amount` on their own checkout events rather than from a model somebody fitted.
func BasisRevenue(prop string) Basis {
	return Basis("people affected × mean revenue per person, from the " + prop + " property on this event")
}

// Cost is how big a finding is. UsdPerMonth is only ever set when revenue is genuinely
// instrumented; the zero value means "we did not estimate money", never "zero money".
type Cost struct {
	UsdPerMonth float64 `json:"usd_per_month,omitempty"`
	People      int     `json:"people"`
	Basis       Basis   `json:"basis"`
	// Size is the rendered figure, computed by Cost.Size() and carried in the JSON.
	//
	// It is serialized rather than left to each consumer because the consumers are not all Go:
	// the marketing site renders findings from this JSON in TypeScript, and a Go-only "everyone
	// must call Size()" test cannot reach across that boundary. Shipping the rendered string is
	// the only way the one-renderer property survives the language change — otherwise the web
	// would quietly keep printing the people half after the engine learned to price things.
	SizeText string `json:"size,omitempty"`
}

// Finding is one line of the brief.
type Finding struct {
	Kind     Kind   `json:"kind"`
	Headline string `json:"headline"` // the conclusion, in one sentence, with the number in it
	Cause    string `json:"cause"`    // why, or the honest absence of a why
	NextMove string `json:"next_move"`
	Evidence string `json:"evidence"` // the /v1 query that recomputes this line
	Cost     Cost   `json:"cost"`
	Event    string `json:"event,omitempty"`
	Day      string `json:"day,omitempty"`

	// Fingerprint identifies this finding stably across sweeps: the same kind of change to the
	// same event on the same day is the same finding tomorrow, so an annotation made today still
	// attaches. Deliberately NOT a hash — kind|event|day is already unique, human-readable in an
	// API response, and debuggable without a rainbow table of our own making.
	Fingerprint string `json:"fingerprint,omitempty"`

	// ActedAt is when a human said they acted on this, stamped from the outcome ledger by the
	// serving layer. Zero means nobody has.
	ActedAt time.Time `json:"acted_at,omitempty"`

	// Recovered marks a regression whose metric has returned to its pre-drop level — computed by
	// the same daily-series arithmetic that detected the drop, never asserted. It says
	// "recovered", not "fixed": no causality is known. A recovered finding stays on the queue so
	// the week reads whole, but it must never look like open work.
	Recovered bool `json:"recovered,omitempty"`

	// NeedsYou marks the ones worth a human's attention today. Everything else is context.
	// A brief where every line is urgent is a brief with no triage in it.
	NeedsYou bool `json:"needs_you"`
}

// Investigation is one pass over the product.
type Investigation struct {
	GeneratedAt time.Time `json:"generated_at"`
	Findings    []Finding `json:"findings"`
	// Quiet is true when the sweep ran properly and found nothing worth saying. It is a real
	// answer and the brief must render it as one.
	Quiet bool `json:"quiet"`
	// Scanned is what was looked at, so "nothing to report" can be distinguished from
	// "nothing was checked" — the difference between a calm product and a broken sweep.
	Scanned []string `json:"scanned"`
	// Movements is the quarter-level reading: per metric, did it move at all across everything
	// that shipped. The findings above are what changed THIS WEEK; this is whether any of it
	// added up. It is the half a founder forwards.
	Movements []Movement `json:"movements,omitempty"`

	// BelowFloor names the events this instance tracks that CANNOT currently produce a finding,
	// with the arithmetic. The detection gates are correct — a 3-a-day product genuinely moves
	// 50% every other day by chance — but correct behaviour that renders as permanent silence is
	// zero delivered value, and the smallest products this tool targets are exactly the ones the
	// gates exclude. "Below the floor, and here is the number that would change that" converts a
	// silent product into a visible one on day one.
	BelowFloor []FloorNote `json:"below_floor,omitempty"`
}

// FloorNote is one metric's honest exclusion.
type FloorNote struct {
	Event string `json:"event"`
	// PerDay is the metric's current daily rate over the window.
	PerDay float64 `json:"per_day"`
	// NeedPerDay is the baseline the change detector requires before a percentage may speak.
	NeedPerDay int    `json:"need_per_day"`
	Note       string `json:"note"`
}

// Opts configures a pass.
type Opts struct {
	Days int       // lookback for change detection (default 30)
	Now  time.Time // injected for tests
	// MinPeople is the floor below which a move is noise rather than news. A three-person
	// product produces a "50% drop" every other day.
	MinPeople int
}

func (o Opts) withDefaults() Opts {
	if o.Days <= 0 {
		o.Days = 30
	}
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	}
	if o.MinPeople <= 0 {
		o.MinPeople = 20
	}
	return o
}

// Run performs one investigation pass.
//
// It takes the raw event slice and does its own scoping, so a caller cannot accidentally hand it
// a pre-filtered window and get a change detector that cannot see the "before".
func Run(evs []event.Event, opts Opts) Investigation {
	o := opts.withDefaults()
	inv := Investigation{GeneratedAt: o.Now}

	evs = query.Apply(evs, nil) // production scope, same as every report
	// The sampler is this tool's own robot. A change in how often we crawl is not news about
	// their product, and it arrives daily, which makes it the single most reliable way to
	// manufacture a fake trend.
	evs = query.WithoutSampler(evs)

	names := productEvents(evs)
	for _, n := range names {
		inv.Scanned = append(inv.Scanned, n)
		if f, ok := changeFinding(evs, n, o); ok {
			inv.Findings = append(inv.Findings, f)
			continue
		}
		// If this metric could never have produced a finding — its baseline is under the floor
		// the detector requires — say so with the arithmetic, once, rather than being silent
		// about it forever. Only genuinely floored metrics: a metric above the floor with no
		// step change is a quiet metric, which is a different (and good) answer.
		rate := dailyMean(evs, n, o.Now.AddDate(0, 0, -o.Days), o.Now)
		if rate > 0 && rate < minDailyBefore {
			inv.BelowFloor = append(inv.BelowFloor, FloorNote{
				Event: n, PerDay: math.Round(rate*10) / 10, NeedPerDay: minDailyBefore,
				Note: fmt.Sprintf("%s runs at ~%.1f/day; step-change detection needs a baseline of about %d/day before a percentage means anything. Below that, a 50%% swing is a coin flip, and reporting it would be noise dressed as news.",
					n, rate, minDailyBefore),
			})
		}
	}

	// ATTRIBUTE HERE, NOT ONLY IN WithContext.
	//
	// Attribute() was reached only from WithContext, which only the dashboard and the share page
	// call. So the CLI brief, GET /v1/brief and the cloud daily email — every unprompted surface,
	// the ones a person reads without opening a browser — printed the raw placeholder "cause not
	// yet attributed" on every finding for as long as they have existed, while the dashboard said
	// "62% of the loss is os=Android" about the same event on the same instance.
	//
	// The deploy half genuinely needs a store and is passed nil here; the SEGMENT half needs
	// nothing but the events already in hand, works on every instance, and is frequently the more
	// useful of the two — "it is only mobile Safari" turns an unbounded investigation into a
	// twenty-minute one. Withholding it from the surfaces people actually read was not a design
	// decision, it was a call site nobody added.
	//
	// WithContext re-runs this with real deploys, which overwrites rather than duplicates.
	Attribute(evs, inv.Findings, nil, o)
	// And close the loop: a regression whose metric came back is marked recovered, so the queue
	// can retire it visibly instead of leaving the reader to re-derive last week's state.
	MarkRecovered(evs, inv.Findings, o)

	// The quarter-level reading belongs on EVERY caller, not just the one with a deploy store.
	// Wiring it into WithContext alone meant brief.Build — the CLI, the email and the dashboard —
	// silently rendered no movements at all, which is the same "two paths, one question" split
	// this package exists to stop. Deploys are optional: without them the ship count is zero and
	// the sentence says so rather than implying nothing shipped.
	inv.Movements = Movements(evs, nil, MovementOpts{Now: o.Now})

	for i := range inv.Findings {
		if inv.Findings[i].Recovered {
			inv.Findings[i].NeedsYou = false
		}
		f := &inv.Findings[i]
		f.Fingerprint = string(f.Kind) + "|" + f.Event + "|" + f.Day
	}
	rank(inv.Findings)
	StampSizes(inv.Findings)
	inv.Quiet = len(inv.Findings) == 0
	return inv
}

// changeFinding looks for a step change in one event and turns it into a costed line.
func changeFinding(evs []event.Event, name string, o Opts) (Finding, bool) {
	series := whatchanged.TrimLeadingSilence(whatchanged.Series(evs, name, o.Days, o.Now))
	if len(series) < 6 { // the detector needs 3 days either side of a split
		return Finding{}, false
	}
	c := whatchanged.Detect(series)
	if !c.Found || c.Day == "" {
		return Finding{}, false
	}

	// People affected per month, from the size of the daily step. Deliberately arithmetic a
	// reader can redo in their head: (before − after) per day × 30.
	perDay := c.Before - c.After
	if perDay < 0 {
		perDay = -perDay
	}
	monthly := int(perDay * 30)
	// TWO gates, because either alone lets noise through.
	//
	// Volume: a product doing 2 signups a day loses one and that is a "50% collapse" — real
	// arithmetic, and worthless as news, because one person changing their mind produces it.
	// Extrapolating first hid this: 1/day became "30 people a month", which reads substantial.
	// So the BASELINE RATE has to clear a floor before a percentage is allowed to speak.
	//
	// Size: and the move itself still has to affect enough people to be worth a person's day.
	if c.Before < minDailyBefore || monthly < o.MinPeople {
		return Finding{}, false
	}

	f := Finding{
		Event:    name,
		Day:      c.Day,
		Cost:     Cost{People: monthly, Basis: BasisPeople},
		Evidence: fmt.Sprintf("/v1/explain?event=%s&days=%d", name, o.Days),
	}
	switch c.Direction {
	case "drop":
		f.Kind = KindRegression
		f.Headline = fmt.Sprintf("%s fell %.0f%% on %s", name, absPct(c.PercentPct), c.Day)
		f.NextMove = "find the ship that did it: " + f.Evidence
		// A drop is the only kind that is reliably worth interrupting someone for. A surge is
		// good news and keeps until they read the brief.
		f.NeedsYou = true
	case "spike":
		f.Kind = KindSurge
		f.Headline = fmt.Sprintf("%s rose %.0f%% on %s", name, absPct(c.PercentPct), c.Day)
		f.NextMove = "work out what caused it — a surge you can explain is a surge you can repeat"
	default:
		return Finding{}, false
	}
	// Size it in money if — and only if — this metric genuinely carries revenue. Silently leaves
	// the people figure in place otherwise, which is a real answer rather than a fallback.
	sizeInMoney(evs, &f, o)

	// No cause yet. Saying "unexplained" out loud is better than an empty field, which reads as
	// though nobody looked.
	f.Cause = "cause not yet attributed — no deploy or channel has been matched to this day"
	return f, true
}

// productEvents returns the events worth investigating: what the product deliberately sends.
//
// Autocapture ($pageview, $click, $engagement) is excluded because a move in it is almost never
// a finding a person can act on — it is the raw material other findings are computed FROM. A
// brief led by "page view fell 12%" is the "cacophony of numbers" complaint in one line.
func productEvents(evs []event.Event) []string {
	seen := map[string]int{}
	for _, e := range evs {
		if len(e.Name) > 0 && e.Name[0] == '$' {
			continue
		}
		seen[e.Name]++
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	// Busiest first, so a tie in cost breaks toward the event that carries more of the product.
	sort.Slice(out, func(i, j int) bool {
		if seen[out[i]] != seen[out[j]] {
			return seen[out[i]] > seen[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// rank orders the brief: money where we have it, then things needing attention, then size.
//
// NOT simply "biggest first", and the live output is what made that precise. A 480-people SURGE
// sorts below a 210-people regression, because a surge is good news that keeps until someone
// reads the brief and a regression is costing money now. Urgency outranks magnitude among
// equally-sized findings; magnitude decides between findings of equal urgency.
func rank(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		// A recovered finding sorts LAST regardless of size. The most expensive drop of the month,
		// once recovered, must not take the slab — the slab is the one thing that needs you, and a
		// resolved problem leading the desk teaches the reader the desk is history, not work.
		if a.Recovered != b.Recovered {
			return !a.Recovered
		}
		if (a.Cost.UsdPerMonth > 0) != (b.Cost.UsdPerMonth > 0) {
			return a.Cost.UsdPerMonth > 0
		}
		if a.Cost.UsdPerMonth != b.Cost.UsdPerMonth {
			return a.Cost.UsdPerMonth > b.Cost.UsdPerMonth
		}
		if a.NeedsYou != b.NeedsYou {
			return a.NeedsYou
		}
		return a.Cost.People > b.Cost.People
	})
}

func absPct(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// AnyUnattributed reports whether any finding could not be narrowed to a ship or a segment.
//
// Lets a renderer state the "record deploys and this becomes which ship did it" instruction ONCE,
// underneath, instead of printing it inside every unexplained row. On an instance with no deploys
// recorded that is most rows, and the dashboard was showing the identical two-line paragraph
// twice inside its first screen.
func (inv Investigation) AnyUnattributed() bool {
	for _, f := range inv.Findings {
		if f.Kind != KindDeadShip && !f.Attributed() {
			return true
		}
	}
	return false
}

// NeedsYouCount is the triage line at the top of the brief: "3 things changed. 1 needs you."
func (inv Investigation) NeedsYouCount() int {
	n := 0
	for _, f := range inv.Findings {
		if f.NeedsYou {
			n++
		}
	}
	return n
}
