package demo

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// The product's most defensible claim is that a user's journey and the agent's behaviour are
// ONE dataset, so a funnel can cross from a pageview into a tool call. That is only true on
// screen if the demo seeds them as one population; it previously seeded two disjoint ones and
// the flagship funnel silently returned zero.
func TestAgentEventsAttachToProductUsers(t *testing.T) {
	s := memory.New()
	if err := Seed(s); err != nil {
		t.Fatal(err)
	}
	evs, err := s.Range(time.Time{}, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	product := map[string]bool{}
	agent := map[string]bool{}
	for _, e := range evs {
		switch e.Name {
		case "$pageview", "signup", "activate", "checkout":
			product[e.DistinctID] = true
		case "agent_tool_call", "agent_turn":
			agent[e.DistinctID] = true
			if strings.HasPrefix(e.DistinctID, "au") {
				t.Fatalf("agent event on a separate id namespace (%s) — the joined funnel cannot work", e.DistinctID)
			}
		}
	}
	both := 0
	for id := range agent {
		if product[id] {
			both++
		}
	}
	if both == 0 {
		t.Fatalf("no user has both product and agent events (%d agent users, %d product users)", len(agent), len(product))
	}
	t.Logf("%d users have both product and agent events", both)
}

// The funnel no other analytics tool can compute: a user journey that crosses into the agent's
// own behaviour. PostHog has the product half and not the agent half; LangSmith and friends have
// the agent half and not the product half. It works here because both are the same event log —
// and this test fails the moment the demo stops being able to show it.
func TestFunnelCrossesFromProductIntoAgent(t *testing.T) {
	s := memory.New()
	if err := Seed(s); err != nil {
		t.Fatal(err)
	}
	evs, err := s.Range(time.Time{}, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	perUser := map[string]map[string]bool{}
	for _, e := range evs {
		if perUser[e.DistinctID] == nil {
			perUser[e.DistinctID] = map[string]bool{}
		}
		perUser[e.DistinctID][e.Name] = true
	}
	crossed := 0
	for _, names := range perUser {
		if names["signup"] && names["agent_tool_call"] {
			crossed++
		}
	}
	if crossed == 0 {
		t.Fatal("no user both signed up and made a tool call — the signup -> agent_tool_call funnel renders empty")
	}
	t.Logf("%d users go signup -> agent_tool_call", crossed)
}
