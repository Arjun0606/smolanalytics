package api

import (
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/aicrawl"
	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The crawler card: which AI crawlers have actually fetched this site, from the customer's
// own server. Its own pane rather than a section inside AI visibility, because its empty
// state is a DIFFERENT question with a different fix — "we sample the engines for you" vs
// "your server has to tell us, here is the one-liner" — and folding two unrelated
// call-to-actions into one card is how a person ends up staring at instructions for a thing
// they did not ask about.
type aicrawlVM struct {
	aicrawl.Result
	// Ever reads the store's event NAMES, not this window: "nothing installed" and
	// "installed, nothing crawled you lately" look identical in an empty window and need
	// opposite responses from the reader.
	Ever bool
	// Scoped means a site/env chip is narrowing the page. $ai_crawl carries a site
	// property so it CAN be scoped legitimately, but an empty card under a filter still
	// needs to say which of the two things happened.
	Scoped   bool
	WidenURL string
	Managed  bool
	CloudURL string
	// Bars is the day trend, pre-scaled to a percentage height so the template does no
	// arithmetic. Same reason every other chart here is computed server-side.
	Bars []crawlBar
	// Peak is the tallest day, printed as the axis label so the bars have a scale.
	Peak int
	// FirstDay/LastDay label the ends of the trend. Precomputed because the template has
	// no arithmetic helper, and adding one just to index the last element would be a new
	// public surface for every future template to misuse.
	FirstDay, LastDay string
	// UserPct is the share of fetches that were an assistant answering a person, rather than
	// a training or indexing sweep. The single most valuable number in the module and the one
	// every "bot traffic" report buries.
	UserPct int
	// Crawlers shadows Result.Crawlers with rows that carry their own recency. The embedded
	// row has LastAt and the table never rendered it; see crawlerRow.
	Crawlers []crawlerRow
}

// crawlerRow is one crawler row with the two things the table was missing: WHEN it last
// fetched, and whether "now" is a word we are entitled to use about it.
//
// The table printed "answering someone now" for every user-purpose crawler unconditionally, in
// the accent colour, next to a column headed "last seen" that rendered the last PATH. So
// ChatGPT-User read as a live assistant mid-answer for as long as the pane was open — a day
// later, a week later — and there was no timestamp anywhere on the row to contradict it. The
// data had LastAt the whole time; nothing rendered it.
type crawlerRow struct {
	aicrawl.CrawlerRow
	// Live is the ONLY thing that licenses present-tense copy: a user-initiated fetch inside
	// the last liveWindow. Everything else describes what the crawler is FOR.
	Live bool
	// LastAgo is "4m" / "20h" / "3d", empty when the crawler has never been seen.
	LastAgo string
}

// liveWindow is how recent a user-initiated fetch has to be before the row may say "now".
// ChatGPT-User fetches a page while a person waits for an answer, so the interesting question
// is genuinely "is that happening right now" — but a quarter of an hour is the outside edge of
// what any reader would accept as "now", and past it the honest word is "last".
const liveWindow = 15 * time.Minute

// PurposeText is the sentence for the why column. Present tense ONLY when Live.
func (c crawlerRow) PurposeText() string {
	switch c.Purpose {
	case "user":
		if c.Live {
			return "answering someone now"
		}
		return "answers when someone asks"
	case "search":
		return "indexing for answers"
	default:
		return "training"
	}
}

// agoShort renders a duration the way a table column can hold it.
func agoShort(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return itoaInt(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return itoaInt(int(d.Hours())) + "h ago"
	default:
		return itoaInt(int(d.Hours()/24)) + "d ago"
	}
}

func itoaInt(n int) string { return strconv.Itoa(n) }

// crawlBar is one day of the crawl trend, split by purpose.
type crawlBar struct {
	Day      string
	Label    string
	Hits     int
	Training int
	Search   int
	User     int
	// Three stacked heights, each a percentage of the PEAK day so the columns are
	// comparable across the window. Split rather than one total bar because the whole
	// argument of this card is that a training sweep and a live assistant fetch are not
	// the same event, and a single-colour bar would flatten exactly that.
	UserPct     int
	SearchPct   int
	TrainingPct int
	Pct         int // total height, for the tooltip-free eyeball read
	Today       bool
}

// buildAICrawl renders the card over THIS PAGE'S window and filters — same evs, days and
// asof as every other pane, so it can never quietly answer over a wider range than the
// toolbar advertises.
func buildAICrawl(vm *dashVM, evs []event.Event, names []string, days int, asof time.Time, scoped bool, widenURL, cloudURL string) {
	ac := aicrawlVM{
		Result:   aicrawl.Compute(evs, days, asof),
		Ever:     hasName(names, aicrawl.CrawlEvent),
		Scoped:   scoped,
		WidenURL: widenURL,
		Managed:  cloudURL != "",
		CloudURL: cloudURL,
	}
	end := asof
	if end.IsZero() {
		end = time.Now().UTC()
	}
	today := end.UTC().Format("2006-01-02")

	// Recency per row, computed against the SAME `end` every other number on this pane uses —
	// not time.Now(), or a historical range would report crawlers as live because the wall
	// clock has moved on since the window closed.
	for _, r := range ac.Result.Crawlers {
		row := crawlerRow{CrawlerRow: r}
		if t, err := time.Parse(time.RFC3339, r.LastAt); err == nil {
			if d := end.Sub(t.UTC()); d >= 0 {
				row.LastAgo = agoShort(d)
				row.Live = r.Purpose == "user" && d < liveWindow
			}
		}
		ac.Crawlers = append(ac.Crawlers, row)
	}

	for _, d := range ac.Trend {
		if d.Hits > ac.Peak {
			ac.Peak = d.Hits
		}
	}
	for _, d := range ac.Trend {
		b := crawlBar{Day: d.Day, Hits: d.Hits, Training: d.Training, Search: d.Search, User: d.User, Today: d.Day == today}
		if t, err := time.Parse("2006-01-02", d.Day); err == nil {
			b.Label = t.Format("Jan 2")
		} else {
			b.Label = d.Day
		}
		if ac.Peak > 0 {
			b.Pct = d.Hits * 100 / ac.Peak
			b.UserPct = d.User * 100 / ac.Peak
			b.SearchPct = d.Search * 100 / ac.Peak
			b.TrainingPct = d.Training * 100 / ac.Peak
		}
		ac.Bars = append(ac.Bars, b)
	}
	if len(ac.Bars) > 0 {
		ac.FirstDay, ac.LastDay = ac.Bars[0].Label, ac.Bars[len(ac.Bars)-1].Label
	}
	if ac.Hits > 0 {
		ac.UserPct = ac.User * 100 / ac.Hits
	}
	vm.AICrawl = ac
}
