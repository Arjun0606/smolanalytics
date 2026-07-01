#!/bin/sh
# Send events to a running smolanalytics server.
HOST="${HOST:-http://localhost:8080}"
KEY="${SMOLANALYTICS_WRITE_KEY:-}"

# send <json> — includes the auth header only when a write key is set. (Building the
# header inside a variable and expanding it unquoted doesn't work in sh, so branch.)
send() {
  if [ -n "$KEY" ]; then
    curl -s -X POST "$HOST/v1/events" -H "Authorization: Bearer $KEY" -d "$1" >/dev/null
  else
    curl -s -X POST "$HOST/v1/events" -d "$1" >/dev/null
  fi
}

# a single event
send '{"name":"signup","distinct_id":"u_1","properties":{"plan":"pro","source":"hacker news"}}'

# a batch
send '[
  {"name":"signup","distinct_id":"u_2","properties":{"plan":"free"}},
  {"name":"activate","distinct_id":"u_2"},
  {"name":"checkout","distinct_id":"u_2","properties":{"amount":29}}
]'

echo "sent. open $HOST and ask: \"what's my signup -> checkout conversion?\""
