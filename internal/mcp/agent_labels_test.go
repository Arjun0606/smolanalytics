package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/agent"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// labelServer seeds two conversations: one with turn text, one without.
func labelServer(t *testing.T) (*Server, *memory.Store) {
	t.Helper()
	st := memory.New()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	turn := func(id, cid, role string, off time.Duration, text string) event.Event {
		p := map[string]any{agent.PropConversationID: cid, "role": role}
		if text != "" {
			p["text"] = text
		}
		return event.Event{ID: id, Name: agent.EventTurn, DistinctID: cid, Timestamp: base.Add(off), Properties: p}
	}
	if err := st.Ingest(
		turn("t1", "c1", "user", 0, "my card was charged twice"),
		turn("t2", "c1", "assistant", time.Minute, "let me check that invoice"),
		turn("t3", "c2", "user", time.Hour, ""),
		turn("t4", "c2", "assistant", time.Hour+time.Minute, ""),
	); err != nil {
		t.Fatal(err)
	}
	return New(st), st
}

// The full loop: read a sample, write a label back, read the computed breakdown over it.
func TestLabelLoopRoundTrip(t *testing.T) {
	s, st := labelServer(t)

	sample, isErr := toolText(t, call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sample_conversations","arguments":{}}}`))
	if isErr {
		t.Fatalf("sample_conversations failed: %s", sample)
	}
	if !strings.Contains(sample, `"conversation_id":"c2"`) || !strings.Contains(sample, "my card was charged twice") {
		t.Fatalf("sample should carry both conversations and the real text: %s", sample)
	}
	if !strings.Contains(sample, `"labeled":false`) {
		t.Fatalf("an unlabeled conversation must say so, so the model knows what is left: %s", sample)
	}

	before, _ := st.Range(time.Time{}, time.Time{})
	wrote, isErr := toolText(t, call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"label_conversation","arguments":{"conversation_id":"c1","labels":{"intent":"billing","sentiment":"negative"},"labeled_by":"claude-sonnet-4-5"}}}`))
	if isErr {
		t.Fatalf("label_conversation failed: %s", wrote)
	}
	if !strings.Contains(wrote, "inference") {
		t.Fatalf("the write must say plainly that labels are an inference: %s", wrote)
	}

	// append-only: exactly one NEW agent_label event, and every turn untouched.
	after, _ := st.Range(time.Time{}, time.Time{})
	if len(after) != len(before)+1 {
		t.Fatalf("expected exactly one new event, got %d -> %d", len(before), len(after))
	}
	labels := 0
	for _, e := range after {
		if e.Name == agent.EventLabel {
			labels++
			if e.Properties[agent.PropLabeledBy] != "claude-sonnet-4-5" || e.Properties["intent"] != "billing" {
				t.Fatalf("label event is missing its attribution or labels: %+v", e.Properties)
			}
			if e.Properties[agent.PropLabeledAt] == nil {
				t.Fatalf("label event must carry labeled_at: %+v", e.Properties)
			}
		}
	}
	if labels != 1 {
		t.Fatalf("expected 1 agent_label event, got %d", labels)
	}
	for _, b := range before {
		for _, a := range after {
			if a.ID == b.ID && a.Properties["intent"] != nil {
				t.Fatalf("labelling must never mutate an existing event: %+v", a)
			}
		}
	}

	out, isErr := toolText(t, call(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"agent_labels","arguments":{"label":"intent"}}}`))
	if isErr {
		t.Fatalf("agent_labels failed: %s", out)
	}
	var b agent.LabelBreakdown
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatal(err)
	}
	if b.Conversations != 2 || b.Labeled != 1 || b.Unlabeled != 1 {
		t.Fatalf("breakdown counts wrong: %+v", b)
	}
	if len(b.Values) != 1 || b.Values[0].Value != "billing" || b.Values[0].Conversations != 1 {
		t.Fatalf("breakdown values wrong: %+v", b.Values)
	}
	if len(b.LabeledBy) != 1 || b.LabeledBy[0] != "claude-sonnet-4-5" {
		t.Fatalf("breakdown must name the labelling model: %+v", b.LabeledBy)
	}

	// and the sample now knows c1 is done
	sample2, _ := toolText(t, call(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"sample_conversations","arguments":{}}}`))
	if !strings.Contains(sample2, `"labeled":true`) {
		t.Fatalf("a labeled conversation must come back flagged: %s", sample2)
	}
}

// Every way of writing a label that would produce a number nobody can trust is an error with
// guidance, not a silent write.
func TestLabelConversationRejectsUntrustworthyWrites(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{"no labeled_by", `{"conversation_id":"c1","labels":{"intent":"billing"}}`, "labeled_by is required"},
		{"no conversation_id", `{"labels":{"intent":"billing"},"labeled_by":"m"}`, "conversation_id is required"},
		{"no labels", `{"conversation_id":"c1","labels":{},"labeled_by":"m"}`, "at least one entry"},
		{"phantom conversation", `{"conversation_id":"nope","labels":{"intent":"billing"},"labeled_by":"m"}`, "nothing to label"},
		{"reserved name", `{"conversation_id":"c1","labels":{"labeled_by":"me"},"labeled_by":"m"}`, "reserved"},
		{"nested value", `{"conversation_id":"c1","labels":{"intent":{"a":1}},"labeled_by":"m"}`, "not a nested object"},
		{"blank value", `{"conversation_id":"c1","labels":{"intent":"  "},"labeled_by":"m"}`, "empty value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, st := labelServer(t)
			before, _ := st.Range(time.Time{}, time.Time{})
			raw := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"label_conversation","arguments":` + c.args + `}}`
			text, isErr := toolText(t, call(t, s, raw))
			if !isErr {
				t.Fatalf("expected an error, got: %s", text)
			}
			if !strings.Contains(text, c.want) {
				t.Fatalf("error should say %q, got: %s", c.want, text)
			}
			after, _ := st.Range(time.Time{}, time.Time{})
			if len(after) != len(before) {
				t.Fatalf("a rejected label must write nothing, %d -> %d", len(before), len(after))
			}
		})
	}
}

// A missing label on agent_labels is a guiding error, not a breakdown of nothing.
func TestAgentLabelsNeedsALabel(t *testing.T) {
	s, _ := labelServer(t)
	text, isErr := toolText(t, call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"agent_labels","arguments":{}}}`))
	if !isErr || !strings.Contains(text, "label is required") {
		t.Fatalf("expected the label-required error, got isErr=%v: %s", isErr, text)
	}
}

// Nothing labeled yet must teach the loop instead of returning a bare empty object.
func TestAgentLabelsUnlabeledExplainsHowToLabel(t *testing.T) {
	s, _ := labelServer(t)
	text, isErr := toolText(t, call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"agent_labels","arguments":{"label":"intent"}}}`))
	if isErr {
		t.Fatalf("an unlabeled instance is not an error: %s", text)
	}
	if !strings.Contains(text, "sample_conversations") || !strings.Contains(text, `"labeled":0`) {
		t.Fatalf("empty result must be honest and self-teaching: %s", text)
	}
}
