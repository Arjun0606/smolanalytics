package mcp

// filtersSchema documents the segmentation predicate array every report accepts:
// AND-combined conditions over event properties.
var filtersSchema = map[string]any{
	"type":        "array",
	"description": "Optional segmentation. AND-combined conditions over event properties, e.g. [{\"property\":\"plan\",\"op\":\"eq\",\"value\":\"pro\"}]. Ops: eq, neq, contains, gt, lt.",
	"items": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"property": map[string]any{"type": "string"},
			"op":       map[string]any{"type": "string", "enum": []string{"eq", "neq", "contains", "gt", "lt"}},
			"value":    map[string]any{},
		},
	},
}

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
			"filters": filtersSchema,
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
			"filters": filtersSchema,
		}, nil),
	},
	{
		"name":        "trends",
		"description": "Daily time series for an event: how many happened each day (or unique users per day). With breakdown set, returns one line per property value. Use for 'how many signups', 'signups by source over time', 'is growth up'.",
		"inputSchema": obj(map[string]any{
			"event":     map[string]any{"type": "string", "description": "Event name, e.g. \"signup\". Empty = all events."},
			"unique":    map[string]any{"type": "boolean", "description": "Count distinct users per day instead of raw events."},
			"breakdown": map[string]any{"type": "string", "description": "Optional property to split into one series per value, e.g. \"source\"."},
			"filters":   filtersSchema,
		}, nil),
	},
	{
		"name":        "breakdown",
		"description": "Segment an event by one of its properties — counts per property value, sorted desc. Use for 'where do signups come from', 'break down checkout by plan', 'top sources'.",
		"inputSchema": obj(map[string]any{
			"event":    map[string]any{"type": "string", "description": "Event to break down, e.g. \"signup\". Empty = all events."},
			"property": map[string]any{"type": "string", "description": "Property to group by, e.g. \"source\" or \"plan\"."},
			"filters":  filtersSchema,
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
	{
		"name":        "lifecycle",
		"description": "Daily lifecycle breakdown: how many users are new, returning, resurrected, or went dormant each day. Use for 'are we growing or churning', 'how many users came back', 'new vs returning'.",
		"inputSchema": obj(map[string]any{
			"days":    map[string]any{"type": "number", "description": "How many trailing days (default 30)."},
			"filters": filtersSchema,
		}, nil),
	},
	{
		"name":        "stickiness",
		"description": "Engagement ratio: daily/weekly/monthly active users (DAU/WAU/MAU) and the DAU/MAU stickiness ratio. Use for 'how engaged are users', 'DAU', 'how sticky is the product'.",
		"inputSchema": obj(map[string]any{
			"filters": filtersSchema,
		}, nil),
	},
	{
		"name":        "paths",
		"description": "User flows: after a start event, what do users do next (ranked at each step)? Use for 'what do users do after signup', 'where do users go from the pricing page', 'common paths after X'.",
		"inputSchema": obj(map[string]any{
			"start":   map[string]any{"type": "string", "description": "The event to start the flow from, e.g. \"signup\"."},
			"depth":   map[string]any{"type": "number", "description": "How many steps to follow (default 3)."},
			"filters": filtersSchema,
		}, []string{"start"}),
	},
	{
		"name":        "groups",
		"description": "Account-level (B2B) analytics: roll events up by a group property (company, account_id, team) — total accounts, active accounts (7d/30d), and the most active accounts with their user + event counts. Use for 'which companies are most active', 'how many accounts', 'account engagement'.",
		"inputSchema": obj(map[string]any{
			"property": map[string]any{"type": "string", "description": "The group key, e.g. \"company\" or \"account_id\"."},
			"limit":    map[string]any{"type": "number", "description": "Max accounts to return (default 50)."},
			"filters":  filtersSchema,
		}, []string{"property"}),
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
