package investigate

import (
	"fmt"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// The metric inversion: what moved, over a quarter, across everything you shipped.
//
// This replaces the per-ship claim, which was refuted by measurement. Deriving "what was this
// merge supposed to move" from real pull requests yielded a checkable claim on 3.8% of 210 merges
// across three production repositories — nine in ten merges are infrastructure, bug fixes and
// plumbing, and the ~10% that are user-facing state no expected effect because nobody wrote one
// down. See internal/claim/RESULT.md.
//
// So the unit changes. Instead of asking "did THIS ship do what it promised", which almost never
// has an answer, ask "is this metric any different after everything you shipped", which always
// does:
//
//	checkout conversion  3.1% -> 3.0%  unchanged  across 23 ships
//
// That sentence needs no claim object, is computable from data every instance already has, and is
// considerably harder to shrug off than a per-merge verdict. It is also the sentence a founder
// forwards, which is the whole distribution argument for the weekly update.
//
// MEASURED AS A RATE, NOT A COUNT. "signups went from 400 to 600" says nothing if traffic doubled.
// The numerator is people who did the event in the half; the denominator is people who were active
// at all in that half. A rate survives growth, a count does not, and reporting a count as progress
// during a traffic increase is the most common way an analytics tool flatters its customer.

// Verdict for one metric over the window. A closed set, and "cannot tell" is a first-class member
// rather than a silent fallback — "it did not move" and "we could not measure it" are opposite
// findings, and collapsing them is how a tool tells someone their work was wasted when it was
// merely unmeasured.
type Verdict string

const (
	MovedUp    Verdict = "moved up"
	MovedDown  Verdict = "moved down"
	Unchanged  Verdict = "unchanged"
	CannotTell Verdict = "cannot tell — too few people to say"
	NeverFired Verdict = "not recorded in this window"
)

// Movement is one metric's reading across the window.
type Movement struct {
	Metric string `json:"metric"`
	// Before/After are the share of ACTIVE people who did this event in each half, as a percentage.
	Before float64 `json:"before_pct"`
	After  float64 `json:"after_pct"`
	// People is the count in the later half, so a reader can see the weight behind the rate.
	People   int     `json:"people"`
	Verdict  Verdict `json:"verdict"`
	PValue   float64 `json:"p_value,omitempty"`
	Ships    int     `json:"ships"`
	Evidence string  `json:"evidence"`
	// Headline is the whole finding in one line, ready to render.
	Headline string `json:"headline"`
	// Label is the verdict in one or two words, for a table cell.
	//
	// Carried in the JSON rather than derived per surface. The marketing site was reducing the
	// verdict to a chip in TypeScript while the dashboard printed the whole sentence, so the same
	// reading rendered as a scannable four-column table in one place and as seven near-identical
	// prose lines in the other — five of them ending "statistically unchanged across 2 ships".
	// The product had the worse version of its own component.
	Label string `json:"label"`
}

// label reduces a verdict to the one or two words a table cell can hold, without losing the
// distinction that matters most: "unchanged" and "can't tell" are opposite findings.
func label(v Verdict) string {
	switch v {
	case MovedUp:
		return "up"
	case MovedDown:
		return "down"
	case CannotTell:
		return "can't tell"
	case NeverFired:
		return "not recorded"
	default:
		return "unchanged"
	}
}

// MovementOpts configures the reading.
type MovementOpts struct {
	Now  time.Time
	Days int // total window, split in half. Default 90.
	// MinPeople is the floor below which a rate is not worth stating. Default 30 per half.
	MinPeople int
}

func (o MovementOpts) withDefaults() MovementOpts {
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	}
	if o.Days <= 0 {
		o.Days = 90
	}
	if o.MinPeople <= 0 {
		o.MinPeople = 30
	}
	return o
}

// Movements reads every product metric across the window and counts the ships in between.
func Movements(evs []event.Event, deps []deploys.Deploy, o MovementOpts) []Movement {
	o = o.withDefaults()
	evs = query.WithoutSampler(query.Apply(evs, nil))

	end := o.Now
	mid := end.AddDate(0, 0, -o.Days/2)
	start := end.AddDate(0, 0, -o.Days)

	// Active people per half is the denominator. Anyone who did anything at all counts, so the
	// rate answers "of the people using this product, what share reach X" — which is the question
	// a founder actually has, and the one that does not move when traffic does.
	activeA, activeB := map[string]bool{}, map[string]bool{}
	didA := map[string]map[string]bool{}
	didB := map[string]map[string]bool{}

	for _, e := range evs {
		if e.Timestamp.Before(start) || !e.Timestamp.Before(end) {
			continue
		}
		first := e.Timestamp.Before(mid)
		if first {
			activeA[e.DistinctID] = true
		} else {
			activeB[e.DistinctID] = true
		}
		if len(e.Name) > 0 && e.Name[0] == '$' {
			continue // autocapture is the denominator's raw material, never a headline metric
		}
		m := didB
		if first {
			m = didA
		}
		if m[e.Name] == nil {
			m[e.Name] = map[string]bool{}
		}
		m[e.Name][e.DistinctID] = true
	}

	ships := 0
	for _, d := range deps {
		if !d.At.Before(start) && d.At.Before(end) {
			ships++
		}
	}

	names := map[string]bool{}
	for n := range didA {
		names[n] = true
	}
	for n := range didB {
		names[n] = true
	}

	out := make([]Movement, 0, len(names))
	nA, nB := len(activeA), len(activeB)
	for n := range names {
		cA, cB := len(didA[n]), len(didB[n])
		m := Movement{
			Metric: n, People: cB, Ships: ships,
			Evidence: fmt.Sprintf("/v1/rows?event=%s&days=%d&unique=1", n, o.Days/2),
		}
		if nA > 0 {
			m.Before = round1(100 * float64(cA) / float64(nA))
		}
		if nB > 0 {
			m.After = round1(100 * float64(cB) / float64(nB))
		}

		switch {
		case cA == 0 && cB == 0:
			m.Verdict = NeverFired
		case nA < o.MinPeople || nB < o.MinPeople:
			// Not enough people to state a rate at all. Distinct from "no change", and saying so
			// is what stops a small product being told its work was wasted when it simply cannot
			// be measured yet.
			m.Verdict = CannotTell
		default:
			// The SAME test the experiment reports use, exported rather than reimplemented — a
			// fourth definition of "did this change" is exactly how surfaces come to disagree.
			differs, p := flag.ProportionsDiffer(cB, nB, cA, nA, 0.05)
			m.PValue = round3(p)
			switch {
			case !differs:
				m.Verdict = Unchanged
			case m.After > m.Before:
				m.Verdict = MovedUp
			default:
				m.Verdict = MovedDown
			}
		}
		m.Headline = headline(m, o.Days)
		m.Label = label(m.Verdict)
		out = append(out, m)
	}

	// Biggest population first: the metric most of the product touches leads.
	sort.Slice(out, func(i, j int) bool {
		if out[i].People != out[j].People {
			return out[i].People > out[j].People
		}
		return out[i].Metric < out[j].Metric
	})
	return out
}

// headline writes the line as a person would say it. The ship count is in every sentence,
// including the good ones — "up 25% across 23 ships" is still a question about which of the 23.
func headline(m Movement, days int) string {
	ships := fmt.Sprintf("across %d ship%s", m.Ships, plural(m.Ships))
	if m.Ships == 0 {
		ships = "with nothing recorded as shipped"
	}
	switch m.Verdict {
	case NeverFired:
		return fmt.Sprintf("%s was not recorded at all in the last %d days", m.Metric, days)
	case CannotTell:
		return fmt.Sprintf("%s: too few people in the window to say whether it moved", m.Metric)
	case Unchanged:
		return fmt.Sprintf("%s: %.1f%% then, %.1f%% now, statistically unchanged %s",
			m.Metric, m.Before, m.After, ships)
	case MovedUp:
		return fmt.Sprintf("%s: %.1f%% then, %.1f%% now, up %s", m.Metric, m.Before, m.After, ships)
	default:
		return fmt.Sprintf("%s: %.1f%% then, %.1f%% now, DOWN %s", m.Metric, m.Before, m.After, ships)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }
