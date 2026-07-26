package agent

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// lbl builds one agent_label event: a model's inference about a conversation, written back as an
// ordinary event (never a mutation of the turns it describes).
func lbl(cid, by string, offset time.Duration, labels map[string]any) event.Event {
	p := map[string]any{PropConversationID: cid, PropLabeledBy: by}
	for k, v := range labels {
		p[k] = v
	}
	return event.Event{ID: cid + by + offset.String(), Name: EventLabel, DistinctID: cid,
		Timestamp: time.Unix(1_700_000_000, 0).UTC().Add(offset), Properties: p}
}

func TestLabelBreakdownCountsConversationsPerValue(t *testing.T) {
	evs := []event.Event{
		tn("c1", "user", 1, nil), tn("c1", "assistant", 2, nil),
		tn("c2", "user", 1, nil), tn("c2", "assistant", 2, nil),
		tn("c3", "user", 1, nil), tn("c3", "assistant", 2, nil),
		tn("c4", "user", 1, nil), // labeled by nobody
		lbl("c1", "claude-sonnet-4-5", 10, map[string]any{"intent": "billing", "sentiment": "negative"}),
		lbl("c2", "claude-sonnet-4-5", 11, map[string]any{"intent": "billing"}),
		lbl("c3", "gpt-5", 12, map[string]any{"intent": "onboarding"}),
	}
	b := ComputeLabelBreakdown(evs, "intent", time.Time{}, time.Time{})
	if b.Conversations != 4 || b.Labeled != 3 || b.Unlabeled != 1 {
		t.Fatalf("counts wrong: %+v", b)
	}
	// busiest value first, name as tie-break (same rule as query.Breakdown)
	if len(b.Values) != 2 || b.Values[0].Value != "billing" || b.Values[0].Conversations != 2 {
		t.Fatalf("expected billing(2) first, got %+v", b.Values)
	}
	if b.Values[1].Value != "onboarding" || b.Values[1].Conversations != 1 {
		t.Fatalf("expected onboarding(1) second, got %+v", b.Values)
	}
	// the honesty payload: WHO inferred these, sorted and deduped
	if !reflect.DeepEqual(b.LabeledBy, []string{"claude-sonnet-4-5", "gpt-5"}) {
		t.Fatalf("labeled_by wrong: %+v", b.LabeledBy)
	}
	if !b.Inferred {
		t.Fatalf("label values are model inferences and must be flagged as such")
	}
	if !strings.Contains(b.Note, "inference") || !strings.Contains(b.Note, "computed") {
		t.Fatalf("note must say the values are inferred and the counts computed: %q", b.Note)
	}
	// a second label key over the same events breaks down independently
	s := ComputeLabelBreakdown(evs, "sentiment", time.Time{}, time.Time{})
	if s.Labeled != 1 || s.Unlabeled != 3 || len(s.Values) != 1 || s.Values[0].Value != "negative" {
		t.Fatalf("sentiment breakdown wrong: %+v", s)
	}
	// deterministic across runs
	if !reflect.DeepEqual(b, ComputeLabelBreakdown(evs, "intent", time.Time{}, time.Time{})) {
		t.Fatalf("ComputeLabelBreakdown is not deterministic")
	}
}

func TestLabelBreakdownUnlabeledIsHonestNotEmptyLooking(t *testing.T) {
	evs := []event.Event{
		tn("c1", "user", 1, nil), tn("c1", "assistant", 2, nil),
		tn("c2", "user", 1, nil),
	}
	b := ComputeLabelBreakdown(evs, "intent", time.Time{}, time.Time{})
	if b.Conversations != 2 || b.Labeled != 0 || b.Unlabeled != 2 {
		t.Fatalf("unlabeled counts wrong: %+v", b)
	}
	if len(b.Values) != 0 {
		t.Fatalf("nothing is labeled, so there must be zero values (never a fabricated split): %+v", b.Values)
	}
	if b.Values == nil || b.LabeledBy == nil {
		t.Fatalf("empty results must serialize as [] not null: %+v", b)
	}
	// the empty result has to TEACH the loop, not just be empty
	if !strings.Contains(b.Note, "sample_conversations") || !strings.Contains(b.Note, "label_conversation") {
		t.Fatalf("empty note must explain how to label: %q", b.Note)
	}
	// an unknown label name is an honest empty too, never an error
	none := ComputeLabelBreakdown(evs, "no_such_label", time.Time{}, time.Time{})
	if none.Labeled != 0 || len(none.Values) != 0 || none.Conversations != 2 {
		t.Fatalf("unknown label should be honestly empty: %+v", none)
	}
}

func TestLabelBreakdownLatestWriteWins(t *testing.T) {
	// append-only means a correction is a NEW event; the most recent write is the answer,
	// and the older one must not be double-counted.
	evs := []event.Event{
		tn("c1", "user", 1, nil),
		lbl("c1", "gpt-5", 10, map[string]any{"intent": "billing"}),
		lbl("c1", "claude-sonnet-4-5", 20, map[string]any{"intent": "onboarding"}),
	}
	b := ComputeLabelBreakdown(evs, "intent", time.Time{}, time.Time{})
	if b.Labeled != 1 || len(b.Values) != 1 || b.Values[0].Value != "onboarding" {
		t.Fatalf("latest label must win exactly once: %+v", b)
	}
	if !reflect.DeepEqual(b.LabeledBy, []string{"claude-sonnet-4-5"}) {
		t.Fatalf("labeled_by must name the model behind the counted label: %+v", b.LabeledBy)
	}
}

func TestLabelBreakdownWindowScopesConversationsNotLabels(t *testing.T) {
	// a conversation inside the window, labeled LATER (outside it), still counts as labeled:
	// a label is a statement about the conversation, not something that happened at that moment.
	base := time.Unix(1_700_000_000, 0).UTC()
	evs := []event.Event{
		tn("in", "user", 1*time.Hour, nil),
		tn("out", "user", 100*time.Hour, nil),
		lbl("in", "claude-sonnet-4-5", 200*time.Hour, map[string]any{"intent": "billing"}),
	}
	b := ComputeLabelBreakdown(evs, "intent", base, base.Add(2*time.Hour))
	if b.Conversations != 1 || b.Labeled != 1 {
		t.Fatalf("window must scope conversations while labels still join: %+v", b)
	}
	if len(b.Values) != 1 || b.Values[0].Value != "billing" {
		t.Fatalf("value wrong: %+v", b.Values)
	}
}

func TestSampleConversationsDeterministicAndCapped(t *testing.T) {
	var evs []event.Event
	// c1 oldest ... c5 newest, so most-recent-first ordering is checkable
	for i := 1; i <= 5; i++ {
		cid := string(rune('a' + i))
		evs = append(evs,
			tn(cid, "user", time.Duration(i)*time.Hour, nil),
			tn(cid, "assistant", time.Duration(i)*time.Hour+time.Minute, nil))
	}
	a := SampleConversations(evs, time.Time{}, time.Time{}, 3)
	b := SampleConversations(evs, time.Time{}, time.Time{}, 3)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("SampleConversations must be deterministic:\n%+v\n%+v", a, b)
	}
	if a.Total != 5 || a.Returned != 3 || a.Limit != 3 {
		t.Fatalf("cap wrong: total=%d returned=%d limit=%d", a.Total, a.Returned, a.Limit)
	}
	// most recent conversation first
	if a.Conversations[0].ConversationID != "f" || a.Conversations[2].ConversationID != "d" {
		t.Fatalf("expected most-recent-first f,e,d — got %s,%s,%s",
			a.Conversations[0].ConversationID, a.Conversations[1].ConversationID, a.Conversations[2].ConversationID)
	}
	// turns inside a conversation stay oldest-first so the model reads it as a conversation
	turns := a.Conversations[0].Turns
	if len(turns) != 2 || turns[0].Role != "user" || turns[1].Role != "assistant" {
		t.Fatalf("turns must be oldest-first: %+v", turns)
	}
	if !strings.Contains(a.Note, "sample") {
		t.Fatalf("a capped sample must say it is a sample: %q", a.Note)
	}
	// the default limit applies when none is given, and it is never unbounded
	d := SampleConversations(evs, time.Time{}, time.Time{}, 0)
	if d.Limit != DefaultSampleLimit || d.Returned != 5 {
		t.Fatalf("default limit wrong: %+v", d)
	}
	if SampleConversations(evs, time.Time{}, time.Time{}, 10_000).Limit != MaxSampleLimit {
		t.Fatalf("limit must be capped at %d", MaxSampleLimit)
	}
}

func TestSampleConversationsNeverInventsText(t *testing.T) {
	long := strings.Repeat("x", MaxTextRunes+50)
	evs := []event.Event{
		tn("c1", "user", 1, nil), // no text property at all
		tn("c1", "assistant", 2, map[string]any{"text": "here is your invoice"}),
		tn("c2", "user", 3, map[string]any{"text": long}),
		tn("c2", "assistant", 4, map[string]any{"text": ""}), // empty is not content
	}
	s := SampleConversations(evs, time.Time{}, time.Time{}, 0)
	byID := map[string]SampledConversation{}
	for _, c := range s.Conversations {
		byID[c.ConversationID] = c
	}
	c1 := byID["c1"]
	if c1.Turns[0].HasText || c1.Turns[0].Text != "" {
		t.Fatalf("a turn with no text must come back without text, never invented: %+v", c1.Turns[0])
	}
	if c1.Turns[0].Role != "user" || c1.Turns[0].Timestamp.IsZero() {
		t.Fatalf("a text-less turn still carries role and timing: %+v", c1.Turns[0])
	}
	if !c1.Turns[1].HasText || c1.Turns[1].Text != "here is your invoice" {
		t.Fatalf("real text must be returned as sent: %+v", c1.Turns[1])
	}
	c2 := byID["c2"]
	if !c2.Turns[0].TextTruncated || len([]rune(c2.Turns[0].Text)) != MaxTextRunes {
		t.Fatalf("long text must be truncated and flagged: truncated=%v len=%d",
			c2.Turns[0].TextTruncated, len([]rune(c2.Turns[0].Text)))
	}
	if c2.Turns[1].HasText {
		t.Fatalf("an empty text property is not content: %+v", c2.Turns[1])
	}
	if s.TurnsWithText != 2 || s.TurnsWithoutText != 2 {
		t.Fatalf("text counts wrong: with=%d without=%d", s.TurnsWithText, s.TurnsWithoutText)
	}
}

func TestSampleConversationsFlagsAlreadyLabeled(t *testing.T) {
	evs := []event.Event{
		tn("c1", "user", 1, nil),
		tn("c2", "user", 2, nil),
		lbl("c1", "claude-sonnet-4-5", 10, map[string]any{"intent": "billing"}),
	}
	s := SampleConversations(evs, time.Time{}, time.Time{}, 0)
	for _, c := range s.Conversations {
		want := c.ConversationID == "c1"
		if c.Labeled != want {
			t.Fatalf("conversation %s labeled=%v, want %v", c.ConversationID, c.Labeled, want)
		}
	}
}

func TestSampleConversationsEmptyIsHonest(t *testing.T) {
	s := SampleConversations(nil, time.Time{}, time.Time{}, 0)
	if s.Returned != 0 || s.Total != 0 || s.Conversations == nil {
		t.Fatalf("empty sample must be zero and serialize as [], got %+v", s)
	}
	if !strings.Contains(s.Note, "nothing to label") {
		t.Fatalf("empty sample must say there is nothing to label yet: %q", s.Note)
	}
}
