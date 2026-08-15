package investigate

import (
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
)

// The backtest: what this would have told you, and when, over history you already remember.
//
// It is the cold open, and it exists because of a hard prior. Kohavi's measurement at Microsoft
// found only about a THIRD of tested ideas improve the metric they were built to improve; Google
// and Netflix report roughly one in ten. Pendo's telemetry across 615 subscriptions found 80% of
// features rarely or never used. So a replay over any real product's last quarter will find waste.
// It is the only demo in analytics that cannot come back empty.
//
// It also answers the question no dashboard can. A live dashboard says "here is your product".
// A backtest says "on the 14th of June this would have told you checkout was broken, and you
// found out on the 2nd of July" — a claim about events the reader personally lived through, which
// is why it does not need a reference customer to be believed.
//
// The mechanism is deliberately unglamorous: run the same Investigator that runs today, but with
// the clock moved back, once per step across the window, and keep the FIRST time each finding
// would have appeared. Opts.Now was injectable from the first commit, so this is a loop rather
// than a second implementation — which matters, because a backtest computed differently from the
// live product would be a sales artefact rather than a proof.

// Dated is one finding plus when the system would first have surfaced it.
type Dated struct {
	Finding
	// FirstSeen is the day the sweep would have reported it. The finding's own Day is when the
	// underlying change happened. The gap between them is the detection lag, and the gap between
	// FirstSeen and when the human actually noticed is the number that sells this.
	FirstSeen string `json:"first_seen"`
	// LagDays is FirstSeen minus the change day: how long the data took to become conclusive.
	// Reported honestly rather than hidden, because "we'd have told you the same day" is usually
	// false and claiming it would be the first thing a sceptical reader checks.
	LagDays int `json:"lag_days"`
}

// Replay is one backtest.
type Replay struct {
	From     string  `json:"from"`
	To       string  `json:"to"`
	StepDays int     `json:"step_days"`
	Sweeps   int     `json:"sweeps"`
	Findings []Dated `json:"findings"`
	// Scanned names the metrics the sweep looked at across the whole window, so an empty result
	// is distinguishable from a broken run — the same rule the live Brief follows.
	Scanned []string `json:"scanned"`
}

// BacktestOpts configures a replay.
type BacktestOpts struct {
	Opts
	// Days of history to replay. Default 90.
	WindowDays int
	// StepDays is how often the sweep is re-run inside the window. Default 1.
	//
	// One day is the honest default: the product runs daily, so a replay that only checks weekly
	// would understate how early it would have caught something, and understating is the wrong
	// direction to be wrong in a sales artefact. Raise it on a very large log.
	StepDays int
	// Deploys let a replayed finding name the ship that caused it, exactly as the live
	// investigation does. Optional: without them the replay still reports what changed, it just
	// cannot attribute it, which is the same degradation the live path has.
	Deploys []deploys.Deploy
}

// Backtest replays the investigation across history and returns each finding dated by when it
// would first have been reported.
//
// Deduped on (kind, event, change-day) keeping the EARLIEST sweep that saw it. A regression
// detected on forty consecutive daily sweeps is one finding you would have been told about once,
// not forty — reporting it forty times would be the same "cacophony" failure the live Brief was
// built to avoid, just spread over time.
func Backtest(evs []event.Event, flags []flag.Flag, o BacktestOpts) Replay {
	base := o.Opts.withDefaults()
	if o.WindowDays <= 0 {
		o.WindowDays = 90
	}
	if o.StepDays <= 0 {
		o.StepDays = 1
	}

	end := base.Now
	start := end.AddDate(0, 0, -o.WindowDays)
	rep := Replay{
		From:     start.Format("2006-01-02"),
		To:       end.Format("2006-01-02"),
		StepDays: o.StepDays,
	}

	type key struct {
		kind  Kind
		event string
		day   string
	}
	first := map[key]Dated{}
	seenScanned := map[string]bool{}

	for at := start; !at.After(end); at = at.AddDate(0, 0, o.StepDays) {
		rep.Sweeps++
		// The sweep sees only what had HAPPENED by `at`. Handing it the whole log would let it
		// detect a change using data from the future, which would make every lag number a lie and
		// the whole artefact worthless.
		past := before(evs, at)
		if len(past) == 0 {
			continue
		}
		step := o.Opts
		step.Now = at
		inv := WithContext(past, flagsBefore(flags, at), deploysBefore(o.Deploys, at), step)

		for _, s := range inv.Scanned {
			seenScanned[s] = true
		}
		for _, f := range inv.Findings {
			k := key{f.Kind, f.Event, f.Day}
			if _, ok := first[k]; ok {
				continue // already reported, earlier
			}
			d := Dated{Finding: f, FirstSeen: at.Format("2006-01-02")}
			if day, err := time.Parse("2006-01-02", f.Day); err == nil {
				d.LagDays = int(at.Sub(day).Hours() / 24)
			}
			first[k] = d
		}
	}

	for _, d := range first {
		rep.Findings = append(rep.Findings, d)
	}
	// Chronological, because the artefact is read as a story: this is what you would have been
	// told, in the order you would have been told it.
	sort.Slice(rep.Findings, func(i, j int) bool {
		if rep.Findings[i].FirstSeen != rep.Findings[j].FirstSeen {
			return rep.Findings[i].FirstSeen < rep.Findings[j].FirstSeen
		}
		return rep.Findings[i].Cost.People > rep.Findings[j].Cost.People
	})
	for s := range seenScanned {
		rep.Scanned = append(rep.Scanned, s)
	}
	sort.Strings(rep.Scanned)
	return rep
}

// before returns the events that had happened by `at`.
func before(evs []event.Event, at time.Time) []event.Event {
	out := make([]event.Event, 0, len(evs))
	for _, e := range evs {
		if e.Timestamp.Before(at) {
			out = append(out, e)
		}
	}
	return out
}

// deploysBefore drops ships that had not happened by `at`. Without this the replay could
// attribute a June regression to a July release — the finding would look sharper and be a lie,
// and a sales artefact caught inventing one attribution loses every other line with it.
func deploysBefore(deps []deploys.Deploy, at time.Time) []deploys.Deploy {
	out := make([]deploys.Deploy, 0, len(deps))
	for _, d := range deps {
		if d.At.Before(at) {
			out = append(out, d)
		}
	}
	return out
}

// flagsBefore drops experiments that had not started by `at`, so the kill list cannot name a ship
// that had not happened yet.
func flagsBefore(flags []flag.Flag, at time.Time) []flag.Flag {
	out := make([]flag.Flag, 0, len(flags))
	for _, f := range flags {
		if f.Experiment != nil && !f.Experiment.Started.IsZero() && f.Experiment.Started.After(at) {
			continue
		}
		out = append(out, f)
	}
	return out
}
