package connector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/investigate"
)

// Running the investigator against someone else's analytics tool.
//
// Two passes, because the two queries have opposite economics. Detection is constant and cheap:
// one aggregate over the whole window, every sync. Attribution is rare and expensive: it needs a
// dimension breakdown of ONE event over ±7 days, and it is only worth a query for an event that
// actually changed. A product with nothing wrong costs one query. A product with three findings
// costs seven.
//
// Doing it in one pass would mean grouping every event by every dimension across ninety days —
// the row explosion that makes people believe an aggregate read cannot work.

// Fetcher is what a vendor must implement. Two methods, both aggregate, neither an export.
type Fetcher interface {
	// Vendor names the tool, for the Snapshot and for error text.
	Vendor() string
	// PhaseA returns daily counts per event across [from, to). One query.
	PhaseA(ctx context.Context, from, to time.Time) ([]Bucket, error)
	// PhaseB returns the dimension marginals and money counters for ONE event.
	//
	// segFrom/segTo bound the breakdown (the ±7 days worstSegment examines). revFrom/revTo bound
	// the money figures, and they are the investigator's REPORTING window, which is wider. Two
	// windows in one call because they are two parts of one answer about one event, and splitting
	// them across two calls doubles the query count for no gain.
	PhaseB(ctx context.Context, name string, segFrom, segTo, revFrom, revTo time.Time, dims []string) (map[time.Time]map[string]map[string]int, []string, Revenue, error)
}

// Result is one investigation of a connected tool: the findings, and every claim this source
// could not support.
//
// Degradations are a FIELD, not a log line. The rule the package is judged by is that a finding
// from aggregates either equals the finding from raw events or is visibly labelled — and "visibly"
// means it travels in the same struct the findings do, to every renderer, in the same response.
// A degradation that has to be looked up somewhere else is a degradation nobody sees.
type Result struct {
	investigate.Investigation
	Snapshot     Snapshot      `json:"-"`
	Degradations []Degradation `json:"degradations,omitempty"`
	// CheckedDims names, per finding event, the dimensions the cause line was actually derived
	// from. worstSegment's fallback sentence says a change is "spread evenly across browsers,
	// devices and pages" — a sentence about dimensions, which on this path is only true of the
	// dimensions we asked for. This is what lets a renderer state the real list.
	CheckedDims map[string][]string `json:"checked_dims,omitempty"`
}

// InvestigateOpts configures a connected pass.
type InvestigateOpts struct {
	Now  time.Time
	Days int // the investigator's reporting window. Default 30.
	// Dims is the vendor-side dimension list Phase B asks for. Default CauseDims.
	Dims []string
	// MinPeople is passed straight through to the investigator.
	MinPeople int
}

func (o InvestigateOpts) withDefaults() InvestigateOpts {
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	}
	if o.Days <= 0 {
		o.Days = 30
	}
	if o.Dims == nil {
		o.Dims = CauseDims
	}
	return o
}

// Investigate runs the two-phase read and returns findings with their honest edges attached.
//
// held is the buckets already synced (see sync.go). Passing them rather than re-fetching is the
// point of keeping a watermark: the ticker reads the last few days, Merge folds them in, and the
// investigator sees ninety days of history for the cost of three.
func Investigate(ctx context.Context, f Fetcher, held []Bucket, opts InvestigateOpts) (Result, error) {
	o := opts.withDefaults()
	to := o.Now.UTC()
	from := to.AddDate(0, 0, -o.Days)

	s := Snapshot{
		Vendor: f.Vendor(), From: from, To: to,
		// Trimmed to the reporting window on purpose. The sync holds ninety days; the investigator
		// reads thirty. Synthesising the other sixty would put rows in front of
		// money.revenuePropFor — which scans every event of a name, unwindowed — that the vendor's
		// money counters were never computed over, and the property-quality tally would describe a
		// different population than the sum does.
		Buckets:         Trim(held, from, to),
		Revenue:         map[string]Revenue{},
		InvestigateDays: o.Days,
		Dims:            map[string][]string{},
		DroppedDims:     map[string][]string{},
		FetchedAt:       o.Now,
	}

	// PASS 1 — detection only. No dimensions, no money: nothing here can produce a cause line, and
	// the cause lines this pass would write are thrown away.
	evs, degs := Synthesize(s)
	first := investigate.Run(evs, investigate.Opts{Days: o.Days, Now: o.Now, MinPeople: o.MinPeople})

	// PASS 2 — attribution, for the events that actually changed.
	type target struct {
		name string
		day  time.Time
	}
	var targets []target
	seen := map[string]bool{}
	for _, fd := range first.Findings {
		if fd.Event == "" || fd.Day == "" || seen[fd.Event] {
			continue
		}
		day, err := time.Parse(time.DateOnly, fd.Day)
		if err != nil {
			continue
		}
		seen[fd.Event] = true
		targets = append(targets, target{fd.Event, day})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].name < targets[j].name })

	for _, t := range targets {
		segFrom, segTo := Window(t.day)
		segs, dropped, rev, err := f.PhaseB(ctx, t.name, segFrom, segTo, from, to, o.Dims)
		if err != nil {
			// A failed breakdown must not silently become "spread evenly across browsers, devices
			// and pages" — that sentence is a measurement, and we did not take it. The finding
			// keeps its detection, loses its cause, and says why.
			degs = append(degs, Degradation{
				Code: "segment_unavailable",
				What: fmt.Sprintf("%s was not broken down by any dimension", t.name),
				Why:  fmt.Sprintf("the breakdown query against %s failed: %v.", f.Vendor(), err),
				Fix:  "The size of the change is exact regardless; retry the sync for the cause line.",
			})
			continue
		}
		AttachSegments(&s, t.name, segs)
		var kept []string
		for _, d := range o.Dims {
			if !contains(dropped, d) {
				kept = append(kept, d)
			}
		}
		s.Dims[t.name] = kept
		if len(dropped) > 0 {
			s.DroppedDims[t.name] = dropped
			degs = append(degs, DegradeDims(t.name, dropped))
		}
		if rev.Usable() {
			s.Revenue[t.name] = rev
		} else if rev.Prop != "" {
			degs = append(degs, degradeRevenue(t.name, rev))
		}
	}

	evs, more := Synthesize(s)
	degs = append(degs, more...)
	inv := investigate.Run(evs, investigate.Opts{Days: o.Days, Now: o.Now, MinPeople: o.MinPeople})

	// STRIP WHAT THIS SOURCE CANNOT KNOW, rather than let it render.
	//
	// investigate.Movements is a per-person rate: the share of people active in each half of the
	// window who did the event. Synthesised rows carry one person id each, so it would compute
	// cleanly, print confidently, and be nonsense — the single most dangerous shape a wrong number
	// can take. Blanking it and saying so is the only honest option available from daily counts.
	inv.Movements = nil
	degs = append(degs, DegradeSequence, DegradePeople)

	res := Result{Investigation: inv, Snapshot: s, Degradations: dedupe(degs)}
	if len(s.Dims) > 0 {
		res.CheckedDims = map[string][]string{}
		for name, dims := range s.Dims {
			local := make([]string, 0, len(dims))
			for _, d := range dims {
				if n := LocalDim[d]; n != "" {
					local = append(local, n)
				} else {
					local = append(local, strings.TrimPrefix(d, "$"))
				}
			}
			res.CheckedDims[name] = local
		}
	}
	return res, nil
}

// degradeRevenue says why a metric that LOOKS priced is still sized in people.
//
// Without this the product is silently inconsistent: the same event is sized in dollars on an
// instance with 20 sales and in people on one with 19, and the reader has no way to tell that a
// threshold rather than a missing property is the reason.
func degradeRevenue(name string, r Revenue) Degradation {
	why := fmt.Sprintf("only %d priced rows of %s in the window; a mean order value needs at least %d before it is worth stating.",
		r.Rows, r.Prop, minRevenueEvents)
	if r.Seen > 0 && float64(r.Numeric)/float64(r.Seen) < minNumericRatio {
		why = fmt.Sprintf("%d of %d %s values are not numbers, so a mean over the rest would describe a subset while reading as the whole.",
			r.Seen-r.Numeric, r.Seen, r.Prop)
	}
	return Degradation{
		Code: "revenue_floor",
		What: fmt.Sprintf("%s is sized in people affected, not money", name),
		Why:  why,
		Fix:  "Send a numeric amount on this event and the figure becomes money on the next sync.",
	}
}

// dedupe collapses identical degradations. Phase B runs per event and the standing ones are
// appended unconditionally, so the same sentence can arrive several times; printing it twice reads
// as two problems.
func dedupe(in []Degradation) []Degradation {
	seen := map[string]bool{}
	out := make([]Degradation, 0, len(in))
	for _, d := range in {
		k := d.Code + "\x00" + d.What
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, d)
	}
	return out
}
