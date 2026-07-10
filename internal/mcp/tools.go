package mcp

// filtersSchema documents the segmentation predicate every report accepts:
// AND-combined conditions over event properties — the full array form, or an
// equality-shorthand map (decoded by FilterSet).
var filtersSchema = map[string]any{
	"description": "Optional segmentation. AND-combined conditions over event properties: an array like [{\"property\":\"plan\",\"op\":\"eq\",\"value\":\"pro\"}]. Ops: eq, neq, contains, gt, lt, in (value is a list — OR over one property, e.g. source in [\"hn\",\"twitter\"]), notin, set (property exists, no value), notset (property missing). Combine with AND for queries like \"pro users from HN or Twitter\": [{\"property\":\"plan\",\"op\":\"eq\",\"value\":\"pro\"},{\"property\":\"source\",\"op\":\"in\",\"value\":[\"hn\",\"twitter\"]}]. Or an equality shorthand map like {\"plan\":\"pro\"}.",
	"anyOf": []map[string]any{
		{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"property": map[string]any{"type": "string"},
					"op":       map[string]any{"type": "string", "enum": []string{"eq", "neq", "contains", "gt", "lt", "in", "notin", "set", "notset"}},
					"value":    map[string]any{},
				},
			},
		},
		{
			"type":        "object",
			"description": "Equality shorthand: each key is a property, each scalar value the required value.",
		},
	},
}

// toolList is the tools/list payload — the menu the user's model sees. Descriptions
// are written FOR the model: tell it when to reach for each tool.
var toolList = []map[string]any{
	{
		"name":        "whats_notable",
		"description": "Proactive digest: the most important things happening right now — sudden 24h drops/spikes, the biggest drop-off in the (auto-detected) user journey, WHICH segment converts worst through that step, week-over-week change in the headline event, and the retention read — each computed exactly. Call this for open-ended asks like 'how's it going?', 'what's broken?', 'what should I fix?', 'anything I should worry about?'.",
		"inputSchema": obj(nil, nil),
	},
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
		"description": "Cohort retention: group users by when they first appeared and measure what % return on period 1..N. Defaults to daily; set bucket \"week\" or \"month\" for a weekly/monthly product (daily retention understates a weekly product). rolling=true gives unbounded 'active on or after period n' retention. Use for 'do users come back', 'weekly retention', 'are users sticking'.",
		"inputSchema": obj(map[string]any{
			"event": map[string]any{
				"type":        "string",
				"description": "Which event counts as a return/active (e.g. \"open\"). Empty = any event.",
			},
			"days": map[string]any{
				"type":        "number",
				"description": "How many periods to measure (default 7).",
			},
			"bucket": map[string]any{
				"type":        "string",
				"description": "Period size: \"day\" (default), \"week\" (7-day blocks), or \"month\" (30-day blocks). Use week/month for a weekly/monthly product.",
			},
			"rolling": map[string]any{
				"type":        "boolean",
				"description": "true = unbounded retention (active on period n OR any later period), instead of exactly on period n.",
			},
			"filters": filtersSchema,
		}, nil),
	},
	{
		"name":        "trends",
		"description": "Daily time series for an event: how many happened each day (or unique users per day). With breakdown set, returns one line per property value. With measure+property set, aggregates a NUMERIC property per day (sum/avg/min/max/median/p90) — use this for revenue ('measure=sum, property=amount'), average order value ('measure=avg'), p90 latency, etc. Use for 'how many signups', 'total revenue per day', 'average order value', 'signups by source over time'.",
		"inputSchema": obj(map[string]any{
			"event":     map[string]any{"type": "string", "description": "Event name, e.g. \"signup\". Empty = all events."},
			"unique":    map[string]any{"type": "boolean", "description": "Count distinct users per day instead of raw events."},
			"breakdown": map[string]any{"type": "string", "description": "Optional property to split into one series per value, e.g. \"source\"."},
			"measure":   map[string]any{"type": "string", "description": "Numeric aggregation over `property`: sum, avg, min, max, median, or p90. Requires property. Example: revenue = measure \"sum\" of property \"amount\"."},
			"property":  map[string]any{"type": "string", "description": "The numeric property to aggregate when `measure` is set, e.g. \"amount\" or \"duration_ms\"."},
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
		"name":        "web_overview",
		"description": "The web-analytics view: unique visitors, pageviews, LIVE visitors right now, top pages, referrers (grouped by host), UTM sources, and device split — from autocaptured $pageview events. Use for 'how's traffic', 'where do visitors come from', 'top pages', 'how many people are on the site right now'.",
		"inputSchema": obj(map[string]any{
			"days":    map[string]any{"type": "number", "description": "Trailing window in days (default 30)."},
			"filters": filtersSchema,
		}, nil),
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
