package agent

// The BYO-model conversation-intelligence seam. Competitors run their own LLM over every
// conversation server-side and meter you per message; we never call a model at all. Instead the
// user's OWN model (already connected over MCP) reads a SAMPLE of conversations, infers labels
// (intent, sentiment, frustration...), and writes them back as ordinary events. Then the same
// computed engine breaks down over those labels.
//
// The honesty line runs right through the middle of this file: the LABEL is a model inference,
// the COUNTS over labels are computed from the event log. Every result carries which model(s)
// wrote the labels and how many conversations are still unlabeled, so nobody can mistake an
// inferred label for a measured fact. Labels never mutate anything — the store is append-only,
// so a label is a NEW agent_label event joined to the conversation by conversation_id.

import (
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// EventLabel is the third reserved agent event name: one model-written label set for one
// conversation. Like the other two it is deliberately NOT $-prefixed.
const EventLabel = "agent_label"

// Reserved properties on an agent_label event. Everything else on it is a label.
const (
	PropConversationID = "conversation_id"
	PropLabeledBy      = "labeled_by" // the model that inferred the labels — required, never blank
	PropLabeledAt      = "labeled_at" // RFC3339 stamp of when the label was written
)

// Sampling caps: a model has to be able to read the result in one go, so the sample is bounded
// on every axis (conversations, turns per conversation, characters per turn) and the caller is
// told exactly what was left out.
const (
	DefaultSampleLimit = 20
	MaxSampleLimit     = 100
	MaxSampleTurns     = 40
	MaxTextRunes       = 500
)

// SampledTurn is one turn as the user's model sees it. Text is only ever what the app actually
// sent on the turn: a turn with no `text` property comes back with role and timing only, and
// HasText false. We never invent conversation content.
type SampledTurn struct {
	Role          string    `json:"role"`
	Timestamp     time.Time `json:"timestamp"`
	HasText       bool      `json:"has_text"`
	Text          string    `json:"text,omitempty"`
	TextTruncated bool      `json:"text_truncated,omitempty"`
}

// SampledConversation is one whole conversation, turns oldest-first, ready to be read and labeled.
type SampledConversation struct {
	ConversationID string        `json:"conversation_id"`
	TurnCount      int           `json:"turn_count"` // real number of turns, even if some were dropped
	Start          time.Time     `json:"start"`
	End            time.Time     `json:"end"`
	Labeled        bool          `json:"labeled"` // already carries at least one agent_label event
	Turns          []SampledTurn `json:"turns"`
	TurnsOmitted   int           `json:"turns_omitted,omitempty"`
}

// ConversationSample is what sample_conversations returns: whole conversations plus the counts
// that keep it honest (how many exist, how many came back, how many turns actually had text).
type ConversationSample struct {
	Event            string                `json:"event"`
	Conversations    []SampledConversation `json:"conversations"`
	Returned         int                   `json:"returned"`
	Total            int                   `json:"total"` // conversations in the window, before the cap
	Limit            int                   `json:"limit"`
	TurnsWithText    int                   `json:"turns_with_text"`
	TurnsWithoutText int                   `json:"turns_without_text"`
	Note             string                `json:"note"`
}

// SampleConversations returns whole conversations for the user's own model to read: agent_turn
// events in [from, to) grouped by conversation_id, turns ordered oldest-first inside each
// conversation, conversations ordered most-recent-first (last turn descending, conversation_id
// ascending as the tie-break) so the same input always produces the same sample — no randomness
// anywhere. limit defaults to 20 and is capped at 100; turns per conversation are capped and long
// text is truncated so a model can actually consume the result. A turn without a `text` property
// comes back without text; we never fabricate what was said.
func SampleConversations(events []event.Event, from, to time.Time, limit int) ConversationSample {
	if limit <= 0 {
		limit = DefaultSampleLimit
	}
	if limit > MaxSampleLimit {
		limit = MaxSampleLimit
	}
	out := ConversationSample{Event: EventTurn, Conversations: []SampledConversation{}, Limit: limit}

	byConv := map[string][]event.Event{}
	labeled := map[string]bool{}
	for _, e := range events {
		switch e.Name {
		case EventTurn:
			if !inWindow(e, from, to) {
				continue
			}
			cid := "(none)"
			if v, ok := e.Properties[PropConversationID]; ok {
				cid = valueOf(v)
			}
			byConv[cid] = append(byConv[cid], e)
		case EventLabel:
			// labels are joined by conversation_id whenever they were written, so a
			// conversation labeled today still reads as labeled inside an older window.
			if cid := valueOf(e.Properties[PropConversationID]); cid != "" {
				labeled[cid] = true
			}
		}
	}
	out.Total = len(byConv)

	convs := make([]SampledConversation, 0, len(byConv))
	for cid, evs := range byConv {
		sort.SliceStable(evs, func(i, j int) bool { return evs[i].Timestamp.Before(evs[j].Timestamp) })
		c := SampledConversation{
			ConversationID: cid,
			TurnCount:      len(evs),
			Start:          evs[0].Timestamp.UTC(),
			End:            evs[len(evs)-1].Timestamp.UTC(),
			Labeled:        labeled[cid],
			Turns:          []SampledTurn{},
		}
		keep := evs
		if len(keep) > MaxSampleTurns {
			keep = keep[:MaxSampleTurns]
			c.TurnsOmitted = len(evs) - MaxSampleTurns
		}
		for _, e := range keep {
			t := SampledTurn{Role: valueOf(e.Properties["role"]), Timestamp: e.Timestamp.UTC()}
			if raw, ok := e.Properties["text"]; ok {
				if s, isStr := raw.(string); isStr && s != "" {
					t.HasText = true
					t.Text, t.TextTruncated = truncateRunes(s, MaxTextRunes)
				}
			}
			c.Turns = append(c.Turns, t)
		}
		convs = append(convs, c)
	}
	// deterministic: most recent conversation first, id ascending as the tie-break.
	sort.Slice(convs, func(i, j int) bool {
		if !convs[i].End.Equal(convs[j].End) {
			return convs[i].End.After(convs[j].End)
		}
		return convs[i].ConversationID < convs[j].ConversationID
	})
	if len(convs) > limit {
		convs = convs[:limit]
	}
	out.Conversations = convs
	out.Returned = len(convs)
	for _, c := range convs {
		for _, t := range c.Turns {
			if t.HasText {
				out.TurnsWithText++
			} else {
				out.TurnsWithoutText++
			}
		}
	}

	notes := []string{"these are your own agent_turn events, grouped by conversation_id, turns oldest first. read them, infer labels, then write each one back with label_conversation. labels are your model's inference, the counts over them stay computed."}
	if out.Returned == 0 {
		notes = append(notes, "no agent_turn events in this window, so there is nothing to label yet.")
	} else if out.TurnsWithText == 0 {
		notes = append(notes, "no turn here carries a text property, so only roles and timing are shown. we never invent turn content. send text on agent_turn if you want labels inferred from what was said.")
	}
	if out.Total > out.Returned {
		notes = append(notes, "this is a sample, not the full set. raise limit (max 100) or narrow the window to read more.")
	}
	out.Note = strings.Join(notes, " ")
	return out
}

// LabelValue is one value of a label and how many conversations carry it.
type LabelValue struct {
	Value         string `json:"value"`
	Conversations int    `json:"conversations"`
}

// LabelBreakdown is the conversation count per label value, wrapped in the honesty context that
// makes it safe to read: which model(s) wrote the labels, and how many conversations in the
// window are still unlabeled. Inferred is always true — the VALUES are a model's judgement; the
// counts are computed from the event log.
type LabelBreakdown struct {
	Event         string       `json:"event"`
	Label         string       `json:"label"`
	Values        []LabelValue `json:"values"`
	Conversations int          `json:"conversations"` // conversations in the window
	Labeled       int          `json:"labeled"`       // ...carrying this label
	Unlabeled     int          `json:"unlabeled"`
	LabeledBy     []string     `json:"labeled_by"` // distinct models behind the counted labels
	Inferred      bool         `json:"inferred"`
	Note          string       `json:"note"`
}

// ComputeLabelBreakdown counts conversations per value of one label. Conversations come from
// agent_turn events in [from, to); labels come from agent_label events joined on
// conversation_id, regardless of when the label was written (a label is a statement about a
// conversation, not something that happened at that moment — windowing it would silently drop
// last week's conversations the moment you label them today). When a conversation was labeled
// more than once for the same label, the most recent write wins (event id breaks a timestamp
// tie, so the result is deterministic). Buckets and ordering come from query.Breakdown, the same
// grouping every other report uses.
//
// Nothing here is invented: an unlabeled set returns an honest empty result that says how to
// label, and every result carries labeled/unlabeled counts plus the labelling model(s).
func ComputeLabelBreakdown(events []event.Event, label string, from, to time.Time) LabelBreakdown {
	label = strings.TrimSpace(label)
	out := LabelBreakdown{Event: EventLabel, Label: label, Values: []LabelValue{},
		LabeledBy: []string{}, Inferred: true}
	if label == "" {
		out.Note = "no label given. pass a label name, e.g. intent, sentiment or frustration: whatever your model wrote with label_conversation."
		return out
	}

	convs := map[string]bool{}
	for _, e := range events {
		if e.Name != EventTurn || !inWindow(e, from, to) {
			continue
		}
		cid := "(none)"
		if v, ok := e.Properties[PropConversationID]; ok {
			cid = valueOf(v)
		}
		convs[cid] = true
	}
	out.Conversations = len(convs)

	// latest label write per conversation wins
	type write struct {
		value string
		by    string
		at    time.Time
		evID  string
	}
	winner := map[string]write{}
	for _, e := range events {
		if e.Name != EventLabel {
			continue
		}
		cid := valueOf(e.Properties[PropConversationID])
		if cid == "" || !convs[cid] {
			continue // a label for a conversation outside this window counts for nothing here
		}
		raw, ok := e.Properties[label]
		if !ok {
			continue
		}
		w := write{value: valueOf(raw), by: valueOf(e.Properties[PropLabeledBy]), at: e.Timestamp.UTC(), evID: e.ID}
		cur, seen := winner[cid]
		if !seen || w.at.After(cur.at) || (w.at.Equal(cur.at) && w.evID > cur.evID) {
			winner[cid] = w
		}
	}
	out.Labeled = len(winner)
	out.Unlabeled = out.Conversations - out.Labeled

	// one pseudo-event per labeled conversation, then the shared Breakdown — so label buckets
	// and their ordering (count desc, value asc) are the exact same code every report uses.
	cids := make([]string, 0, len(winner))
	for cid := range winner {
		cids = append(cids, cid)
	}
	sort.Strings(cids)
	pseudo := make([]event.Event, 0, len(cids))
	models := map[string]bool{}
	for _, cid := range cids {
		w := winner[cid]
		pseudo = append(pseudo, event.Event{Name: EventLabel, DistinctID: cid,
			Properties: map[string]any{label: w.value}})
		if w.by != "" {
			models[w.by] = true
		}
	}
	for _, g := range query.Breakdown(pseudo, label) {
		out.Values = append(out.Values, LabelValue{Value: g.Value, Conversations: g.Count})
	}
	for m := range models {
		out.LabeledBy = append(out.LabeledBy, m)
	}
	sort.Strings(out.LabeledBy)

	notes := []string{"the values are inferences written by the model(s) in labeled_by, not measured facts. the conversation counts over them are computed from your event log."}
	notes = append(notes, "labels join to conversations by conversation_id whenever they were written; the window scopes which conversations are counted.")
	if out.Labeled == 0 {
		notes = append(notes, "nothing carries this label yet. call sample_conversations to read a sample, then label_conversation to write labels back as agent_label events.")
	} else if out.Unlabeled > 0 {
		notes = append(notes, "the unlabeled conversations are not counted in any value above.")
	}
	out.Note = strings.Join(notes, " ")
	return out
}

// truncateRunes cuts s to at most n runes (rune-safe, so a multi-byte character is never split),
// reporting whether anything was dropped. Truncation is visible in the payload (text_truncated),
// never silent.
func truncateRunes(s string, n int) (string, bool) {
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	return string(r[:n]), true
}
