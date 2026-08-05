package fixbrief

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/web"
)

// convWindow is the funnel window the verdict engine uses. Hard-coded on purpose: the brief must
// describe the same funnel the finding was detected over, not a window a caller chose.
const convWindow = 7 * 24 * time.Hour

// whereDays is the window for the "where it shows up" page list. 30 days, matching the web
// overview the dashboard renders, so the pages named here are the pages the operator can see.
const whereDays = 30

// Evidence is one computed fact plus the two ways to re-derive it. Report is a real path on this
// instance (the dashboard renders it as a link — clicking it IS the audit), Tool/Args are the
// same question through MCP.
type Evidence struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	N      int    `json:"n,omitempty"` // the base behind a rate; 0 when the value is a raw count
	Report string `json:"report"`
	Tool   string `json:"tool"`
	Args   string `json:"args"`
}

// Where is a MEASURED location, not a guess about a repo. The engine has never seen the code and
// must never pretend otherwise: it names the pages, and the agent maps pages to files.
type Where struct {
	Path  string `json:"path"`
	Users int    `json:"users"`
	Why   string `json:"why"`
}

// Acceptance is the falsifiable half of the brief. Without it "fix with your agent" is a wish.
type Acceptance struct {
	Metric    string `json:"metric"`
	Current   string `json:"current"`
	Direction string `json:"direction"`
	Window    string `json:"window"`
	Report    string `json:"report"`
	Tool      string `json:"tool"`
	Args      string `json:"args"`
}

// Brief is the whole artifact. Prompt is the paste-anywhere rendering of every other field —
// derived, never independently written, so the copy block cannot say something the structured
// fields do not. Ask is the short form for a deeplink, where 8k of URL is the ceiling.
type Brief struct {
	Fingerprint string          `json:"fingerprint"`
	Finding     insight.Finding `json:"finding"`
	ComputedBy  string          `json:"computed_by"`
	Evidence    []Evidence      `json:"evidence"`
	Where       []Where         `json:"where,omitempty"`
	Do          []string        `json:"do"`
	Acceptance  Acceptance      `json:"acceptance"`
	Note        string          `json:"note,omitempty"`
	Prompt      string          `json:"prompt"`
	Ask         string          `json:"ask"`
}

// Item is one line of the picker: enough to choose a finding, not enough to act on it.
type Item struct {
	Fingerprint string `json:"fingerprint"`
	Severity    string `json:"severity"`
	Title       string `json:"title"`
}

// Result always lists what IS available, so a caller that asked for a finding that has since
// stopped being true gets a real next step instead of an empty object.
type Result struct {
	Available []Item `json:"available"`
	Brief     *Brief `json:"brief"`
	Note      string `json:"note,omitempty"`
}

// ComputeAll builds a brief for every finding in ONE pass. The dashboard renders all of them (the
// sheet has to open instantly — a spinner on the verdict's second action would make the most
// important control on the page feel broken), and computing them one at a time would re-run the
// funnel and the whole web overview once per card on every page load.
func ComputeAll(evs []event.Event, steps []funnel.Step, asof time.Time) []Brief {
	if asof.IsZero() {
		asof = time.Now().UTC()
	}
	findings := insight.GenerateForFunnel(evs, steps)
	if len(findings) == 0 {
		return nil
	}
	c := &ctx{evs: evs, steps: steps, asof: asof, funnels: map[string]funnel.Result{}}
	out := make([]Brief, 0, len(findings))
	for _, f := range findings {
		out = append(out, c.build(f))
	}
	return out
}

// Compute is the single-finding surface behind GET /v1/fix-brief and the fix_brief MCP tool.
// `want` matches a fingerprint or a title (case-insensitive); empty picks the lead finding, which
// is findings[0] because GenerateForFunnel already sorts warnings first.
func Compute(evs []event.Event, steps []funnel.Step, want string, asof time.Time) Result {
	briefs := ComputeAll(evs, steps, asof)
	res := Result{Available: []Item{}}
	for _, b := range briefs {
		res.Available = append(res.Available, Item{Fingerprint: b.Fingerprint, Severity: b.Finding.Severity, Title: b.Finding.Title})
	}
	if len(briefs) == 0 {
		res.Note = "the verdict is empty right now: nothing is moving enough to brief an agent about. That is a real answer, not a failure — send more traffic, or ask for a specific report instead."
		return res
	}
	if want = strings.TrimSpace(want); want == "" {
		b := briefs[0]
		res.Brief = &b
		return res
	}
	for i := range briefs {
		if briefs[i].Fingerprint == want || strings.EqualFold(strings.TrimSpace(briefs[i].Finding.Title), want) {
			b := briefs[i]
			res.Brief = &b
			return res
		}
	}
	// Never substitute a different finding for the one that was asked for. A brief handed to an
	// agent under the wrong title is how someone ships a change for a problem they do not have.
	res.Note = fmt.Sprintf("no finding matching %q is in the verdict right now — findings appear and disappear as the numbers move, so a link from an old email can outlive one. Pick from the %d in `available`.", want, len(briefs))
	return res
}

// ctx memoizes the expensive recomputes across the findings in one pass.
type ctx struct {
	evs     []event.Event
	steps   []funnel.Step
	asof    time.Time
	funnels map[string]funnel.Result
	webRes  *web.Result
}

func (c *ctx) web() web.Result {
	if c.webRes == nil {
		r := web.Compute(c.evs, whereDays, c.asof)
		c.webRes = &r
	}
	return *c.webRes
}

func (c *ctx) funnel(names []string) funnel.Result {
	key := strings.Join(names, "\x00")
	if r, ok := c.funnels[key]; ok {
		return r
	}
	steps := make([]funnel.Step, len(names))
	for i, n := range names {
		steps[i] = funnel.Step{Event: n}
	}
	r := funnel.Compute(c.evs, steps, convWindow)
	c.funnels[key] = r
	return r
}

// stepNames resolves the funnel a finding was computed over: the finding's own definition first
// (it carries it), then the caller's page funnel. A brief without a funnel definition is one an
// agent cannot reproduce, which defeats the point.
func (c *ctx) stepNames(f insight.Finding) []string {
	if len(f.Steps) >= 2 {
		return f.Steps
	}
	if len(c.steps) >= 2 {
		out := make([]string, len(c.steps))
		for i, s := range c.steps {
			out[i] = s.Event
		}
		return out
	}
	return nil
}

func (c *ctx) build(f insight.Finding) Brief {
	b := Brief{Fingerprint: f.Fingerprint(), Finding: f, Note: lowN(f)}
	switch f.Kind {
	case insight.KindDropoff, insight.KindSegment:
		names := c.stepNames(f)
		b.Evidence = c.funnelEvidence(names, f)
		b.Where = c.pages(c.web().ExitPages, "sessions end here (last pageview in the visit, last 30 days)")
		b.Acceptance = funnelAcceptance(names, f)
		b.ComputedBy = "the funnel report over the last 7 days, the same computation the dashboard's funnel pane and GET /v1/funnel run. Not a model's reading of it."
		b.Do = []string{
			fmt.Sprintf("Read the code behind the leaking step (%s → %s) and behind the pages listed above. Do not propose anything before you have read it.", human(f.From), human(f.To)),
			"Form ONE hypothesis for why people stop there, and name the evidence row it rests on.",
			"Make the SMALLEST change consistent with that hypothesis: copy, a CTA, a dead-end link, a missing empty state, a broken flow. No redesigns, no refactors, no new dependencies.",
			"Show me the diff and the reasoning before you apply it. Apply only after I agree.",
			"If nothing you read plausibly explains the number, say so and stop. Declining is a correct answer and a much better one than a change that looks useful and is not.",
		}
	case insight.KindAnomaly, insight.KindTrend:
		rep, args := trendReport(f.Metric)
		val := fmt.Sprintf("%+d%%", f.Rate)
		if f.Kind == insight.KindAnomaly {
			val += fmt.Sprintf(" against the trailing 7-day daily mean (%d events of baseline)", f.N)
		} else {
			val += fmt.Sprintf(" week-over-week (%d in the prior week)", f.N)
		}
		b.Evidence = []Evidence{{Label: f.Metric + " volume", Value: val, N: f.N, Report: rep, Tool: "trends", Args: args}}
		b.Where = c.pages(c.web().TopPages, "the pages that carry most of this traffic (last 30 days)")
		b.Acceptance = Acceptance{
			Metric: f.Metric + " volume", Current: val, Direction: "back to its baseline",
			Window: "48 hours", Report: rep, Tool: "trends", Args: args,
		}
		b.ComputedBy = "the verdict engine's change detector (the last 24h, and the last 7 days, against this event's own trailing baseline), the same computation GET /v1/notable returns."
		b.Do = []string{
			"FIRST decide whether TRACKING broke or the PRODUCT did. If the events simply stopped arriving, this is instrumentation and no product change will fix it — the instrumentation_health tool (or the events pane) answers that in one call.",
			"Only if tracking is healthy: find the code path that emits this event and what changed around it (a recent deploy, a release, a flag flip).",
			"Propose the SMALLEST change that explains the move. Show me the diff and the reasoning first.",
			"If nothing you read explains it, say so and stop rather than inventing a cause.",
		}
	case insight.KindRetention:
		rep, args := "/v1/retention?days=7", jsonArgs(map[string]any{"days": 7})
		b.Evidence = []Evidence{{
			Label: "day-1 retention (any activity counts as returning)", Value: fmt.Sprintf("%d%%", f.Rate), N: f.N,
			Report: rep, Tool: "retention", Args: args,
		}}
		b.Where = c.pages(c.web().EntryPages, "where visits START — retention is won or lost in the first session")
		b.Acceptance = Acceptance{
			Metric: "day-1 retention", Current: fmt.Sprintf("%d%%", f.Rate), Direction: "up",
			Window: "14 days (cohorts need time before day 1 is even observable)", Report: rep, Tool: "retention", Args: args,
		}
		b.ComputedBy = "the retention report over cohorts old enough to have observed day 1, the same computation the dashboard's retention grid renders."
		b.Do = []string{
			"Read the first-run path: the entry pages above, onboarding, and whatever the product asks for before it gives anything back.",
			"Find the one step that costs the most before any value lands, and propose removing or deferring it. One change, not a redesign.",
			"Show me the diff and the reasoning first.",
			"Retention moves slowly. Do not expect the number to answer you tomorrow, and do not claim it did.",
		}
	default:
		rep, args := "/v1/notable", jsonArgs(map[string]any{})
		b.Evidence = []Evidence{{Label: f.Title, Value: f.Detail, N: f.N, Report: rep, Tool: "whats_notable", Args: args}}
		b.Acceptance = Acceptance{
			Metric: "this finding", Current: "present in the verdict", Direction: "gone from the verdict",
			Window: "7 days", Report: rep, Tool: "whats_notable", Args: args,
		}
		b.ComputedBy = "the verdict engine, the same computation GET /v1/notable returns."
		b.Do = []string{
			"Pull the reports this finding names and read the numbers yourself before proposing anything.",
			"Propose the smallest change the evidence supports, and show me the diff first.",
		}
	}
	b.Prompt = renderPrompt(b)
	b.Ask = renderAsk(b)
	return b
}

func (c *ctx) funnelEvidence(names []string, f insight.Finding) []Evidence {
	if len(names) < 2 {
		return nil
	}
	rep, args := funnelReport(names, "")
	fr := c.funnel(names)
	out := make([]Evidence, 0, len(fr.Steps)+2)
	for i, st := range fr.Steps {
		if i == 0 {
			out = append(out, Evidence{
				Label: st.Event + " — users who entered the funnel", Value: fmt.Sprintf("%d", st.Count),
				Report: rep, Tool: "funnel", Args: args,
			})
			continue
		}
		prev := fr.Steps[i-1]
		out = append(out, Evidence{
			Label: fmt.Sprintf("%s → %s conversion", prev.Event, st.Event),
			Value: fmt.Sprintf("%d%% — %d of %d continued, %d stopped", pctOf(st.ConversionFromPrev), st.Count, prev.Count, st.DroppedFromPrev),
			N:     prev.Count, Report: rep, Tool: "funnel", Args: args,
		})
	}
	out = append(out, Evidence{
		Label: "end-to-end conversion", Value: fmt.Sprintf("%d%%", pctOf(fr.OverallConversion)),
		N: fr.Steps[0].Count, Report: rep, Tool: "funnel", Args: args,
	})
	if f.Kind == insight.KindSegment && f.Prop != "" {
		segRep, segArgs := funnelReport(names, f.Prop)
		out = append(out, Evidence{
			Label: fmt.Sprintf("%s = %s through %s → %s", f.Prop, f.Value, f.From, f.To),
			Value: fmt.Sprintf("%d%%, against %d%% for everyone", f.Rate, pctOf(rateBetween(fr, f.From, f.To))),
			N:     f.N, Report: segRep, Tool: "funnel", Args: segArgs,
		})
	}
	return out
}

func (c *ctx) pages(rows []web.Row, why string) []Where {
	out := make([]Where, 0, 5)
	for i, r := range rows {
		if i >= 5 {
			break
		}
		out = append(out, Where{Path: r.Value, Users: r.Count, Why: why})
	}
	return out
}

func funnelAcceptance(names []string, f insight.Finding) Acceptance {
	rep, args := funnelReport(names, "")
	metric := fmt.Sprintf("%s → %s conversion", f.From, f.To)
	if f.Kind == insight.KindSegment && f.Prop != "" {
		metric = fmt.Sprintf("%s → %s conversion for %s = %s", f.From, f.To, f.Prop, f.Value)
		rep, args = funnelReport(names, f.Prop)
	}
	return Acceptance{
		Metric: metric, Current: fmt.Sprintf("%d%%", f.Rate), Direction: "up",
		Window: "7 days after the change ships", Report: rep, Tool: "funnel", Args: args,
	}
}

func funnelReport(names []string, breakdown string) (string, string) {
	rep := "/v1/funnel?window=168h&steps=" + url.QueryEscape(strings.Join(names, ","))
	a := map[string]any{"steps": names}
	if breakdown != "" {
		rep += "&breakdown=" + url.QueryEscape(breakdown)
		a["breakdown"] = breakdown
	}
	return rep, jsonArgs(a)
}

func trendReport(ev string) (string, string) {
	return "/v1/trends?days=14&event=" + url.QueryEscape(ev), jsonArgs(map[string]any{"event": ev, "days": 14})
}

// jsonArgs renders MCP arguments compactly and DETERMINISTICALLY — encoding/json sorts map keys,
// which matters because agreement_test compares this string byte-for-byte across two surfaces.
func jsonArgs(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func pctOf(f float64) int { return int(f*100 + 0.5) }

func rateBetween(fr funnel.Result, from, to string) float64 {
	for i := 1; i < len(fr.Steps); i++ {
		if fr.Steps[i-1].Event == from && fr.Steps[i].Event == to {
			return fr.Steps[i].ConversionFromPrev
		}
	}
	return 0
}

func human(s string) string {
	if s == "" {
		return "that step"
	}
	return s
}

// sampleNoun names what a finding's N actually counts. Only three of the seven kinds count
// people: an anomaly's N is EVENTS over the trailing week (insight.go, s.baseTotal), a trend's
// N is events in the prior week (prev7), a crawl finding's N is HTTP fetches by a bot, and an
// AI-visibility finding's N is sampled model answers. lowN called all of them "people", and
// that sentence is embedded verbatim in the paste-into-your-agent prompt — an agent told it
// has "40 people" when it has 40 GPTBot fetches sizes its confidence on a population that does
// not exist. Kinds without an N never reach here (lowN returns early on 0).
func sampleNoun(kind string) string {
	switch kind {
	case insight.KindAnomaly, insight.KindTrend:
		return "events"
	case insight.KindCrawl:
		return "crawler fetches"
	case insight.KindAIVis:
		return "sampled model answers"
	default: // dropoff, segment, retention — these really are distinct users
		return "people"
	}
}

// lowN restates the sample in the brief's own voice. The verdict already suppresses anything
// under its floor and annotates anything thin — but a brief is the thing that gets PASTED
// somewhere else, where the page's "(n=34, small sample)" caveat does not travel with it.
func lowN(f insight.Finding) string {
	switch {
	case f.N == 0:
		return ""
	case f.N < 100:
		return fmt.Sprintf("Sample: %d. That is thin. A percentage over %d %s is directional, not a measurement — weigh the size of the fix accordingly and do not ship anything large off this number alone.", f.N, f.N, sampleNoun(f.Kind))
	default:
		return fmt.Sprintf("Sample: %d.", f.N)
	}
}

// renderPrompt is the paste-anywhere rendering, derived entirely from the fields above so the
// copy block can never assert something the structured brief does not.
func renderPrompt(b Brief) string {
	var s strings.Builder
	s.WriteString("Fix this with me. Every number below was computed from my own product events by a deterministic report — you can re-run each one and get the same answer. None of it is a model's summary, including this brief.\n\n")
	fmt.Fprintf(&s, "FINDING (%s)\n%s\n%s\n", b.Finding.Severity, b.Finding.Title, b.Finding.Detail)
	if b.Note != "" {
		s.WriteString(b.Note + "\n")
	}
	if len(b.Evidence) > 0 {
		s.WriteString("\nEVIDENCE — each row names the report that recomputes it\n")
		for _, e := range b.Evidence {
			fmt.Fprintf(&s, "- %s: %s\n    re-run: GET %s   ·   MCP: %s %s\n", e.Label, e.Value, e.Report, e.Tool, e.Args)
		}
	}
	if len(b.Where) > 0 {
		s.WriteString("\nWHERE IT SHOWS UP — measured pages, not a guess about my repo. Start reading the code behind these.\n")
		for _, w := range b.Where {
			fmt.Fprintf(&s, "- %s — %d (%s)\n", w.Path, w.Users, w.Why)
		}
	}
	s.WriteString("\nWHAT I WANT YOU TO DO\n")
	for i, d := range b.Do {
		fmt.Fprintf(&s, "%d. %s\n", i+1, d)
	}
	s.WriteString("\nACCEPTANCE — the only thing that closes this\n")
	fmt.Fprintf(&s, "%s is %s today. It should go %s within %s.\n", b.Acceptance.Metric, b.Acceptance.Current, b.Acceptance.Direction, b.Acceptance.Window)
	fmt.Fprintf(&s, "Re-run: GET %s   ·   MCP: %s %s\n", b.Acceptance.Report, b.Acceptance.Tool, b.Acceptance.Args)
	s.WriteString("If it does not move, the change was wrong: say so and revert it. Never tell me it worked without re-running that exact report.\n")
	fmt.Fprintf(&s, "\nPROVENANCE\n%s\nFinding id: %s\nIf the smolanalytics MCP server is connected you can pull this brief live instead of trusting this paste: fix_brief {\"finding\": %q}\n",
		b.ComputedBy, b.Fingerprint, b.Finding.Title)
	return s.String()
}

// renderAsk is the short form, for places with a hard length budget (a deeplink URL). It does not
// restate the numbers — it tells the agent to pull them, which is stronger anyway: the numbers
// arrive fresh and provably from the engine instead of pasted and stale.
func renderAsk(b Brief) string {
	return fmt.Sprintf("Fix this finding from my smolanalytics verdict: %q. Call the smolanalytics MCP tool fix_brief with {\"finding\": %q} — it returns the computed evidence, the measured pages it shows up on, and the acceptance criterion. Read the code behind them, propose the smallest change the evidence supports, show me the diff before applying anything, and re-run the report the brief names before claiming it worked. If nothing you read explains the number, say so and stop.",
		b.Finding.Title, b.Finding.Title)
}
