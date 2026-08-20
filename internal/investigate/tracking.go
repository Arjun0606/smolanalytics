package investigate

// TRACKING BROKE, NOT THE PRODUCT.
//
// Every regression this package reports has two possible stories, and they demand opposite
// responses. Either the product got worse — which needs a human with judgment — or the
// MEASUREMENT got worse: somebody deleted a track() call and the number fell to zero while
// nothing about the product changed at all. The second story is the only one in this whole
// package whose fix is mechanical, and it is the one this file exists to prove.
//
// It is a promotion, not a new detector. The regression is already found by whatchanged.Detect;
// ClassifyTracking takes those findings and asks five questions of each. All five must answer
// yes. The FIRST no writes the arithmetic into TrackingRuledOut and the finding stays a plain
// regression — because "we checked whether your tracking broke and here is why it did not" is a
// real answer, and silently declining is how a reader learns the check does not run.
//
// PURE ON PURPOSE: no os.Getenv, no time.Now(), no I/O. The kill switch and the tracking-plan
// store both live in the serving layer, so every gate in here is a table test with no fixture
// server behind it.
//
// WHY THE BAR IS THIS HIGH: a promoted finding is the trigger for software that opens a pull
// request in somebody's repository without being asked. The worst case of a wrong PR is a
// duplicate track() call, which is recoverable — but the worst case of a CONFIDENT wrong PR is
// that the whole category gets switched off, and then nothing acts on anything. So the gates are
// tuned to refuse rather than to catch: we would much rather miss a real tracking break than
// open a PR against a feature somebody deliberately removed.

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// KindTrackingBroke — an event in the declared tracking plan went silent while the traffic that
// produces it did not. This is instrumentation loss, and its fix is a line of code that used to
// exist. It is the only finding kind in this package whose next move is mechanical enough for
// software to take unattended.
const KindTrackingBroke Kind = "tracking_broke"

// BasisBlind is the size basis of a tracking break, and it is deliberately not a number.
//
// The regression this was promoted from said "~1,240 people/mo". Nobody lost 1,240 people — we
// stopped COUNTING 1,240 actions, and the true user impact is exactly the thing we can no longer
// measure. Reporting instrumentation loss as user loss is the invented number rule 2 of this
// package's doc comment forbids, so the size renders as a sentence rather than a figure.
const BasisBlind Basis = "actions we can no longer count — this is instrumentation loss, not measured user loss"

// blindSizeText is what a blind cost renders as. NOT "0 people": a zero in the size column reads
// as "nothing is wrong", which is the opposite of what a tracking break means.
const blindSizeText = "not sized — we stopped counting"

// TrackingPrEvent is the receipt written onto the operator's own timeline when the cloud opens a
// pull request restoring a deleted track() call — the $experiment_reverted pattern, for the same
// reason: an autonomous act that leaves no trace where people look is indistinguishable from a
// bug. Declared here rather than in the cloud so both halves of the wire name it once.
const TrackingPrEvent = "$tracking_pr_opened"

// PlanLookup answers "was this event declared in the tracking plan". A nil lookup means no plan
// is declared, which is a REFUSAL, never a permissive default: without a statement of intent,
// "stopped firing" and "was retired on purpose" are indistinguishable.
type PlanLookup func(name string) bool

const (
	// silenceShare / silenceFloorMin define the silence floor: the drop must be to (near) zero.
	// A 40% fall is a product change; only silence is a deleted call site.
	silenceShare    = 0.02
	silenceFloorMin = 1.0
	// minQuietDays — one quiet day is a weekend, a cache, or an outage.
	minQuietDays = 3
	// minAgeDays / maxAgeDays — under two days the data is not in; past three weeks the
	// repository has moved on and restoring a line stops being a mechanical edit.
	minAgeDays = 2
	maxAgeDays = 21
	// witnessTolerance — how much the witness metric may fall and still count as having HELD.
	witnessTolerance = 0.90
	// witnessWindowDays is the "before" window for both the event and its witness.
	witnessWindowDays = 14
	// pathCoverage / pathDominance decide whether this event is concentrated enough on one page
	// for that page's traffic to be the witness rather than the whole instance.
	pathCoverage  = 0.80
	pathDominance = 0.60
)

// ClassifyTracking promotes the regressions that are provably instrumentation loss, and explains
// the refusal on every one that is not.
//
// findings is mutated in place. Only KindRegression rows are considered: a surge cannot be a
// deleted call site, and a dead ship is a judgement about a flag.
func ClassifyTracking(evs []event.Event, findings []Finding, planned PlanLookup, o Opts) {
	o = o.withDefaults()
	// Same scope as every other reading in this package, applied HERE rather than trusted from
	// the caller: a pre-filtered slice would give the gates a different "before" than the
	// detector saw, and the whole discriminator is a comparison of two windows.
	evs = query.WithoutSampler(query.Apply(evs, nil))
	for i := range findings {
		f := &findings[i]
		if f.Kind != KindRegression || f.Event == "" || f.Day == "" {
			continue
		}
		day, err := time.Parse("2006-01-02", f.Day)
		if err != nil {
			continue
		}
		classifyOne(evs, f, day.UTC(), planned, o)
	}
}

// classifyOne applies the five gates in order. It writes EITHER a promotion or a rule-out
// sentence, never neither — that exclusive-or is the property the invariant test pins.
func classifyOne(evs []event.Event, f *Finding, day time.Time, planned PlanLookup, o Opts) {
	// G1 — DECLARED INTENT. The tracking plan is the only statement we have of what this app
	// MEANS to send. Without it, a stopped event and a deliberately retired feature look
	// identical, and opening a pull request against a feature somebody removed on purpose is the
	// exact failure that would end this category for us.
	if planned == nil {
		// Worded to be true in BOTH states that produce a nil lookup: no plan declared, and the
		// operator having switched the check off. "No plan is declared" would be a false
		// statement to somebody who declared one and then disabled detection, and a rule-out
		// line that lies about why is worse than no rule-out line.
		f.TrackingRuledOut = "no tracking plan is in play for this instance, so we cannot say this event was " +
			"supposed to still be firing — declare one with set_tracking_plan, and if you already have, " +
			"check SMOLANALYTICS_TRACKING_BROKE is not set to off"
		return
	}
	if !planned(f.Event) {
		f.TrackingRuledOut = fmt.Sprintf("%s is not in the tracking plan, so a stop is a product change until you declare otherwise", f.Event)
		return
	}

	// G2 — TOTAL, NOT PARTIAL. A deleted call site produces silence. Anything still arriving means
	// the code still runs and fewer people reached it, which is a product change.
	before := dailyMean(evs, f.Event, day.AddDate(0, 0, -witnessWindowDays), day)
	after := dailyMean(evs, f.Event, day, o.Now)
	floor := math.Max(silenceFloorMin, silenceShare*before)
	if after > floor {
		f.TrackingRuledOut = fmt.Sprintf(
			"%s is still arriving at ~%.1f/day against ~%.1f/day before %s — a deleted call site goes silent, and this did not",
			f.Event, after, before, f.Day)
		return
	}

	// G4 — RECENT BUT SETTLED. Evaluated BEFORE G3 on purpose, though it is numbered after it.
	//
	// Not style: ordered the other way, the lower bound is DEAD CODE. G3 requires three full quiet
	// days since f.Day, and f.Day is parsed from a date so it is always midnight — so passing G3
	// already proves the finding is at least three days old, and `age < 2` could never once be
	// true. A gate that cannot fire is not a gate, it is a comment that reads like one, and the
	// mutation test that deleted it stayed green, which is exactly how that gets discovered late.
	//
	// Running the age window first also gives a better answer to a caller who built the finding
	// itself rather than getting it from Detect: "the data is not in yet" is the real reason a
	// one-day-old drop is not actionable, and "only 1 full quiet day" is a symptom of it.
	age := int(o.Now.Sub(day).Hours() / 24)
	if age < minAgeDays {
		f.TrackingRuledOut = fmt.Sprintf(
			"%s went silent %d day(s) ago — under %d days the data is not in yet, and today's partial day is not evidence",
			f.Event, age, minAgeDays)
		return
	}
	if age > maxAgeDays {
		f.TrackingRuledOut = fmt.Sprintf(
			"%s went silent %d days ago — past %d days the repository has moved on, so putting the call back is no longer a mechanical edit",
			f.Event, age, maxAgeDays)
		return
	}

	// G3 — SUSTAINED. One quiet day is a weekend or an outage, and both come back on their own.
	quiet := quietFullDays(evs, f.Event, day, o.Now, floor)
	if quiet < minQuietDays {
		f.TrackingRuledOut = fmt.Sprintf(
			"%s has been at or below ~%.1f/day for only %d full day(s) since %s — a single quiet day is a weekend or an outage, so this waits for %d",
			f.Event, floor, quiet, f.Day, minQuietDays)
		return
	}

	// G5 — THE WITNESS HELD. The whole discriminator. Everything above says "this event stopped";
	// only this says "and the product behind it did not", which is the difference between a
	// deleted line of code and a broken checkout.
	w, ok := witnessFor(evs, f.Event, day, o)
	if !ok {
		f.TrackingRuledOut = "no metric on this instance is busy enough to act as a witness, so we cannot tell a tracking break from a real drop"
		return
	}
	if w.after < witnessTolerance*w.before {
		f.TrackingRuledOut = fmt.Sprintf(
			"%s did stop — but %s fell %s over the same window (%.0f/day to %.0f/day), so the product changed, not the tracking",
			f.Event, w.label, pctFall(w.after, w.before), w.before, w.after)
		return
	}

	promoteTracking(f, w)
}

// promoteTracking rewrites the finding as instrumentation loss.
//
// FINGERPRINT NOTE, written down here because someone will otherwise discover it in production:
// the fingerprint is kind|event|day, so promotion CHANGES it. An acted-ledger entry recorded
// against the old "regression|checkout|2026-08-04" fingerprint does not carry over to
// "tracking_broke|checkout|2026-08-04". That is intended — it is a different claim about the
// world, and the cloud uses this same string as its branch name and its idempotency key — but it
// is a real consequence and it is deliberate, not an oversight.
func promoteTracking(f *Finding, w witness) {
	f.Kind = KindTrackingBroke
	f.SilentSince = f.Day
	f.TrackingRuledOut = ""
	f.Witness = fmt.Sprintf("%s held at %.0f/day (%s) across the same window", w.label, w.after, signedPct(w.after, w.before))
	f.Headline = fmt.Sprintf("%s stopped arriving on %s; the traffic that produces it did not stop", f.Event, f.Day)
	f.NextMove = fmt.Sprintf("find the commit that removed the track() call for %s and put it back", f.Event)
	// Never a people count. See BasisBlind.
	f.Cost = Cost{People: 0, Basis: BasisBlind}
	f.NeedsYou = true
	// Attribute() ran before this and may have narrowed the drop to a segment, which is still
	// true and still useful. It is only the UNATTRIBUTED boilerplate that must go: that sentence
	// tells the reader to record deploys, and recording deploys will not put a deleted line back.
	if !f.Attributed() {
		f.Cause = "the events stopped; the traffic did not — " + f.Witness
	}
	f.Fingerprint = string(f.Kind) + "|" + f.Event + "|" + f.Day
}

// witness is the metric that stayed up, with the numbers that say so.
type witness struct {
	label  string
	before float64
	after  float64
}

// witnessFor picks the metric whose survival proves the product did not change.
//
// Preferred: the page this event fires on. It is the tightest possible control — same users,
// same surface, same window — so "checkout_completed stopped but $pageview on /checkout did not"
// is nearly impossible to explain any way other than a deleted call.
//
// Fallback: everything else the instance tracks. Looser, but a whole product going quiet at once
// is a very different picture from one event going quiet, and that difference is what we need.
//
// Either way the chosen witness must itself be busy enough to mean something. A witness running
// at 2/day proves nothing about anything, and we say that instead of pretending otherwise.
func witnessFor(evs []event.Event, name string, day time.Time, o Opts) (witness, bool) {
	from := day.AddDate(0, 0, -witnessWindowDays)
	if p, ok := dominantPath(evs, name, from, day); ok {
		w := witness{
			label:  "$pageview on " + p,
			before: pathViewMean(evs, p, from, day),
			after:  pathViewMean(evs, p, day, o.Now),
		}
		// A page witness below the floor falls back to the instance-wide one rather than
		// refusing outright: the fallback is a legitimate witness in its own right, and refusing
		// while a perfectly good witness sits unused would be a silence we cannot justify.
		if w.before >= minDailyBefore {
			return w, true
		}
	}
	w := witness{
		label:  "every other tracked event",
		before: otherEventsMean(evs, name, from, day),
		after:  otherEventsMean(evs, name, day, o.Now),
	}
	if w.before < minDailyBefore {
		return witness{}, false
	}
	return w, true
}

// dominantPath reports the one page this event overwhelmingly fires on, if there is one.
func dominantPath(evs []event.Event, name string, from, to time.Time) (string, bool) {
	total, withPath := 0, 0
	counts := map[string]int{}
	for _, e := range evs {
		if e.Name != name || e.Timestamp.Before(from) || !e.Timestamp.Before(to) {
			continue
		}
		total++
		p := pathOf(e)
		if p == "" {
			continue
		}
		withPath++
		counts[p]++
	}
	if total == 0 || float64(withPath)/float64(total) < pathCoverage {
		return "", false
	}
	best, bestN := "", 0
	for p, n := range counts {
		if n > bestN || (n == bestN && p < best) {
			best, bestN = p, n
		}
	}
	if best == "" || float64(bestN)/float64(total) < pathDominance {
		return "", false
	}
	return best, true
}

// pathViewMean is the daily rate of autocaptured page views on one path.
func pathViewMean(evs []event.Event, path string, from, to time.Time) float64 {
	days := to.Sub(from).Hours() / 24
	if days <= 0 {
		return 0
	}
	n := 0
	for _, e := range evs {
		if e.Name != "$pageview" || e.Timestamp.Before(from) || !e.Timestamp.Before(to) {
			continue
		}
		if pathOf(e) == path {
			n++
		}
	}
	return float64(n) / days
}

// otherEventsMean is the daily rate of everything the instance records EXCEPT this event.
func otherEventsMean(evs []event.Event, name string, from, to time.Time) float64 {
	days := to.Sub(from).Hours() / 24
	if days <= 0 {
		return 0
	}
	n := 0
	for _, e := range evs {
		if e.Name == name || e.Timestamp.Before(from) || !e.Timestamp.Before(to) {
			continue
		}
		n++
	}
	return float64(n) / days
}

// quietFullDays counts the COMPLETED UTC days since the drop that sat at or below the floor.
// Today is excluded on purpose: a partial day always looks quiet, and counting it would let a
// finding promote itself several hours early every single morning.
func quietFullDays(evs []event.Event, name string, day, now time.Time, floor float64) int {
	counts := map[string]int{}
	for _, e := range evs {
		if e.Name != name {
			continue
		}
		t := e.Timestamp.UTC()
		if t.Before(day) || !t.Before(now) {
			continue
		}
		counts[t.Format("2006-01-02")]++
	}
	quiet := 0
	end := now.UTC().Truncate(24 * time.Hour)
	for d := day.Truncate(24 * time.Hour); d.Before(end); d = d.AddDate(0, 0, 1) {
		if float64(counts[d.Format("2006-01-02")]) <= floor {
			quiet++
		}
	}
	return quiet
}

// pathOf reads the page an event happened on, under our property name or any vendor's, and
// reduces a full URL to its path so a $current_url from an imported dataset compares equal to a
// path from ours.
func pathOf(e event.Event) string { return normalizePath(propValue(e, "path")) }

func normalizePath(v string) string {
	if v == "" {
		return ""
	}
	if i := strings.Index(v, "://"); i >= 0 {
		rest := v[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			v = rest[j:]
		} else {
			v = "/"
		}
	}
	if i := strings.IndexAny(v, "?#"); i >= 0 {
		v = v[:i]
	}
	return v
}

// signedPct renders the witness's move with its sign, so "held" is visibly a measurement.
func signedPct(after, before float64) string {
	if before <= 0 {
		return "no prior level"
	}
	return fmt.Sprintf("%+.0f%%", 100*(after-before)/before)
}

// pctFall renders how far the witness fell, for the rule-out sentence.
func pctFall(after, before float64) string {
	if before <= 0 {
		return "to nothing"
	}
	return fmt.Sprintf("%.0f%%", 100*(before-after)/before)
}
