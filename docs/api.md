# The stats API

Every report the dashboard and MCP can compute is also a plain `GET` — same engine,
same numbers (CI-enforced). Authenticate with your API key (the write key, or any
key from Settings → API keys):

```sh
export HOST=https://your-instance
export KEY=sa_...

# web overview: visitors, live-now, top pages, referrers, UTM, devices, bounce, AI channel
curl -H "Authorization: Bearer $KEY" "$HOST/v1/web?days=30"

# funnel with a property filter
curl -H "Authorization: Bearer $KEY" \
  "$HOST/v1/funnel?steps=signup,activate,checkout&filters=$(python3 -c 'import urllib.parse;print(urllib.parse.quote("[{\"property\":\"plan\",\"op\":\"eq\",\"value\":\"pro\"}]"))')"

# trends / retention / breakdown / paths / lifecycle / stickiness / groups
curl -H "Authorization: Bearer $KEY" "$HOST/v1/trends?event=signup"
curl -H "Authorization: Bearer $KEY" "$HOST/v1/retention?days=7&event=signup"
curl -H "Authorization: Bearer $KEY" "$HOST/v1/breakdown?event=signup&property=source"

# the verdict (what to look at) and usage counters
curl -H "Authorization: Bearer $KEY" "$HOST/v1/notable"
curl -H "Authorization: Bearer $KEY" "$HOST/v1/usage"

# the morning brief as JSON — pulse + findings, per-site breakdown (?days=1..90, default 7)
curl -H "Authorization: Bearer $KEY" "$HOST/v1/brief?days=7"

# full export — CSV or JSONL (JSONL round-trips straight back into /v1/events)
curl -H "Authorization: Bearer $KEY" "$HOST/v1/export?format=jsonl" -o events.jsonl
```

Semantics shared by every endpoint:

- **Production scope by default** — events stamped `env=development` are excluded
  unless a filter explicitly references `env`.
- **Filters** — `filters=<url-encoded JSON array>` of `{property, op, value}`;
  ops: `eq, neq, contains, gt, lt`. `site` and `env` are ordinary properties.
- **Keys are read-only here** — they authorize ingest (`POST /v1/events`), MCP, and
  `GET` reports. Settings and destructive routes always require the dashboard session.
- Unknown event/property names return a `4xx` with the list of real names — never
  silent zeros.
