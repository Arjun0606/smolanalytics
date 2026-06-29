#!/bin/sh
# Send events to a running smolanalytics server.
HOST="${HOST:-http://localhost:8080}"
KEY="${SMOLANALYTICS_WRITE_KEY:-}"
AUTH=""
[ -n "$KEY" ] && AUTH="-H \"Authorization: Bearer $KEY\""

# a single event
curl -s -X POST "$HOST/v1/events" $AUTH \
  -d '{"name":"signup","distinct_id":"u_1","properties":{"plan":"pro","source":"hacker news"}}' >/dev/null

# a batch
curl -s -X POST "$HOST/v1/events" $AUTH -d '[
  {"name":"signup","distinct_id":"u_2","properties":{"plan":"free"}},
  {"name":"activate","distinct_id":"u_2"},
  {"name":"checkout","distinct_id":"u_2","properties":{"amount":29}}
]' >/dev/null

echo "sent. open $HOST and ask: \"what's my signup -> checkout conversion?\""
