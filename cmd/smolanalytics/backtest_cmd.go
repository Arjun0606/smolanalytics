package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	alias2 "github.com/Arjun0606/smolanalytics/internal/alias"
	"github.com/Arjun0606/smolanalytics/internal/deploys"
	flagpkg "github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
	"github.com/Arjun0606/smolanalytics/internal/store"
)

// `smolanalytics backtest` — what this would have told you, over history you already lived through.
//
// The point of this command is not the report. It is the QUESTION it lets you ask a real person:
// for each line, did you know, did you not know, or is it wrong. Three answers, ten products, and
// you find out in an afternoon whether the thing is worth building the rest of.
//
// Under 30% "didn't know" and it is a fancy report. That is a cheap thing to learn now and an
// expensive thing to learn in three months.
func backtestCmd(args []string) {
	fs := flag.NewFlagSet("backtest", flag.ExitOnError)
	days := fs.Int("days", 90, "how far back to replay")
	step := fs.Int("step", 1, "days between sweeps; raise it on a very large log")
	asJSON := fs.Bool("json", false, "emit the replay as JSON")
	_ = fs.Parse(args)
	if *days <= 0 || *step <= 0 {
		log.Fatal("backtest: --days and --step must be at least 1")
	}

	st, closeStore, err := openServeStore()
	if err != nil {
		log.Fatal(err)
	}
	var rd store.Store = st
	if am, aerr := alias2.Open(dataPath() + ".aliases.json"); aerr == nil {
		rd = alias2.Wrap(st, am)
	}
	evs, err := rd.Range(time.Time{}, time.Time{})
	_ = closeStore()
	if err != nil {
		log.Fatal(err)
	}
	if len(evs) == 0 {
		fmt.Println("no events yet, so there is nothing to replay")
		return
	}

	var flags []flagpkg.Flag
	if fstore, ferr := flagpkg.Open(dataPath() + ".flags.json"); ferr == nil {
		flags = fstore.List()
	}
	// Deploys let a replayed finding name the ship behind it. Optional, like everywhere else:
	// without them the replay still says what changed, it just cannot say what did it.
	var deps []deploys.Deploy
	if dp, derr := deploys.Open(dataPath() + ".deploys.json"); derr == nil {
		deps = dp.List()
	}

	rep := investigate.Backtest(evs, flags, investigate.BacktestOpts{
		Opts:       investigate.Opts{Now: time.Now().UTC()},
		WindowDays: *days,
		StepDays:   *step,
		Deploys:    deps,
	})

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return
	}
	fmt.Print(formatReplay(rep))
}

// formatReplay writes the artefact a person reads out loud to someone else.
//
// Every line carries the date it WOULD have arrived, because that is the whole claim. Without it
// this is one more report about the past, and the reader has no way to tell whether they were
// told late or told nothing.
func formatReplay(r investigate.Replay) string {
	s := fmt.Sprintf("what smolanalytics would have told you, %s to %s\n", r.From, r.To)
	s += fmt.Sprintf("%d sweeps, %d metric(s) watched\n\n", r.Sweeps, len(r.Scanned))

	if len(r.Findings) == 0 {
		// An honest empty is a real answer and has to read like one. A backtest that manufactures
		// findings to look impressive is worth less than one that comes back empty, because the
		// first thing a sceptic does is check a line.
		s += "nothing. across the whole window there was no step change big enough to be worth\n"
		s += "telling you about, and nothing shipped that had the traffic to answer and did not.\n"
		if len(r.Scanned) > 0 {
			s += fmt.Sprintf("watched: %v\n", r.Scanned)
		}
		return s
	}

	for _, f := range r.Findings {
		s += fmt.Sprintf("%s   %s\n", f.FirstSeen, f.Headline)
		if sz := f.Cost.Size(); sz != "" {
			s += "             " + sz
			if f.LagDays > 0 {
				s += fmt.Sprintf(" · %d day(s) after it started", f.LagDays)
			}
			s += "\n"
		}
		if f.Cause != "" {
			s += fmt.Sprintf("             %s\n", f.Cause)
		}
		s += "\n"
	}
	s += "for each line: did you know, did you not know, or is it wrong?\n"
	return s
}
