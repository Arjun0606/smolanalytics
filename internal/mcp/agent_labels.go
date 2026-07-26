package mcp

// The write half of the BYO-model conversation-intelligence loop. The user's own model reads
// conversations with sample_conversations, infers labels, and calls label_conversation to write
// them back. We never call a model, so this costs us nothing and is never metered to them.
//
// Two rules hold the brand line here. (1) Append-only: a label is a NEW agent_label event that
// joins to the conversation by conversation_id; no existing event is ever touched. (2) Attributed:
// labeled_by is required and must name the model, because a label is an INFERENCE and every
// report over it says so. The counts computed over labels stay provably correct; only the label
// itself is a judgement.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/agent"
	"github.com/Arjun0606/smolanalytics/internal/event"
)

// errNoLabel is the exact wording GET /v1/agent/labels uses for a missing label, so the two
// surfaces answer the same question the same way (see api.errNoLabel).
const errNoLabel = "label is required, e.g. intent or sentiment: a label name your model wrote with label_conversation. nothing labeled yet? call sample_conversations, then label_conversation."

// label hygiene: enough room to be useful, tight enough that a breakdown stays readable.
const (
	maxLabelKeys     = 10
	maxLabelKeyLen   = 40
	maxLabelValueLen = 200
)

// labelConversation validates and appends one agent_label event. Validation is strict on
// purpose: a phantom conversation_id, an unnamed labelling model, or a nested blob of a value
// would each produce a breakdown that reads like a measurement but isn't, which is the one thing
// this engine refuses to do.
func (s *Server) labelConversation(evs []event.Event, cid string, labels map[string]any, by string) (string, error) {
	if cid == "" {
		return "", fmt.Errorf("conversation_id is required. get one from sample_conversations")
	}
	if by == "" {
		return "", fmt.Errorf("labeled_by is required: the model writing these labels, e.g. \"claude-sonnet-4-5\". labels are an inference, so every report over them names who inferred them")
	}
	if len(labels) == 0 {
		return "", fmt.Errorf("labels is required and must have at least one entry, e.g. {\"intent\":\"billing\",\"sentiment\":\"negative\"}")
	}
	if len(labels) > maxLabelKeys {
		return "", fmt.Errorf("too many labels (%d). keep it to %d or fewer so the breakdowns stay readable", len(labels), maxLabelKeys)
	}
	// the conversation must actually exist, or the label counts a conversation nobody had.
	if !hasConversation(evs, cid) {
		return "", fmt.Errorf("no %s events for conversation_id %q, so there is nothing to label. list real conversations with sample_conversations", agent.EventTurn, cid)
	}

	props := map[string]any{
		agent.PropConversationID: cid,
		agent.PropLabeledBy:      by,
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic validation order: the same bad input always names the same key first
	for _, k := range keys {
		key := strings.TrimSpace(k)
		switch {
		case key == "":
			return "", fmt.Errorf("label names cannot be blank")
		case len(key) > maxLabelKeyLen:
			return "", fmt.Errorf("label name %q is too long (max %d characters)", key, maxLabelKeyLen)
		case strings.HasPrefix(key, "$"):
			return "", fmt.Errorf("label name %q cannot start with $ (that prefix is reserved for autocapture)", key)
		case key == agent.PropConversationID || key == agent.PropLabeledBy || key == agent.PropLabeledAt:
			return "", fmt.Errorf("%q is reserved on an agent_label event. pick a different label name", key)
		}
		v, verr := scalarLabelValue(key, labels[k])
		if verr != nil {
			return "", verr
		}
		props[key] = v
	}
	now := time.Now().UTC()
	props[agent.PropLabeledAt] = now.Format(time.RFC3339)

	ev := event.Event{
		ID:         newEventID(),
		Name:       agent.EventLabel,
		DistinctID: cid,
		Timestamp:  now,
		Properties: props,
	}
	if err := s.store.Ingest(ev); err != nil {
		return "", fmt.Errorf("could not write the label: %v", err)
	}
	written := map[string]any{}
	for k, v := range props {
		if k == agent.PropConversationID || k == agent.PropLabeledBy || k == agent.PropLabeledAt {
			continue
		}
		written[k] = v
	}
	return jsonText(map[string]any{
		"labeled":         true,
		"event":           agent.EventLabel,
		"conversation_id": cid,
		"labels":          written,
		"labeled_by":      by,
		"labeled_at":      props[agent.PropLabeledAt],
		"note":            "written as a new agent_label event. no existing event was changed. these labels are your model's inference, not a measured fact, and agent_labels reports them that way (it names labeled_by and counts what is still unlabeled). read the breakdown with agent_labels.",
	})
}

// scalarLabelValue keeps label values to plain scalars. A nested object or array would be
// stringified into a bucket key nobody can read or filter on, so it's rejected with guidance
// instead of silently producing junk in the breakdown.
func scalarLabelValue(key string, v any) (any, error) {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil, fmt.Errorf("label %q has an empty value. drop the label instead of labelling it blank", key)
		}
		if len(s) > maxLabelValueLen {
			return nil, fmt.Errorf("label %q is too long (max %d characters). labels are for grouping, keep them to a short value like \"billing\" or \"high\"", key, maxLabelValueLen)
		}
		return s, nil
	case bool, float64, float32, int, int64:
		return t, nil
	case nil:
		return nil, fmt.Errorf("label %q has a null value. drop the label instead of labelling it null", key)
	default:
		return nil, fmt.Errorf("label %q must be a string, number or boolean, not a nested object or list. summarize it into one short value", key)
	}
}

// hasConversation reports whether any agent_turn event carries this conversation_id. Runs over
// the RAW event set (not the default production scope) so a conversation recorded in a dev
// environment can still be labeled.
func hasConversation(evs []event.Event, cid string) bool {
	for _, e := range evs {
		if e.Name != agent.EventTurn {
			continue
		}
		if v, ok := e.Properties[agent.PropConversationID]; ok && toLabelString(v) == cid {
			return true
		}
	}
	return false
}

// toLabelString stringifies a property the same way the agent package's bucketing does, so a
// conversation_id sent as a number still matches the id shown in the sample.
func toLabelString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
