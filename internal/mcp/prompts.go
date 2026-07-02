package mcp

// MCP prompts — pre-canned workflows the client surfaces natively (slash-command-like
// in Claude Desktop/Code). Each returns a user message that drives the model through a
// multi-tool routine, so "do my weekly review" is one click instead of prompt-crafting.

import "encoding/json"

var promptList = []map[string]any{
	{
		"name":        "instrument-my-app",
		"description": "Wire smolanalytics into the current codebase, declare a tracking plan, and verify events flow — the full setup loop.",
	},
	{
		"name":        "whats-broken-today",
		"description": "The morning check: anomalies, funnel leaks, and instrumentation health, with what to do about each.",
	},
	{
		"name":        "weekly-review",
		"description": "A founder-grade weekly product review: growth, conversion, retention, traffic — with the one thing to fix next week.",
	},
}

var promptText = map[string]string{
	"instrument-my-app": `Set up smolanalytics for this codebase, end to end:
1. Look at the app and decide the 3-6 events that actually matter (a signup-shaped event, an activation moment, the revenue event; page/screen views come free via autocapture on web).
2. Wire the tracking: the <script> snippet + smolanalytics.track() calls for web, or POST /v1/events for backend/mobile. Use a stable distinct_id per user; call identify() on login.
3. Declare what you wired with set_tracking_plan (names, descriptions, expected properties).
4. Tell me to run the app and click through the flows, then verify with instrumentation_health — chase down anything MISSING or any missing_properties until it's healthy.
5. Finish by setting a sensible drop alert on the most important event (create_alert) and telling me exactly what you set up.`,

	"whats-broken-today": `Run my morning check:
1. whats_notable for the verdict (anomalies, biggest funnel leak, retention read).
2. instrumentation_health (last 24h) — is tracking itself healthy?
3. web_overview — anything unusual in traffic or referrers?
Then give me the read like an analyst: what changed, what's broken (product vs tracking), and the single most useful action today. If everything's fine, say so in one line.`,

	"weekly-review": `Do my weekly product review:
1. trends on the headline events, this week vs last (use breakdown by source or plan where it's telling).
2. funnel — where's the biggest leak and did it move?
3. retention — is day-1/day-7 improving for recent cohorts?
4. web_overview — traffic mix shifts worth knowing.
Synthesize: 3-5 bullets a founder actually needs, each with the number and what it means. End with THE one thing to fix next week, and offer to save the most useful view with save_report.`,
}

// dispatchPrompts handles prompts/list and prompts/get. Returns nil when the method
// isn't a prompts method.
func (s *Server) dispatchPrompts(method string, params json.RawMessage, reply func(any) *response, fail func(int, string) *response) *response {
	switch method {
	case "prompts/list":
		return reply(map[string]any{"prompts": promptList})
	case "prompts/get":
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(params, &p)
		text, ok := promptText[p.Name]
		if !ok {
			return fail(-32602, "unknown prompt: "+p.Name)
		}
		return reply(map[string]any{
			"messages": []map[string]any{
				{"role": "user", "content": map[string]any{"type": "text", "text": text}},
			},
		})
	}
	return nil
}
