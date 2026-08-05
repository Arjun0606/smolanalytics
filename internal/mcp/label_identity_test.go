package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/agent"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// labelOwnerServer seeds conversations the way live agent instrumentation writes them: the turn
// carries the USER's distinct_id and puts the conversation id in a property. (The older fixture
// in agent_labels_test.go sets DistinctID = conversation id, which is exactly the confusion that
// let this bug hide.)
func labelOwnerServer(t *testing.T) (*Server, *memory.Store) {
	t.Helper()
	st := memory.New()
	base := time.Now().UTC().Add(-2 * time.Hour)
	turn := func(id, user, cid string, off time.Duration) event.Event {
		return event.Event{ID: id, Name: agent.EventTurn, DistinctID: user, Timestamp: base.Add(off),
			Properties: map[string]any{agent.PropConversationID: cid, "role": "user", "text": "my card was charged twice"}}
	}
	if err := st.Ingest(
		turn("t1", "u1", "c1", 0),
		turn("t2", "u1", "c1", time.Minute),
		turn("t3", "u2", "c2", 2*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	return New(st), st
}

// label_conversation wrote the CONVERSATION id into the event's distinct_id, so every label
// invented a user that never existed. Labelling 22 conversations added 22 to total_users and
// active_users_7d, gave ComputeLifecycle 22 "new" users on the day of labelling, and left
// retention with 22 single-event cohort members that could never return — the agent's own
// bookkeeping read back to the user as growth. Nothing joins on DistinctID here (the join is on
// the conversation_id property), so the label belongs to the user who had the conversation.
func TestLabellingDoesNotInventAUser(t *testing.T) {
	s, st := labelOwnerServer(t)

	usersBefore := overviewUsers(t, s)

	if _, err := s.callTool("label_conversation", mustJSON(map[string]any{
		"conversation_id": "c1",
		"labels":          map[string]any{"intent": "billing"},
		"labeled_by":      "claude-sonnet-4-5",
	})); err != nil {
		t.Fatalf("label_conversation: %v", err)
	}

	if got := overviewUsers(t, s); got != usersBefore {
		t.Errorf("overview total_users went %d -> %d after one label — the agent's bookkeeping is being counted as a user",
			usersBefore, got)
	}

	evs, _ := st.Range(time.Time{}, time.Time{})
	var found bool
	for _, e := range evs {
		if e.Name != agent.EventLabel {
			continue
		}
		found = true
		if e.DistinctID != "u1" {
			t.Errorf("agent_label distinct_id = %q, want the conversation's owner \"u1\"", e.DistinctID)
		}
		if e.Properties[agent.PropConversationID] != "c1" {
			t.Errorf("the conversation_id must stay a property — it is what agent_labels joins on: %+v", e.Properties)
		}
	}
	if !found {
		t.Fatal("no agent_label event was written")
	}

	// the breakdown still finds the label, i.e. moving the identity did not break the join
	out, err := s.callTool("agent_labels", mustJSON(map[string]any{"label": "intent"}))
	if err != nil {
		t.Fatal(err)
	}
	var b agent.LabelBreakdown
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatal(err)
	}
	if b.Labeled != 1 || len(b.Values) != 1 || b.Values[0].Value != "billing" {
		t.Errorf("agent_labels lost the label after the identity change: %+v", b)
	}
}

func overviewUsers(t *testing.T, s *Server) int {
	t.Helper()
	out, err := s.callTool("overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	var ov struct {
		TotalUsers int `json:"total_users"`
	}
	if err := json.Unmarshal([]byte(out), &ov); err != nil {
		t.Fatalf("overview returned invalid JSON: %v\n%s", err, out)
	}
	return ov.TotalUsers
}
