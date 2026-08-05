package api

import (
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/insight"
)

// The findings block is a triage list, and the version this replaced could not triage: four
// bullets in one weight, one of them a paragraph, the warnings-first sort thrown away by the
// rendering. What is pinned here is the part a browser cannot tell you it lost — which figure
// got promoted, which prose survived, and whether the rank is still a character and a word
// rather than only a colour.

// The boundary rule is the whole reason promoteFigure takes a capture group instead of a plain
// \d: "Day-1 retention 45%" must promote the 45% and not the 1 in "Day-1", and a path like
// /blog/2024-guide must promote nothing at all. Both read as obviously fine and both would ship.
func TestPromoteFigureFindsTheRightNumber(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string // the exact text expected inside <b class="vn">, "" for no promotion
	}{
		{"Day-1 retention 45%, day-7 6%", "45%"},
		{"page views jumped 182% in the last 24h", "182%"},
		{"signup is down 8% week-over-week", "8%"},
		{"Mobile visitors convert worst from signup to activate, 1.7× worse than average", "1.7×"},
		{"only 49% go on to activate, so 1220 people stop here.", "49%"},
		{"1220 people stop here", "1220"}, // bare number: the trailing space stays outside the tag
		{"No AI crawler has read /blog/2024-guide", ""},
		{"Your robots.txt turns the AI crawlers away", ""},
	} {
		got, ok := promoteFigure(tc.in)
		if tc.want == "" {
			if ok {
				t.Errorf("promoteFigure(%q) promoted something, expected nothing: %s", tc.in, got)
			}
			continue
		}
		if !ok {
			t.Errorf("promoteFigure(%q) promoted nothing, want %q", tc.in, tc.want)
			continue
		}
		if wrapped := `<b class="vn">` + tc.want + `</b>`; !strings.Contains(string(got), wrapped) {
			t.Errorf("promoteFigure(%q)\n got %s\nwant it to contain %s", tc.in, got, wrapped)
		}
	}
}

// A finding's title can carry a path, a segment value or an event name — all of it user data.
// Promotion splices raw HTML into the row, so the escaping has to happen on the pieces.
func TestPromoteFigureEscapesTheSentenceAroundTheFigure(t *testing.T) {
	got, _ := promoteFigure(`No AI crawler has read /x?<script>alert(1)</script> 42%`)
	if strings.Contains(string(got), "<script>") {
		t.Fatalf("promoteFigure passed a raw tag through: %s", got)
	}
	if !strings.Contains(string(got), "&lt;script&gt;") {
		t.Errorf("promoteFigure did not escape the surrounding sentence: %s", got)
	}
}

// One line per finding. The detail keeps its first sentence and nothing else — except the
// small-sample caveat, which is the honesty note on a thin base: a number that silently drops
// "(n=34, small sample)" reads surer than it is.
func TestLeadSentenceKeepsOneSentenceAndTheCaveat(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{
			"only 49% go on to activate, so 1220 people stop here. End to end, 23% get from signup to checkout.",
			"only 49% go on to activate, so 1220 people stop here.",
		},
		{"430 in the last 7 days vs 406 the week before.", "430 in the last 7 days vs 406 the week before."},
		{
			"399 in the last 7 days vs 35 the week before. (n=35, small sample)",
			"399 in the last 7 days vs 35 the week before. (n=35, small sample)",
		},
		{
			"only 12% continue, against 40% for everyone. Fixing this group is the biggest lever. (n=34, small sample)",
			"only 12% continue, against 40% for everyone. (n=34, small sample)",
		},
	} {
		if got := leadSentence(tc.in); got != tc.want {
			t.Errorf("leadSentence(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestVerdictLinesRankAndPromoteExactlyOnce(t *testing.T) {
	fs := []insight.Finding{
		{Severity: "warn", Title: "the card takes the first finding", Detail: "not in the list."},
		{
			Severity: "warn",
			Title:    "Biggest drop-off: after they signup",
			Detail:   "only 49% go on to activate, so 1220 people stop here. End to end, 23% get from signup to checkout.",
		},
		{Severity: "info", Title: "signup is up 6% week-over-week", Detail: "430 in the last 7 days vs 406 the week before."},
	}
	got := verdictLines(fs)
	if len(got) != 2 {
		t.Fatalf("verdictLines returned %d rows, want 2 (element 0 is the card)", len(got))
	}

	// the rank is a character AND a word, never colour alone
	if got[0].Mark != "!" || got[0].MarkLabel != "warning" || !got[0].Warn {
		t.Errorf("warn row is not marked as one: %+v", got[0])
	}
	if got[1].Mark == "!" || got[1].MarkLabel != "note" || got[1].Warn {
		t.Errorf("info row is marked as a warning: %+v", got[1])
	}

	// exactly one promoted figure per row — two is the undifferentiated prose again
	for i, l := range got {
		if n := strings.Count(string(l.Title)+string(l.Lead), `class="vn"`); n != 1 {
			t.Errorf("row %d promoted %d figures, want exactly 1:\n title %s\n lead  %s", i, n, l.Title, l.Lead)
		}
	}
	// the title states no figure, so the lead's is the one that gets promoted
	if !strings.Contains(string(got[0].Lead), `<b class="vn">49%</b>`) {
		t.Errorf("drop-off row did not promote 49%% out of its detail: %s", got[0].Lead)
	}
	// the title states one, so the lead is left alone
	if !strings.Contains(string(got[1].Title), `<b class="vn">6%</b>`) {
		t.Errorf("trend row did not promote 6%% in its title: %s", got[1].Title)
	}

	// the prose that came off the row is still ON the row, for hover and the a11y tree
	if !strings.Contains(got[0].Detail, "End to end, 23%") {
		t.Errorf("the trimmed prose is gone entirely, not collapsed: %q", got[0].Detail)
	}
	if strings.Contains(string(got[0].Lead), "End to end") {
		t.Errorf("the row is still a paragraph: %s", got[0].Lead)
	}

	// the ask the "why?" affordance runs has to stay the RAW title — it is the finding's
	// identity everywhere else (fingerprint, brief, cloud fix-PR branch)
	if got[1].Q != "signup is up 6% week-over-week" {
		t.Errorf("why? would ask %q, not the finding's own title", got[1].Q)
	}

	if verdictLines(fs[:1]) != nil {
		t.Error("a single finding is the card and nothing else — the list must be empty")
	}
}

// Item 3 of the presentation pass: every other tile puts a COMPARISON under its number, and two
// put a DEFINITION there in the same slot and style, so "Bounce rate 44% / 1 pageview, <10s"
// read as a value and parsed as nonsense.
func TestNoDefinitionsInTheDeltaSlot(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	for _, def := range []string{"1 pageview, &lt;10s", "focused, per visitor"} {
		if strings.Contains(tpl, def) {
			t.Errorf("%q is back in a tile's delta slot — definitions belong in the .kwhy affordance", def)
		}
	}
	if strings.Count(tpl, `class="kwhy"`) < 2 {
		t.Error("the bounce-rate and engaged-time tiles lost their definition affordance; the numbers are now unexplained")
	}
}

// Item 5: the dev-data footnote used to sit above the verdict — the least important true thing
// on the page holding its most valuable space. One definition, two call sites, so the empty
// state (where "view dev data →" is the most useful link on screen) cannot drift from it.
func TestDevNoteComesAfterTheVerdict(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	if !strings.Contains(tpl, `{{define "devnote"}}`) {
		t.Fatal("the devnote is inlined again — two copies is how one of them stops matching the other")
	}
	verdict := strings.Index(tpl, `<div class="verdict">`)
	call := strings.Index(tpl, `{{template "devnote" .}}`)
	if call < 0 || verdict < 0 {
		t.Fatal("verdict block or devnote call site not found")
	}
	// the FIRST call site is the empty-state one (guarded by {{if not .HasData}}), so the one
	// that has to come after the verdict is the last
	if last := strings.LastIndex(tpl, `{{template "devnote" .}}`); last < verdict {
		t.Error("every devnote call site is above the verdict again")
	}
	if strings.Count(tpl, `{{template "devnote" .}}`) != 2 {
		t.Error("the devnote should render in exactly two places: the empty state, and under the verdict")
	}
}
