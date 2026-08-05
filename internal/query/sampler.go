package query

import "github.com/Arjun0606/smolanalytics/internal/event"

// SamplerEvent names the events THIS TOOL writes into the operator's own instance: the GEO
// runner's AI-visibility checks, its site-readability scans, and the server-side AI-crawler
// records. They are our robot, not the product's users.
//
// Left in a product-activity report they are actively wrong, not merely noisy. They arrive
// under one synthetic distinct_id on a daily schedule, so that identity reads as a user who
// returns every single day — it inflates retention, it inflates MAU, it lands in lifecycle as a
// permanently-loyal cohort member, and it competes for the anomaly slot with an event name that
// has no reader-facing wording at all.
//
// This lived as a private helper inside internal/insight, which is why the verdict card was the
// ONLY surface that excluded them. Measured on a live instance: the verdict read "Day-1
// retention 4% of 114 users" while the ask bar, three inches above it on the same page at the
// same moment, answered "Day-1 retention is 6% ... of 119 users". Same metric, same instant, two
// numbers — on the product whose footer promises "ask == dashboard == MCP, all from one report
// engine". One exported definition now, so a surface can only diverge on purpose.
//
// The names are literals rather than imports of aivis.CheckEvent / insight.ReadableEvent /
// aicrawl.CrawlEvent because query sits underneath all three. TestSamplerNamesMatchTheirSources
// in internal/insight holds the two ends together.
var SamplerEvent = map[string]bool{
	"$geo_check":     true, // aivis.CheckEvent      — an AI-visibility sample run
	"$site_readable": true, // insight.ReadableEvent — a render/robots readability scan
	"$ai_crawl":      true, // aicrawl.CrawlEvent    — a server-side crawler fetch record
}

// IsSampler reports whether an event name is one this tool wrote about itself.
func IsSampler(name string) bool { return SamplerEvent[name] }

// WithoutSampler drops this tool's own writes from a product-activity view.
//
// Use it for every report that answers "what did USERS do" — retention, stickiness, lifecycle,
// active users, paths, sessions, anomalies. Do NOT use it for the AI-visibility and AI-crawler
// reports: those events are their INPUT, and filtering them there empties the pane.
//
// Returns the input untouched when there are none, which is every instance that never turned
// GEO on — so the common path allocates nothing.
func WithoutSampler(evs []event.Event) []event.Event {
	n := 0
	for _, e := range evs {
		if SamplerEvent[e.Name] {
			n++
		}
	}
	if n == 0 {
		return evs
	}
	out := make([]event.Event, 0, len(evs)-n)
	for _, e := range evs {
		if !SamplerEvent[e.Name] {
			out = append(out, e)
		}
	}
	return out
}
