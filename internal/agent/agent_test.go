package agent

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/trends"
)

// tc builds one agent_tool_call event.
func tc(tool, client string, latency float64, isErr bool, errType string) event.Event {
	p := map[string]any{"tool": tool, "client": client, "latency_ms": latency, "error": isErr}
	if errType != "" {
		p["error_type"] = errType
	}
	return event.Event{Name: EventToolCall, DistinctID: "u", Timestamp: time.Now().UTC(), Properties: p}
}

// tn builds one agent_turn event at a given offset so ordering is deterministic.
func tn(cid, role string, offset time.Duration, extra map[string]any) event.Event {
	p := map[string]any{"conversation_id": cid, "role": role}
	for k, v := range extra {
		p[k] = v
	}
	return event.Event{Name: EventTurn, DistinctID: "u", Timestamp: time.Unix(1_700_000_000, 0).UTC().Add(offset), Properties: p}
}

func TestToolHealthDeterministic(t *testing.T) {
	evs := []event.Event{
		tc("search", "cursor", 100, false, ""),
		tc("search", "cursor", 200, true, "timeout"),
		tc("search", "claude-code", 300, false, ""),
		tc("write", "cursor", 50, false, ""),
	}
	a := ComputeToolHealth(evs, time.Time{}, time.Time{})
	b := ComputeToolHealth(evs, time.Time{}, time.Time{})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("ComputeToolHealth not deterministic:\n%+v\n%+v", a, b)
	}
	if a.Calls != 4 || a.Errors != 1 {
		t.Fatalf("totals: got calls=%d errors=%d want 4/1", a.Calls, a.Errors)
	}
	// busiest tool first
	if a.Tools[0].Tool != "search" || a.Tools[0].Calls != 3 {
		t.Fatalf("expected search(3) first, got %+v", a.Tools[0])
	}
	s := a.Tools[0]
	if s.Errors != 1 || math.Abs(s.ErrorRate-1.0/3.0) > 1e-9 {
		t.Fatalf("search error stats wrong: %+v", s)
	}
	// latency p90 nearest-rank of [100,200,300] = index ceil(.9*3)-1 = 2 -> 300
	if s.LatencyP90 != 300 || s.LatencyP50 != 200 {
		t.Fatalf("search latency wrong: p50=%v p90=%v", s.LatencyP50, s.LatencyP90)
	}
	// client split: cursor(2) before claude-code(1)
	if len(s.Clients) != 2 || s.Clients[0].Client != "cursor" || s.Clients[0].Calls != 2 {
		t.Fatalf("client split wrong: %+v", s.Clients)
	}
}

func TestToolHealthPercentileEqualsHandComputed(t *testing.T) {
	vals := []float64{5, 1, 9, 3, 7, 2, 8, 4, 6, 10}
	var evs []event.Event
	for _, v := range vals {
		evs = append(evs, tc("x", "cursor", v, false, ""))
	}
	got := ComputeToolHealth(evs, time.Time{}, time.Time{}).Tools[0]
	// hand-computed nearest-rank over the exact same slice via the shared exported function
	if got.LatencyP50 != trends.Percentile(vals, 0.50) ||
		got.LatencyP90 != trends.Percentile(vals, 0.90) ||
		got.LatencyP99 != trends.Percentile(vals, 0.99) {
		t.Fatalf("latency percentiles diverged from trends.Percentile: %+v", got)
	}
	// nearest-rank literals: sorted 1..10, p90 -> ceil(.9*10)-1 = idx 8 -> 9; p99 -> idx 9 -> 10; p50 -> idx 4 -> 5
	if got.LatencyP50 != 5 || got.LatencyP90 != 9 || got.LatencyP99 != 10 {
		t.Fatalf("nearest-rank wrong: p50=%v p90=%v p99=%v", got.LatencyP50, got.LatencyP90, got.LatencyP99)
	}
}

func TestErrorTaxonomyTypoToolEmptyNotError(t *testing.T) {
	evs := []event.Event{
		tc("search", "cursor", 100, true, "timeout"),
		tc("search", "cursor", 100, true, "timeout"),
		tc("search", "cursor", 100, true, "rate_limit"),
		tc("search", "cursor", 100, false, ""),
	}
	all := ComputeErrorTaxonomy(evs, "", time.Time{}, time.Time{})
	if all.Total != 3 {
		t.Fatalf("expected 3 error calls, got %d", all.Total)
	}
	if all.Types[0].ErrorType != "timeout" || all.Types[0].Count != 2 {
		t.Fatalf("expected timeout(2) first, got %+v", all.Types)
	}
	// a typo'd / unseen tool matches nothing: honest empty, never an error, never nil slice
	typo := ComputeErrorTaxonomy(evs, "serch", time.Time{}, time.Time{})
	if typo.Total != 0 || len(typo.Types) != 0 {
		t.Fatalf("typo tool should yield empty taxonomy, got %+v", typo)
	}
}

func TestConversationsReAskAbandon(t *testing.T) {
	// c1: user, assistant, user, user  -> re-ask (consecutive users) AND abandon (ends user)
	// c2: user, assistant              -> neither
	// c3: user, user                   -> re-ask AND abandon
	evs := []event.Event{
		tn("c1", "user", 1, nil), tn("c1", "assistant", 2, nil), tn("c1", "user", 3, nil), tn("c1", "user", 4, nil),
		tn("c2", "user", 1, nil), tn("c2", "assistant", 2, nil),
		tn("c3", "user", 1, nil), tn("c3", "user", 2, nil),
	}
	c := ComputeConversations(evs, time.Time{}, time.Time{})
	if c.Conversations != 3 || c.Turns != 8 {
		t.Fatalf("counts wrong: convs=%d turns=%d", c.Conversations, c.Turns)
	}
	// re-ask: c1 and c3 -> 2/3
	if math.Abs(c.ReAskRate-2.0/3.0) > 1e-9 {
		t.Fatalf("reask rate wrong: %v", c.ReAskRate)
	}
	// abandon: c1 and c3 end on user -> 2/3
	if math.Abs(c.AbandonRate-2.0/3.0) > 1e-9 {
		t.Fatalf("abandon rate wrong: %v", c.AbandonRate)
	}
	// turns per conv: [4,2,2] -> engine median of sorted [2,2,4] -> 2
	if c.MedianTurns != 2 {
		t.Fatalf("median turns wrong: %v", c.MedianTurns)
	}
}

func TestConversationsReAskNeedsNoAssistantBetween(t *testing.T) {
	// user, assistant, user: the two user turns have an assistant between -> NOT a re-ask
	evs := []event.Event{
		tn("c1", "user", 1, nil), tn("c1", "assistant", 2, nil), tn("c1", "user", 3, nil),
	}
	c := ComputeConversations(evs, time.Time{}, time.Time{})
	if c.ReAskRate != 0 {
		t.Fatalf("assistant between user turns must not count as re-ask, got %v", c.ReAskRate)
	}
	// ends on user -> abandon
	if c.AbandonRate != 1 {
		t.Fatalf("abandon rate wrong: %v", c.AbandonRate)
	}
}

func TestResolutionRateNilWithoutResolved(t *testing.T) {
	evs := []event.Event{
		tn("c1", "user", 1, nil), tn("c1", "assistant", 2, nil),
		tn("c2", "user", 1, nil), tn("c2", "assistant", 2, nil),
	}
	c := ComputeConversations(evs, time.Time{}, time.Time{})
	if c.ResolutionRate != nil {
		t.Fatalf("ResolutionRate must be nil when no turn carries `resolved`, got %v", *c.ResolutionRate)
	}
}

func TestResolutionRateComputedWhenResolvedPresent(t *testing.T) {
	// c1 resolved=true, c2 resolved=false -> 1/2
	evs := []event.Event{
		tn("c1", "user", 1, nil), tn("c1", "assistant", 2, map[string]any{"resolved": true}),
		tn("c2", "user", 1, nil), tn("c2", "assistant", 2, map[string]any{"resolved": false}),
	}
	c := ComputeConversations(evs, time.Time{}, time.Time{})
	if c.ResolutionRate == nil {
		t.Fatalf("ResolutionRate must be computed when a turn carries `resolved`")
	}
	if math.Abs(*c.ResolutionRate-0.5) > 1e-9 {
		t.Fatalf("resolution rate wrong: %v", *c.ResolutionRate)
	}
}

func TestConversationsTTFTPercentiles(t *testing.T) {
	evs := []event.Event{
		tn("c1", "user", 1, nil), tn("c1", "assistant", 2, map[string]any{"ttft_ms": float64(100)}),
		tn("c2", "user", 1, nil), tn("c2", "assistant", 2, map[string]any{"ttft_ms": float64(300)}),
		tn("c3", "user", 1, nil), tn("c3", "assistant", 2, map[string]any{"ttft_ms": float64(200)}),
	}
	c := ComputeConversations(evs, time.Time{}, time.Time{})
	// ttft [100,300,200] sorted [100,200,300]: p50 idx ceil(.5*3)-1=1 ->200, p90 idx2 ->300
	if c.TTFTP50 != 200 || c.TTFTP90 != 300 {
		t.Fatalf("ttft percentiles wrong: p50=%v p90=%v", c.TTFTP50, c.TTFTP90)
	}
}

func TestEmptyReportsAreHonestNotError(t *testing.T) {
	// no agent events at all: zero-valued reports, nil-safe, ResolutionRate nil
	th := ComputeToolHealth(nil, time.Time{}, time.Time{})
	if th.Calls != 0 || len(th.Tools) != 0 {
		t.Fatalf("empty tool health should be zero, got %+v", th)
	}
	c := ComputeConversations(nil, time.Time{}, time.Time{})
	if c.Conversations != 0 || c.ResolutionRate != nil {
		t.Fatalf("empty conversations should be zero + nil resolution, got %+v", c)
	}
}
