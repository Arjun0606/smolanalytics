package investigate

import (
	"fmt"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// The review almost nobody runs: what did we ship that moved nothing?
//
// Teams are diligent about launching and negligent about the follow-up, because the follow-up has
// no deadline attached to it and nobody's quarter depends on it. So products accumulate surfaces
// that cost maintenance forever and earned nothing once — an onboarding tooltip, a banner, a
// second CTA — each of which someone was once sure about.
//
// This is the highest-leverage thing an automated PM can do, precisely BECAUSE it is the thing a
// human PM reliably skips. It needs no judgment, only patience and arithmetic: the experiment ran,
// it had enough traffic to answer, and the answer was no.

// minDaysBeforeKill is how long an experiment must have run before "it did nothing" is a
// conclusion rather than impatience. Two weeks covers a weekly usage cycle, which is the shortest
// honest window for most products.
const minDaysBeforeKill = 14

// KillList returns experiments old enough and exposed enough to have answered, whose answer was
// no. The gate is deliberately conservative in ONE direction: we would rather stay silent about a
// dud than tell someone to remove something that was actually working.
func KillList(evs []event.Event, flags []flag.Flag, o Opts) []Finding {
	o = o.withDefaults()
	var out []Finding

	for _, f := range flags {
		exp := f.Experiment
		if exp == nil || exp.Goal == "" || exp.Started.IsZero() {
			continue // never ran as an experiment: nothing was claimed, so nothing is owed
		}
		if !exp.Stopped.IsZero() {
			continue // already concluded by a human; do not relitigate their call
		}
		days := int(o.Now.Sub(exp.Started).Hours() / 24)
		if days < minDaysBeforeKill {
			continue
		}

		rep := flag.MeasureRange(evs, f, exp.Goal, exp.Started, o.Now)
		verdict, exposed, ok := readFlat(rep, exp.Control)
		if !ok {
			continue
		}
		// Too little traffic is NOT a dud. "It did nothing" and "we could not tell" are opposite
		// findings, and conflating them is how you get someone deleting a feature that worked.
		if exposed < o.MinPeople {
			continue
		}

		out = append(out, Finding{
			Kind:     KindDeadShip,
			Event:    f.Key,
			Headline: fmt.Sprintf("%q has been running %d days and has not moved %s", f.Key, days, exp.Goal),
			Cause:    verdict,
			NextMove: "conclude it: roll it out, or remove it and take back the surface it costs you",
			Evidence: fmt.Sprintf("/v1/flags/%s/impact?goal=%s", f.Key, exp.Goal),
			// Sized in people rather than money because the cost of a dud is maintenance, not
			// lost revenue — and inventing a dollar figure for it would be exactly the sort of
			// confident nonsense this product exists to stop.
			Cost: Cost{People: exposed, Basis: BasisPeople},
			Day:  exp.Started.Format("2006-01-02"),
			// Not urgent. It has been true for two weeks and will be true tomorrow; putting it in
			// the "needs you" count would drown the one finding that actually does.
			NeedsYou: false,
		})
	}
	return out
}

// readFlat decides whether a report says "no effect". It returns a sentence for the finding, the
// exposed count, and whether the question is answerable at all.
func readFlat(rep flag.Report, control string) (verdict string, exposed int, ok bool) {
	var ctrl *flag.VariantResult
	var arms []flag.VariantResult
	for i := range rep.Variants {
		v := rep.Variants[i]
		exposed += v.Exposed
		if v.Key == control {
			ctrl = &rep.Variants[i]
			continue
		}
		arms = append(arms, v)
	}
	if ctrl == nil || len(arms) == 0 {
		return "", 0, false
	}
	for _, a := range arms {
		// Any significant arm means this shipped something. Not a dud — leave it alone.
		if a.Significant {
			return "", 0, false
		}
		// An arm we cannot read yet is not a dud either.
		if a.SmallSample || !a.DeltaDefined {
			return "", 0, false
		}
	}
	best := arms[0]
	for _, a := range arms[1:] {
		if a.DeltaPct > best.DeltaPct {
			best = a
		}
	}
	return fmt.Sprintf("%d people exposed, best arm %+.1f%% vs control, not significant",
		exposed, best.DeltaPct), exposed, true
}

// WithFlags runs the standard pass and adds the kill list. Separate from Run because the flag
// store is a different dependency than the event log, and Run must stay usable by callers that
// only hold events.
func WithFlags(evs []event.Event, flags []flag.Flag, o Opts) Investigation {
	return WithContext(evs, flags, nil, o)
}

// WithContext is the full pass: change findings, cause attribution, and the kill list.
//
// Deploys are optional. Most instances record none, and the segment half of the attribution works
// without them — "61% of the loss is browser=Safari" needs nothing but the events.
func WithContext(evs []event.Event, flags []flag.Flag, deps []deploys.Deploy, o Opts) Investigation {
	inv := Run(evs, o)
	Attribute(query.WithoutSampler(query.Apply(evs, nil)), inv.Findings, deps, o)
	if kl := KillList(evs, flags, o); len(kl) > 0 {
		inv.Findings = append(inv.Findings, kl...)
		inv.Quiet = false
	}
	inv.Scanned = append(inv.Scanned, fmt.Sprintf("%d experiments", len(flags)))
	// Recomputed WITH the deploy markers, so the ship count in each sentence is real. Run already
	// produced movements without them; this replaces that reading rather than adding a second one.
	if len(deps) > 0 {
		inv.Movements = Movements(evs, deps, MovementOpts{Now: o.Now})
	}
	rank(inv.Findings)
	return inv
}
