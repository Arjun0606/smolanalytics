#!/usr/bin/env bash
#
# smoke.sh — black-box health + covenant check for a LIVE smolanalytics instance.
#
# Run it after any deploy, against a self-hosted binary or a cloud tenant. It seeds
# nothing and only READS, so it is safe against production. It proves the promises that
# must hold no matter what the real numbers are:
#
#   • liveness            — the instance answers.
#   • the security model  — the PUBLIC write key can never read a report, the export,
#                           the verdict, or MCP (only the SECRET read key can).
#   • the covenant        — /v1, MCP, and the ask bar return the SAME number for the
#                           same question, and recomputing from the raw export agrees.
#
# Usage:
#   SA_URL=https://your-instance \
#   SA_READ_KEY=sk_...           \
#   [SA_WRITE_KEY=pk_...]        # optional: enables the security matrix (write-key-cant-read)
#   [SA_PASSWORD=...]            # optional: enables the ask-bar covenant check
#   [SA_EVENT=signup]            # optional: event to test (default: first one /v1/meta reports)
#   ./scripts/smoke.sh
#
# Exit code 0 = every check passed; 1 = at least one failed (paste the output into the issue).

set -u

URL="${SA_URL:?set SA_URL, e.g. https://your-instance.fly.dev}"
URL="${URL%/}"
RK="${SA_READ_KEY:?set SA_READ_KEY (the SECRET read key)}"
WK="${SA_WRITE_KEY:-}"
PASS="${SA_PASSWORD:-}"
EVENT="${SA_EVENT:-}"

pass=0; fail=0
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  green="\033[32m"; red="\033[31m"; dim="\033[2m"; off="\033[0m"
else
  green=""; red=""; dim=""; off=""  # not a terminal (CI/pipe) — no escape codes
fi
ok()  { pass=$((pass+1)); printf "  ${green}✓${off} %s\n" "$1"; }
bad() { fail=$((fail+1)); printf "  ${red}✗ %s${off}  ${dim}%s${off}\n" "$1" "${2:-}"; }

# status_of METHOD PATH [KEY] [BODY]  -> prints HTTP status code
status_of() {
  local m="$1" p="$2" key="${3:-}" body="${4:-}"
  local args=(-s -o /dev/null -w '%{http_code}' -X "$m" "$URL$p")
  [ -n "$key" ]  && args+=(-H "Authorization: Bearer $key")
  [ -n "$body" ] && args+=(-H "Content-Type: application/json" -d "$body")
  curl "${args[@]}"
}
# body_of PATH KEY  -> prints response body (GET)
body_of() { curl -s -H "Authorization: Bearer ${2:-}" "$URL$1"; }
# jnum KEY < json  -> extract a numeric field with python3 (portable, no jq dependency)
jnum() { python3 -c "import sys,json
try: d=json.load(sys.stdin); print(int(d.get('$1',-1)))
except Exception: print(-1)"; }

echo "== smolanalytics smoke :: $URL =="

# ---- 1. liveness -----------------------------------------------------------------
code=$(status_of GET /healthz)
[ "$code" = "200" ] && ok "instance is live (/healthz 200)" || bad "instance not live (/healthz)" "got $code"

# ---- pick an event to test on ----------------------------------------------------
if [ -z "$EVENT" ]; then
  EVENT=$(body_of "/v1/meta" "$RK" | python3 -c "import sys,json
try:
    e=json.load(sys.stdin).get('events',[])
    print(e[0] if e else '')
except Exception: print('')")
fi
if [ -z "$EVENT" ]; then
  bad "could not read the event catalog with the read key" "check SA_READ_KEY / SA_URL"
  echo; echo "RESULT: $pass passed, $fail failed"; exit 1
fi
echo "   testing on event: $EVENT"

# ---- 2. read key CAN read --------------------------------------------------------
code=$(status_of GET "/v1/trends?event=$EVENT" "$RK")
[ "$code" = "200" ] && ok "read key reads reports (200)" || bad "read key cannot read reports" "got $code"

# ---- 3. no credential is rejected (unless the instance is intentionally open) -----
code=$(status_of GET "/v1/trends?event=$EVENT")
if [ "$code" = "200" ]; then
  printf "  ${dim}• instance is in OPEN mode (no read key required) — skipping auth-negative checks${off}\n"
  OPEN=1
else
  OPEN=0
  [ "$code" = "401" ] || [ "$code" = "403" ] && ok "no credential is rejected ($code)" || bad "no-auth request was not rejected" "got $code"
fi

# ---- 4. SECURITY MATRIX: the PUBLIC write key must NEVER read (read-only probes) --
if [ -n "$WK" ] && [ "$OPEN" = "0" ]; then
  for p in "/v1/trends?event=$EVENT" "/v1/export" "/v1/notable"; do
    code=$(status_of GET "$p" "$WK")
    [ "$code" = "401" ] || [ "$code" = "403" ] \
      && ok "write key CANNOT GET $p ($code)" \
      || bad "write key was allowed to read $p" "got $code — SECRET data is exposed to the SDK key!"
  done
  code=$(status_of POST "/mcp" "$WK" '{"jsonrpc":"2.0","id":1,"method":"tools/list"}')
  [ "$code" = "401" ] || [ "$code" = "403" ] \
    && ok "write key CANNOT use MCP ($code)" \
    || bad "write key was allowed to use MCP" "got $code"
elif [ -z "$WK" ]; then
  printf "  ${dim}• SA_WRITE_KEY not set — skipping the write-key-cant-read security matrix${off}\n"
fi

# ---- 5. THE COVENANT: /v1 == MCP for the same question ---------------------------
v1=$(body_of "/v1/trends?event=$EVENT&days=7" "$RK" | jnum total)
mcp=$(curl -s -X POST "$URL/mcp" -H "Authorization: Bearer $RK" -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"trends\",\"arguments\":{\"event\":\"$EVENT\",\"days\":7}}}" \
  | python3 -c "import sys,json
try: print(int(json.loads(json.load(sys.stdin)['result']['content'][0]['text']).get('total',-1)))
except Exception: print(-1)")
printf "   ${dim}last 7d %s: /v1=%s  MCP=%s${off}\n" "$EVENT" "$v1" "$mcp"
[ "$v1" -ge 0 ] && [ "$v1" = "$mcp" ] \
  && ok "/v1 == MCP (last 7 days $EVENT)" \
  || bad "/v1 != MCP" "$v1 vs $mcp — the covenant is broken"

# ---- 6. ask bar == /v1 (needs the dashboard password) ----------------------------
if [ -n "$PASS" ]; then
  jar=$(mktemp)
  curl -s -c "$jar" -X POST "$URL/login" -H "Content-Type: application/x-www-form-urlencoded" \
    --data-urlencode "password=$PASS" -o /dev/null
  ask=$(curl -s -b "$jar" -X POST "$URL/v1/ask" -H "Content-Type: application/json" \
    -d "{\"question\":\"how many $EVENT in the last 7 days\"}" \
    | python3 -c "import sys,json,re
try:
    a=json.load(sys.stdin).get('answer','')
    m=re.search(r'(\d[\d,]*)',a); print(int(m.group(1).replace(',','')) if m else -1)
except Exception: print(-1)")
  rm -f "$jar"
  printf "   ${dim}ask bar: %s${off}\n" "$ask"
  [ "$ask" -ge 0 ] && [ "$ask" = "$v1" ] \
    && ok "ask bar == /v1 (last 7 days $EVENT)" \
    || bad "ask bar != /v1" "$ask vs $v1 — did you set the right SA_PASSWORD?"
else
  printf "  ${dim}• SA_PASSWORD not set — skipping the ask-bar covenant check${off}\n"
fi

# ---- 7. CORRECTNESS: recompute from the raw export == the report -----------------
raw=$(body_of "/v1/export?format=jsonl" "$RK" | python3 -c "import sys,json
n=0
for line in sys.stdin:
    line=line.strip()
    if not line: continue
    try:
        if json.loads(line).get('name')=='$EVENT': n+=1
    except Exception: pass
print(n)")
alltime=$(body_of "/v1/trends?event=$EVENT" "$RK" | jnum total)
printf "   ${dim}all-time %s: export=%s  report=%s${off}\n" "$EVENT" "$raw" "$alltime"
[ "$raw" -ge 0 ] && [ "$raw" = "$alltime" ] \
  && ok "export recompute == report (all-time $EVENT)" \
  || bad "export != report" "$raw vs $alltime — the raw log and the report disagree"

# ---- verdict ---------------------------------------------------------------------
echo
if [ "$fail" -eq 0 ]; then
  printf "${green}RESULT: %d passed, 0 failed — instance is healthy.${off}\n" "$pass"
  exit 0
else
  printf "${red}RESULT: %d passed, %d FAILED.${off}\n" "$pass" "$fail"
  exit 1
fi
