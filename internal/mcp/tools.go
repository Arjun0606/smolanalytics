package mcp

// toolList is the tools/list payload — the menu the user's model sees. Descriptions
// are written FOR the model: tell it when to reach for each tool.
var toolList = []map[string]any{
	{
		"name":        "overview",
		"description": "Headline numbers for the product: total users, active users in the last 7 days, total events, and the list of event names being tracked. Call this first to orient.",
		"inputSchema": obj(nil, nil),
	},
	{
		"name":        "list_events",
		"description": "List the distinct event names being tracked. Use this to discover what funnels/trends/breakdowns are possible before calling the other tools.",
		"inputSchema": obj(nil, nil),
	},
	{
		"name":        "funnel",
		"description": "Compute an ordered conversion funnel: of the users who did the first step, how many went on to each later step, and where they drop off. Use for 'what's my conversion', 'where do users drop off', 'how many signups become customers'.",
		"inputSchema": obj(map[string]any{
			"steps": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Ordered event names, e.g. [\"signup\",\"activate\",\"checkout\"]. At least two.",
			},
			"window_hours": map[string]any{
				"type":        "number",
				"description": "Conversion window in hours from the first step (default 168 = 7 days).",
			},
		}, []string{"steps"}),
	},
	{
		"name":        "retention",
		"description": "Cohort retention: group users by the day they first appeared and measure what % return on day 1..N. Use for 'do users come back', 'what's my retention', 'are users sticking'.",
		"inputSchema": obj(map[string]any{
			"event": map[string]any{
				"type":        "string",
				"description": "Which event counts as a return/active (e.g. \"open\"). Empty = any event.",
			},
			"days": map[string]any{
				"type":        "number",
				"description": "How many days to measure (default 7).",
			},
		}, nil),
	},
	{
		"name":        "trends",
		"description": "Daily time series for an event: how many happened each day (or unique users per day). Use for 'how many signups', 'is growth up', 'plot X over time'.",
		"inputSchema": obj(map[string]any{
			"event":  map[string]any{"type": "string", "description": "Event name, e.g. \"signup\". Empty = all events."},
			"unique": map[string]any{"type": "boolean", "description": "Count distinct users per day instead of raw events."},
		}, nil),
	},
	{
		"name":        "breakdown",
		"description": "Segment an event by one of its properties — counts per property value, sorted desc. Use for 'where do signups come from', 'break down checkout by plan', 'top sources'.",
		"inputSchema": obj(map[string]any{
			"event":    map[string]any{"type": "string", "description": "Event to break down, e.g. \"signup\". Empty = all events."},
			"property": map[string]any{"type": "string", "description": "Property to group by, e.g. \"source\" or \"plan\"."},
		}, []string{"property"}),
	},
	{
		"name":        "recent_events",
		"description": "The most recent raw events (newest first) with their properties. Use to debug instrumentation ('did my signup event arrive', 'what's coming in right now') or to eyeball live activity.",
		"inputSchema": obj(map[string]any{
			"limit": map[string]any{"type": "number", "description": "How many recent events (default 20)."},
		}, nil),
	},
	{
		"name":        "user_activity",
		"description": "One user's full timeline: event counts, first/last seen, and latest known traits. Use for 'what did user X do', 'when did this user sign up', 'is this account active'.",
		"inputSchema": obj(map[string]any{
			"distinct_id": map[string]any{"type": "string", "description": "The user/visitor id to look up."},
		}, []string{"distinct_id"}),
	},
}

func obj(props map[string]any, required []string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
