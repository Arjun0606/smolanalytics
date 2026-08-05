# Confirmed bugs — deep hunt, 2026-08-04

---

## Fixed in this session

Verified with a before/after: each of these has a regression test that FAILS on the old code.

| # | Severity | What |
|---|---|---|
| 1 | critical | Password-only instance left `POST /mcp` + `/v1/usage` unauthenticated — full read, mutate, raw-log export, and `create_api_key` for any stranger |
| 2 | critical | `"<event> from <channel> over time"` filtered events not users — answered a confident **zero** where the same question without "over time" answered **20** |
| 4 | high | Control arm chosen alphabetically — inverted the sign of every lift |
| 6 | high | `ComputeBreakdown` discarded `order` / `exclusions` (HTTP **and** MCP) |
| 11 | high | Session detail read the raw log while the list was scoped — 404 on a row the list just emitted |
| 13 | high | Verdict computed before the filter chips, fix briefs after |
| — | high | A zero-exposure arm vanished from the A/B report entirely |
| — | high | `Measure` called `time.Now()` inside the pure path — the report was not reproducible |
| — | high | A goal named `$feature_flag_called` was unreachable (shared `switch`) |
| 44 | medium | `ComputeXAU` lookback kept `from`'s time-of-day — WAU returned `[7,7,6,6]` for one day |
| 64 | medium | Bounce/engaged sparklines drawn over a different period than the number above them |
| 67 | low | `ComputeRange` was closed `[from, to]` though documented half-open — boundary events double-counted |
| 71 | low | `Prune` never rebuilt the name index — pruned events stayed in every picker |
| — | — | Funnel results flipped on storage order under tied timestamps (`[1 0 0 0]` by input order) |
| — | — | Browser SDK sent no event id, so every retry double-counted |

## Status after the full fix pass

A second pass ran eleven agents, one per subsystem, each owning a disjoint set of files.
**61 further findings were fixed** on top of the fifteen in the table above, every one with a
regression test. The tree is green: `go build`, `go vet`, `gofmt` and the whole suite.

Be careful reading the numbering below as a to-do list. The fix agents reported per-AREA counts
rather than per-finding ids, and several findings were fixed by more than one route, so the exact
remaining set is not cleanly derivable from this file. What IS reliable:

- Everything in the table above is fixed and pinned by a named test.
- Every finding in the areas `flag-experiments`, `trends-query`, `mcp-tools`, `sdk-js`,
  `ask-nl`, `api-surfaces`, `dashboard-render`, `store-ingest`, `insight-verdict` and
  `web-engagement-session` was worked. The agents' own skip lists are in the workflow journal.
- The honest way to find what is left is to re-run the hunt, not to trust this numbering.

**Known still open**, reported by agents as outside their file ownership and not picked up since:

- #51 the dashboard's "last N days" window is calendar-aligned only when `gran==day`, so
  `?gran=week` moves the headline number.
- #52 microscope descriptors omit the page's filter/env/site scope and collapse hourly bars to a
  whole day.

Numbering below is by severity, not by fix order.



72 findings survived adversarial verification (7 refuted). Ten finders swept
one subsystem each; every finding was then handed to an independent skeptic instructed to refute it
by default and to construct the triggering input. Only those that survived are here.

Several of these were fixed in the same session — see PRESENTATION.md. The rest are open.

## 1. [critical] A password-only instance leaves POST /mcp (and /v1/usage) fully unauthenticated — full read + mutate + raw-export exfiltration

**Where:** `internal/api/api.go:472` · **Area:** api-surfaces

**How it fails:** Operator does the documented thing for a public deploy: `SMOLANALYTICS_PASSWORD=... ADDR=0.0.0.0:8080 smolanalytics serve`, and sets no SMOLANALYTICS_READ_KEY / WRITE_KEY (both optional; the startup guard in cmd/smolanalytics/main.go:440 only checks the password, so the process starts happily). isPublic() exempts /mcp from the session gate, and authorized() then returns true because no key is configured. Verified end-to-end: `GET /` → 302 to /login, `GET /v1/trends` → 401, but `POST /mcp {"name":"trends"}` with NO credential → 200 `{"event":"signup",...,"total":1}`, and `POST /mcp {"name":"create_export_link"}` → 200 with a token, then `GET /export/<token>` (also isPublic) → 200 streaming the entire raw event log including distinct_ids (`secret-user@example.com`). Every mutating MCP tool (create_api_key, set_retention, delete_cohort, import_events…) is equally open — readOnly is set only in demo mode (main.go:397).

**Fix:** Make the "nothing configured" open mode depend on the dashboard password too, exactly as readsGated() already does for notable/brief: `if s.readKey == "" && s.writeKey == "" && !hasManaged && !s.authEnabled() { return true }`. Alternatively have handleMCP and usage() reject when `s.readsGated() && !s.keyAuthed(r) && !s.validSession(r)`. Add an e2e case with a password and NO keys — every existing auth test sets both keys, which is why this is uncaught.

**Verifier:** CONFIRMED by execution, not by reading. I reproduced the exact scenario in a scratchpad copy of the repo (`/private/tmp/.../scratchpad/repo`, working-tree code, only `internal/funnel/funnel.go` mid-edit noise worked around):

Config: `SMOLANALYTICS_PASSWORD=operator-pass-123`, no `SMOLANALYTICS_READ_KEY`, no `SMOLANALYTICS_WRITE_KEY`, no managed keys — i.e. exactly what `cmd/smolanalytics/main.go:440-452` tells an operator to do for a public bind ("set SMOLANALYTICS_PASSWORD=... (recommended for anything internet-facing)"); `authOn` is true so the fatal guard never fires.

Measured results with NO credential at all:
- `GET /` → 302 /login (gated, as expected)
- `GET /v1/trends?event=signup` → 401 (gated)
- `POST /mcp {"method":"tools/call","params":{"name":"trends"}}` → **200**, `{"event":"signup",...,"total":1}`
- `POST /mcp {"name":"recent_events"}` → **200**, returns `"distinct_id":"secret-user@example.com"` (raw PII)
- `GET /v1/usage` → **200** with total_events/users

With a persistent settings store wired (the real `serve` path, `cmd/smolanalytics/main.go:314-324`), it is strictly worse:
- `POST /mcp {"name":"create_api_key","arguments":{"name":"pwned"}}` → **200**, returns `

## 2. [critical] "<event> from <channel> over time" filters events, not users — states a confident zero where the same question without "over time" says 20

**Where:** `internal/api/ask_scope.go:1787` · **Area:** ask-nl

**How it fails:** 20 users arrive with referrer=reddit.com ($pageview) and each fires a `signup` (which, like all real signup events, carries no referrer). "how many signups from reddit" → "20 \"signup\" events from reddit." but "signups from reddit over time" → "No \"signup\" events from reddit the last 30 days (UTC), so the trend is flat at zero." (both verified). answerSegment uses segFilterUsers for user attributes; answerTrendText always uses event-level segFilter, so every signup is erased and the ask bar asserts zero.

**Fix:** In answerTrendText mirror answerSegment: `if m.kind == "event" && userAttr(segs[0].prop) { evs = segFilterUsers(evs, segs[0]) } else { evs = segFilter(evs, segs[0]) }`.

**Verifier:** Reproduced deterministically against the repo's own battery fixture. ask.go:592-594 routes any query containing "trend"/"over time"/"trajectory" to answerTrendText BEFORE the len(segs)==1 branch that would call answerSegment, so the user-scoping at ask_scope.go:723 (segFilterUsers for userAttr props) is never reached. answerTrendText (ask_scope.go:1787) applies event-level segFilter unconditionally; since signup events carry no referrer (the fixture at ask_battery_test.go:70 sets this up explicitly, and segFilterUsers' own doc comment at line 1756 states it), the pool collapses to pageviews and metricCount for the signup metric returns 0.

Exact triggering input, run in-package with answer(q, batteryFixture(), batNow):
- "how many signups from reddit" -> `2 "signup" events from reddit.`
- "signups from reddit over time" -> `No "signup" events from reddit the last 30 days (UTC), so the trend is flat at zero.`
- "signup trend from reddit" -> same false zero.

No guard, type constraint, or earlier validation prevents it: segs[0].found is true (the referrer exists on the $pageview), so the honest-zero early return is bypassed and the answer is a confident, fabricated zero.

No existing

## 3. [high] Conversion is scored against the user's FIRST goal event ever, not their first goal after exposure — repeat converters are silently counted as non-converters

**Where:** `internal/flag/measure.go:91` · **Area:** flag-experiments

**How it fails:** Goal event "purchase". User u1 purchases at T0, is exposed to variant a at T1, purchases again at T2 (T0<T1<T2). firstGoal[u1]=T0, which is Before(ex.at=T1), so u1 counts in the denominator (exposed++) but never in the numerator. Verified by running Measure on exactly those three events: {Exposed:1 Converted:0 RatePct:0}. Any repeatable goal (purchase, $pageview, session_start, message_sent) means every returning user who ever did the goal before the experiment started is permanently locked out of the numerator, so both arms' conversion rates are understated — and unequally so whenever exposure timing differs between arms.

**Fix:** Do not keep the global minimum goal timestamp. Either collect the goal timestamps per user and count a conversion if ANY is >= ex.at, or do a second pass after firstExp is built and mark converted[id]=true on the first goal event at or after firstExp[id].at. The existing test TestMeasureIgnoresPreExposureConversions only covers the case where the user never converts again, so it passes over the bug.

**Verifier:** CONFIRMED. I could not find any guard, caller-side pre-filter, or existing test that prevents it, and I reproduced the exact failure.

Code path (/Users/arjun/smolanalytics/internal/flag/measure.go):
- Line 91 keeps only the MINIMUM goal timestamp per user: `if cur, ok := firstGoal[e.DistinctID]; !ok || e.Timestamp.Before(cur) { firstGoal[...] = e.Timestamp }`.
- Line 106 then tests that single minimum against exposure: `if g, ok := firstGoal[id]; ok && !g.Before(ex.at) { t.converted++ }`.
- Every later goal event for that user is discarded before the comparison, so one pre-exposure goal permanently disqualifies the user no matter how many times they convert afterwards. There is no `lastGoal`, no per-user slice, and no "any goal >= exposure" check anywhere in the function.

Reproduction (temp test written in internal/flag, run, then deleted — working tree is clean):
  goal("u1", base)                       // T0 purchase
  exp("u1", "a", base.Add(1*time.Hour))  // T1 exposure, variant a
  goal("u1", base.Add(2*time.Hour))      // T2 purchase, AFTER exposure
  Measure(evs, "banner", "purchase", 0)
Result: variant a -> exposed=1 converted=0 rate=0. u1 lands in the denominator but not

## 4. [high] Control arm is chosen as the alphabetically-first variant, so a flag with a variant literally named "control" can have the treatment arm labelled control and every lift sign inverted

**Where:** `internal/flag/measure.go:115` · **Area:** flag-experiments

**How it fails:** A flag with arms "control" and "b_new_design": sort.Strings puts "b_new_design" first, so Report.Control="b_new_design" (verified). Every delta_pct, p-value, significance flag and DeltaCI is then computed with the treatment as the baseline — a real +20% win prints as -16.7% on the arm named "control", and the dashboard renders "significant at 95% against b_new_design". Also hits "baseline"/"control", "a_variant"/"control", or any naming where the intended control does not sort first. Measure never sees the Flag, so it cannot use the declared variant order that would give the right answer.

**Fix:** Prefer an arm literally named control/baseline/off/a when present, otherwise the flag's first declared positive-weight Variant (pass the Flag, or its variant order, into Measure); fall back to alphabetical only when neither is available. Sorting alphabetically is fine for output order but must not decide which arm is the baseline.

**Verifier:** CONFIRMED by construction, not just by reading. Ran Measure with arms "control" (100 exposed/20 converted) and "b_new_design" (100/40) — a real +100% win. Output: Report.Control="b_new_design"; the arm literally named "control" gets delta_pct=-50, significant=true; b_new_design gets delta=0 and is badged as the baseline.

No guard prevents it. measure.go:111-125 builds the variant list from the event property $feature_flag_response, sorts with sort.Strings, and takes variants[0] as control. Measure's signature (evs, flagKey, goal, days) never receives the Flag, so the declared Variants order — which CheckSRM in srm.go does receive and use — is unavailable to it. Store.Save (store.go:63-85) validates only non-empty variant keys and a positive weight sum; there is no naming or ordering constraint. Both create_flag (MCP) and POST /v1/flags accept arbitrary variant keys, and the MCP schema's own example is {"key":"a","weight":50}.

No test covers this deliberately. TestMeasureABWin (measure_test.go:39) asserts rep.Control=="a" with arms a/b, where alphabetical order happens to coincide with intent; it pins the sorting rule but never exercises a naming where the rule and the author's in

## 5. [high] Feature flags are never fetched in cookieless (anonymous:true) mode — flag() silently returns the default forever

**Where:** `internal/api/sdk.js:510` · **Area:** flag-experiments

**How it fails:** init(key,{anonymous:true}) sets anon=true; distinctId() returns the "$anon" sentinel on its first line and never assigns the module-level `did`, which stays null. fetchFlags() then returns on its first guard, flagsLoaded stays false, flagCache stays {}, so smolanalytics.flag("checkout_v2", false) returns the default for every visitor and no $feature_flag_called exposure is ever logged. No console warning, no error — the experiment simply never runs, and the A/B read reports "no exposures yet" as if the SDK were fine.

**Fix:** Use the resolved id rather than the raw variable: `var id = distinctId(); if (!host || !key || !id) return;` and send distinct_id=id&bucket_id=bucketId(). In anon mode either bucket on the server-derived visitor id or warn once that flags require a stable bucket id.

**Verifier:** Confirmed by executing the real sdk.js in a shimmed Node environment. Trigger: smolanalytics.init("wk", { host, anonymous: true }). Result: zero fetch calls to /v1/flags/evaluate and flag("checkout_v2", false) === false, while the identical run with anonymous:false fetches flags and returns "b" from the stub server.

Code path: init sets anon=true (sdk.js:636); distinctId() returns the "$anon" sentinel on its first line (sdk.js:96) and never assigns module-level `did`; fetchFlags()'s guard `if (!host || !key || !did) return;` (sdk.js:510) therefore short-circuits forever. Nothing else assigns `did` — bucketId()'s catch branch only writes `bid`, enqueue() just uses distinctId()'s return value, and only identify(id) sets did (and clears anon). So flagCache stays {}, flagsLoaded stays false, onFlags callbacks never fire, and no $feature_flag_called exposure is ever logged, with no console warning.

No existing guard, validation, or test prevents it. The only SDK test (internal/api/sdktest/env.test.mjs) covers detectEnv only; internal/api/flags_test.go and bucket_stability_test.go exercise the Go handler with real ids, never the SDK. Nothing documents flags as unsupported in cookieless

## 6. [high] ComputeBreakdown silently discards order, exclusions and step filters — a segmented funnel always runs `ordered` with no exclusions

**Where:** `internal/funnel/funnel.go:109` · **Area:** funnel-retention

**How it fails:** GET /v1/funnel?steps=signup|activate|checkout&order=strict&exclude=refund&sf1=plan:pro&breakdown=source — the handler parses and validates all four options into `opts`, then takes the breakdown branch and calls ComputeBreakdown, which has no Options parameter and delegates to Compute → ComputeOpts(..., Options{}). Every segment is computed with Order=Ordered, zero exclusions and zero step filters. A user who did signup → refund → activate → checkout is counted as a converter in the per-source breakdown but excluded from the unsegmented funnel, so the segment columns do not sum to the total funnel and the numbers contradict the same question asked without `breakdown=`. Identical on MCP (mcp.go:363) and on the dashboard, where the 'conversion by X' pane (dashboard.go:2072 → funnelBySegment → ComputeBreakdown) is hardcoded ordered while the funnel pane directly above it renders `forder` from the ?forder= dropdown (dashboard.go:1613) — selecting 'strictly consecutive' or 'any order' changes the bars but not the segment card on the same screen. On MCP it is worse: the breakdown branch returns before `funnel.ParseOrder(a.Order)` runs, so order:"Strict" or order:"any-order" is not even a 400, it is silently ordered. The MCP tool schema (tools.go:66-79) advertises order/exclude/step_filters and breakdown as independent parameters with no note that they are mutually exclusive.

**Fix:** Add an Options parameter: `func ComputeBreakdownOpts(events []event.Event, steps []Step, window time.Duration, property string, opts Options) []SegmentResult` that calls ComputeOpts(evs, steps, window, opts) at line 109, keep ComputeBreakdown as a thin wrapper passing Options{}, and pass the request's `opts` from api.go:904, mcp.go:363 and dashboard.go:169 (threading `forder` into funnelBySegment). Move ParseOrder above the MCP breakdown branch so an invalid order still 400s. Add a test asserting ComputeBreakdown(order=strict) differs from order=ordered on a fixture where an interleaved event breaks the strict sequence.

**Verifier:** CONFIRMED at the committed baseline. At git HEAD, internal/funnel/funnel.go:72 ComputeBreakdown takes no Options and calls Compute -> ComputeOpts(..., Options{}) per segment (line 109). internal/api/api.go:859-881 parses and validates order/exclude/sf<N> into opts, then the breakdown branch at line 904 calls ComputeBreakdown without opts and returns; opts is used only by the unsegmented ComputeOpts at line 913. internal/mcp/mcp.go:363 is identical and additionally returns BEFORE funnel.ParseOrder at line 376, so an invalid order (e.g. "Strict") on a breakdown call is silently ordered rather than a 400. internal/api/dashboard.go:169 (funnelBySegment, called at 2122) is hardcoded optionless while the funnel pane at 1655 honours ?forder= parsed at 1611.

Concrete triggering input, executed: users A (signup->activate->checkout) and B (signup->refund->activate->checkout), both source=hn, with order=strict & exclude=refund. Ran at package level: unsegmented ComputeOpts gives converted=1, order=strict, excl=[refund]; ComputeBreakdown gives segment "hn" converted=2, order=ordered, excl=[]. HTTP equivalent: GET /v1/funnel?steps=signup,activate,checkout&order=strict&exclude=refund&breakdown=

## 7. [high] Dashboard retention grid measures week/month period offsets in DAYS — every weekly and monthly grid renders periods that have not started as a finished 0%

**Where:** `internal/api/dashboard.go:1884` · **Area:** funnel-retention

**How it fails:** ?rbucket=week (dashboard.go:1577-1582, fed to retention.ComputeBucketed at :1614). The grid's observability test converts the cohort date to a DAY index (`/ 86400`) and then adds `d`, which for a weekly grid is a WEEK index, comparing the sum against `today`, also a day index. For the current week's cohort, cohortDay ≈ today-3, so d=1 gives today-2 <= today, d=2 gives today-1, … d=6 gives today — all six cells are treated as observable and rendered as `c.Returned[d]/c.Size` = 0/N = "0%", when weeks 1..6 for that cohort begin 7..42 days in the FUTURE. The dashboard therefore shows a fresh weekly cohort as W0 100%, W1-W6 0% — 'retention cratered to 0%', fabricated. Older cohorts are hit too: a 4-week-old cohort has d=5 → cohortDay+5 = today-23 <= today, so week 5 (which starts a week from now) also renders 0%. Month buckets (30-day blocks) are off by 30x. Meanwhile the API for the exact same query returns null for every one of those cells (retention.SerializeCohorts nulls any n>=1 with cp+n >= cur, using the bucket's own bucketSeconds), and retention.PeriodN excludes those cohorts from the summary — so /v1/retention?bucket=week and the dashboard render contradictory answers to the identical question. The same wrong expression at :1874 also sets vm.RetentionReady=true when nothing is actually observable.

**Fix:** Compute the grid's observability with the same bucket unit the engine uses — export retention.BucketSeconds (or add retention.Observable(r Result, cohortIdx, n int, now time.Time) bool) and replace both :1872/:1884 `/ 86400` and the `today` at :1862 with `/ bucketSeconds(rr.Bucket)`. Better: have the dashboard consume retention.SerializeCohorts(rr, now) and render a nil Returned[n] as an empty cell, so there is exactly one observability rule for all surfaces. Add a dashboard test with ?rbucket=week and a same-week cohort asserting W1..W6 render blank, not 0%.

**Verifier:** Confirmed by direct reproduction, not by reading. Trigger: GET /?rbucket=week (or =month) with 20 users seen once two hours ago and no returns. Path: dashboard.go:1619-1623 parses rbucket -> :1656 retention.ComputeBucketed(evs, rdays, retEvent, rbucket, rroll) -> grid loop :1907-1930, where `today := time.Now().UTC().Unix()/86400` and `cohortDay := c.Date.UTC().Unix()/86400` are DAY indices while `d` is a PERIOD index. rr.Bucket is consulted nowhere in that block except to pick the D/W/M header letter (:1891-1897); no guard, validation, or type constraint intervenes.

Rendered output (today 2026-08-04, epoch-week offset 5): ?rbucket=week gives row `Jul 30 | 20 | 100% | 0% | 0% | 0% | 0% | 0% | empty | empty` -- W1..W5 rendered as finished 0% although those weeks begin 2 to 37 days in the future. ?rbucket=month gives `Jul 6 | 20 | 100% | 0% x7` -- every month cell fabricated, M1..M7 begin 1 to 7 months out.

Same data, same instant, the API: /v1/retention?bucket=week&days=7 returns returned:[20,null,null,null,null,null,null,null], and bucket=month the same. retention.SerializeCohorts (retention.go:167-189) uses bucketSeconds(r.Bucket) and nulls every one of those cells; PeriodN (:13

## 8. [high] ComputeInterval's hourly 744-bucket guard truncates the series *and* the Total, and keeps the OLDEST buckets

**Where:** `internal/trends/trends.go:626` · **Area:** trends-query

**How it fails:** `GET /v1/trends?days=60&interval=hour` (or MCP `trends(hours=1440, interval="hour")`; hours is allowed up to 24*366 and interval is parsed independently with no cross-check). With 1440 events, one per hour, over the 60-day window: day grain returns Total 1440, week grain returns Total 1440, hour grain returns Total 744 — because `r.Total += n` lives inside the loop that `break`s at 744. Verified by running the real code: `day total: 1440 / week total: 1440 / hour total: 744`, and `hour first bucket: 2025-12-31 last: 2026-01-30` — i.e. the chart shows the OLDEST 31 days of a 60-day window, the opposite of Compute's cap which keeps the most recent. This directly breaks the WINDOW-2 contract asserted at line 46 ("day/week/month buckets of the same window always sum to the same total").

**Fix:** Reject hour grain over a window longer than 31 days at the API/MCP boundary (a 400 like ParseInterval's unknown-grain error), or start loT at hiT minus 744 hours so the RECENT buckets survive — and in either case accumulate Total from `per` over the whole window rather than inside the truncated emit loop.

**Verifier:** CONFIRMED — I could not find any guard, validation, or cross-check that prevents it, and I reproduced it through the real HTTP handler.

Triggering input (verified, not theoretical): `GET /v1/trends?event=signup&days=60&interval=hour` against a store holding 1400 events, one per hour, over the last ~60 days. Real handler output (memory store + `s.Handler()`, my throwaway test, since deleted):
- `days=60` (day grain): total=1400, 60 points
- `days=60&interval=week`: total=1400, 10 points
- `days=60&interval=hour`: **total=734**, 744 points, first bucket `2026-06-06T00:00:00Z`, last `2026-07-06T23:00:00Z` — i.e. on 2026-08-04 the "last 60 days hourly" chart renders June 6 → July 6 and omits the most recent month entirely, with no error, no flag, and no truncation notice.
(At the package level with a clean synthetic window: day=1440, week=1440, hour=744, first 2025-12-31, last 2026-01-30.)

Path reachability, checked in full:
- `internal/api/query_api.go:332` parses `interval` with `trends.ParseInterval` and does nothing else with it; `parseTrendWindow` (line 371) validates `days<=365` / `hours<=24*366` but never cross-checks the grain. So any `days>31` or `hours>744` combined with `i

## 9. [high] An acquisition-property filter means first-touch on /v1 + MCP but any-touch on the dashboard and ask bar — same question, different number

**Where:** `internal/query/query.go:387` · **Area:** trends-query

**How it fails:** u1 lands on desktop (day 0), returns on mobile (day 2), signs up (day 2). Ask "signups where device=mobile". /v1 (`s.filtered` → StampForFilters → ApplyMode) stamps u1's whole stream with the FIRST-touch device (desktop) and then filters event-level, so u1 is excluded: 0 signups. The dashboard takes the userAttr branch (`query.ScopeUsers(evs, userF, anyMode)`, dashboard.go:1493) and the ask bar takes `segFilterUsers` (ask_scope.go:1760), both of which keep every event of any user with AT LEAST ONE matching event: 1 signup. Verified by running the real code: `signups where device=mobile: /v1+MCP = 0  dashboard = 1`. The same divergence applies to referrer / source / utm_* / country / os / browser for any user whose attribute changed between sessions.

**Fix:** Pick ONE semantic and route every surface through it. Either make the dashboard/ask bar go through StampForFilters+ApplyMode (first-touch), or make /v1 and MCP use ScopeUsers for acquisitionProps (any-touch). The claim in the StampForFilters doc comment that "all four surfaces agree" should then be pinned by a test that exercises a user whose attribute changes mid-history — the existing agreement tests only use single-valued users, which is why this survives.

**Verifier:** CONFIRMED by executing both code paths. Trigger input: one user u1 with $pageview{device:desktop} at d-4, $pageview{device:mobile} at d-2, and signup{plan:free} at d-2; question "signups where device=mobile". Measured: GET /v1/trends?event=signup&f=device:mobile = total 0, MCP trends with the same filter = total 0, query.ScopeUsers (the dashboard.go:1493 branch) = 1 signup, ask bar answer = "1 \"signup\" events on mobile."

Path: /v1 and MCP both call s.filtered -> query.ApplyMode(query.StampForFilters(all, fs), fs, anyMode) at internal/api/query_api.go:111. StampForFilters (internal/query/query.go:387) matches "device" in acquisitionProps and hands it to BuildFirstTouch/Stamp, whose `np[prop] = val` OVERWRITES the natively-carried value on later events with the user's earliest value, so u1's mobile pageview is rewritten to desktop, the event-level ApplyMode matches nothing, and the signup is dropped => first-touch semantics. The dashboard (internal/api/dashboard.go:1487-1499) sends userAttr properties to query.ScopeUsers and the ask bar (internal/api/ask_scope.go:723, 747, 1516) sends them to segFilterUsers, both "keep every event of any user with at least one matching event" => a

## 10. [high] Country share-of-total % divides by the sum of the top-10 rows, not by recorded visitors — 20% renders as 52%

**Where:** `internal/api/dashboard.go:2224` · **Area:** web-engagement-session

**How it fails:** 100 visitors with geo: 20 from US, plus 40 other countries at 2 visitors each. web.ComputeRange returns Countries = rank(countries, 10), i.e. only the top 10 rows, summing to 38 visitors. The dashboard then uses that truncated slice as its own denominator. Measured with the real code: `visitors=100, rank(countries,10) rows=10 summing to 38 (dashboard denominator); US row: count=20 -> dashboard Pct = 52% | true share of recorded = 20%`. The geo card renders `US  20 · 52%` — a 2.6x overstatement — and the ten visible rows' percentages sum to 100% while covering only 38% of visitors. The more long-tail geography a site has, the bigger the lie. Same defect on AIRefs (rank(aiRefs,6) with 10 possible AI hosts).

**Fix:** Carry the true recorded total out of internal/web rather than re-deriving it from a truncated slice: add a per-dimension total (e.g. `CountriesTotal int`) computed in ComputeRange before rank() truncates, and pass that as the denominator to toRows. Alternatively have rank() emit an "(other)" remainder row so sumRows is again complete.

**Verifier:** Verified by running the real handler, not by reading. Triggering input: 100 visitors, each one $pageview with a `country` property — 20 "US" plus 40 other codes at 2 visitors each — ingested into memory.Store, then s.dashboard(w, httptest req "/?range=1d"). Measured: visitors=100, wv.Countries has 10 rows summing to 38, and the rendered HTML contains `data-cc="US">US</span><span class="segnums">20 · 53%</span>` — a 20% true share printed as 53%, with the ten visible rows' percentages summing to 100% while covering only 38 of 100 visitors.

Code path: internal/web/web.go:234 `Countries: rank(countries, 10)`; rank() (web.go:311-334) truncates with `out = out[:limit]`; `country` is bumped once per visitor in the firstPV first-touch loop (web.go:197), so Row.Count == visitors. internal/api/dashboard.go:2274 then computes `toRows(wv.Countries, 10, sumRows(wv.Countries))`, using the already-truncated slice as its own denominator; dashboard.tmpl.html:1386 renders `{{.Count}} · {{.Pct}}%`.

No guard/validation/test prevents it: country_display_test.go only covers flag/label decoration, dashboard_rank_test.go contains no Pct assertion, and nothing recomputes the denominator. The comment at 

## 11. [high] Session detail (/v1/session, MCP session_timeline) reads the RAW event log while the session list is production-scoped and windowed — 404s on rows the list just returned

**Where:** `internal/api/sessions_api.go:39` · **Area:** web-engagement-session

**How it fails:** Two independently confirmed triggers. (a) Dev contamination: alice has $pageview at -3h with env=development, then $pageview /pricing at -3h+1m and $click at -3h+2m. `/v1/sessions` runs query.Apply, which drops the env=development event, so the row is `start_unix=<-3h+1m> events=2 entry=/pricing`. Clicking it calls session.One over the unfiltered log, where splitSessions groups all three events and grp[0] is the dropped dev event, so `grp[0].Timestamp.Unix() != startUnix` and the API returns 404 "session not found". (b) Window straddle, no dev events needed: bob's session spans the days= cutoff (events at -7d-10m and -7d+10m). Sessions(evs, 7, ...) drops the pre-cutoff event before splitting, so the row is `start_unix=<-7d+10m> events=1 entry=/pricing`; One() splits full history, grp[0] is the -7d-10m event, mismatch, 404. Both reproduced: "BUG CONFIRMED: session detail 404s on the start_unix the list just handed out" / "BUG CONFIRMED: detail 404s for a session straddling the days= cutoff". When it does not 404 (a dev event landing mid-session), the detail silently shows a different Events/Pages/RageClicks count than the row it was opened from, and dev traffic leaks into a production-scoped surface.

**Fix:** Give One() the same inputs as Sessions(): in apiSession use s.filtered(r) and pass the days window through (add a days param to session.One so it applies the identical cutoff before splitSessions); in mcp.go wrap the session_timeline evs in query.Apply(query.StampForFilters(...)) with the same days default (7) list_sessions uses. Then extend TestSessionAgreement with a mixed dev/prod journey so the fixture no longer has to avoid the bug.

**Verifier:** CONFIRMED — reproduced four separate triggers against the real HTTP handlers (temporary test file written, run, then deleted; no repo changes left behind).

The asymmetry is exactly as reported and there is no guard anywhere on the detail path:
- /Users/arjun/smolanalytics/internal/api/sessions_api.go:24 — list: `evs, err := s.filtered(r)` → /Users/arjun/smolanalytics/internal/api/query_api.go:111 `query.ApplyMode(query.StampForFilters(all, fs), fs, anyMode)`, and query.Keeper (/Users/arjun/smolanalytics/internal/query/query.go:237-261) drops any event whose `env` is in NonProduction unless a filter names `env`.
- /Users/arjun/smolanalytics/internal/api/sessions_api.go:39 — detail: `evs, err := s.store.Range(time.Time{}, time.Time{})` — raw log, no query.Apply, no days window, no request filters (the handler never even parses filters).
- /Users/arjun/smolanalytics/internal/session/session.go:60 `if days > 0 && e.Timestamp.Before(cutoff) { continue }` inside Sessions, vs One (line 88-97) which only filters by DistinctID and then requires `grp[0].Timestamp.Unix() == startUnix`. So the handle the list mints (`StartUnix = grp[0].Timestamp.Unix()` after filtering) is computed over a dif

## 12. [high] segmentBlame's "for everyone" base is a 2-step funnel, not the multi-step funnel the dashboard shows — it fabricates "N× worse than average" and contradicts the drop-off finding sitting next to it

**Where:** `internal/insight/journey.go:279` · **Area:** insight-verdict

**How it fails:** Funnel signup → activate → checkout. 100 users signup→activate, 30 of them checkout (the dashboard funnel pane and the drop-off finding both say 30%). Another 100 users do activate without signup, 90 of them checkout. segmentBlame computes `overall` as a plain 2-step activate→checkout funnel over ALL events, so entered=200, converted=120 → 60%. The verdict card then prints, one line above the drop-off card that says 30%:
  warn/segment: "Mobile visitors convert worst from activate to checkout, 2.0× worse than average — only 30% of mobile visitors continue, against 60% for everyone (30 of 100). Fixing this group is the biggest single lever on the funnel."
  warn/funnel_dropoff: "Biggest drop-off: after they activate — only 30% go on to checkout"
Mobile is exactly the funnel average, not 2× worse, and "60% for everyone" is a number that appears nowhere else on the page. Verified by running GenerateForFunnel on that fixture. The same brief from fixbrief.ComputeAll carries both numbers at once: Detail says "against 60% for everyone" while its own evidence row says "device = mobile through activate → checkout = 30%, against 30% for everyone" (fixbrief.go:288 reads ConversionFromPrev off the real multi-step funnel). The 0.7 gate at journey.go:190 is applied against the inflated base, so this also FIRES the finding in cases where no segment underperforms. Existing tests never catch it because TestSegmentBlame/TestSegmentBlameFirstTouch only ever blame the FIRST transition (signup→activate), where the two bases coincide.

**Fix:** Compute the blame rates on the same funnel the drop-off came from: pass `steps` into segmentBlame and use funnel.ComputeBreakdown(evs, steps, window, prop) (which already implements first-step segment attribution), taking ConversionFromPrev at the from→to transition for both the segment and the overall base. Failing that, restrict the population fed to stepRate to users who reached `from` through the preceding steps, and assert in a test that the segment finding's "for everyone" number equals the drop-off finding's Rate.

**Verifier:** CONFIRMED by execution, not inspection.

Triggering input (funnel signup → activate → checkout, 7d window):
- 100 users do signup then activate; 30 of them checkout; all device=mobile
- 100 users do activate with NO signup; 90 of them checkout; all device=desktop

Code path: insight.go:202 computes the real 3-step funnel → worst drop is activate→checkout at ConversionFromPrev=0.300 over base 100. insight.go:238 then calls segmentBlame(evs, "activate", "checkout"). segmentBlame narrows `stamped` only by event NAME (journey.go:123-127), never by funnel entry, then journey.go:159 calls stepRate → journey.go:279 runs a standalone 2-step funnel.Compute over ALL events, so entered=200, converted=120, rate=0.600.

Measured output of GenerateForFunnel on that fixture (I wrote and ran the fixture, then deleted it):
  [warn/segment] "Mobile visitors convert worst from activate to checkout, 2.0× worse than average | only 30% of mobile visitors continue, against 60% for everyone (30 of 100). Fixing this group is the biggest single lever on the funnel."
  [warn/funnel_dropoff] "Biggest drop-off: after they activate | only 30% go on to checkout, so 70 people stop here. End to end, 30% get from s

## 13. [high] The dashboard computes the verdict BEFORE the filter chips are applied but the fix briefs AFTER — with any ?f= chip the verdict card contradicts the funnel pane and the briefs behind it

**Where:** `internal/api/dashboard.go:1384` · **Area:** insight-verdict

**How it fails:** Open the dashboard with a filter chip, e.g. `/?f=plan:pro`. `verdict` is built at line 1384 from the unfiltered `evs`; `evs` is then reassigned by the chip filters at lines 1493-1498; the funnel pane (line 1613) and every fix brief (line 1591) are computed from the FILTERED slice. The page then renders a verdict card reading "Biggest drop-off: after they activate — only 30% go on to checkout" (all traffic) directly above a funnel pane drawing 62% for the same transition (pro plan only), and the brief the reader opens from that card describes the pro-plan population under the all-traffic title. Nothing errors and no number is labelled with its population. The comment on line 221 explicitly promises the opposite: "Computed by the SAME path GET /v1/fix-brief and the fix_brief MCP tool return (fixbrief.ComputeAll), over the funnel THIS PAGE is showing — so the button, the endpoint and the tool can never hand out three different stories about one finding."

**Fix:** Move the `verdict := insight.GenerateForFunnel(...)` call (and `verdictSteps := detectFunnel(...)`) to after the chip filters are applied, so the verdict, the funnel pane and fixbrief.ComputeAll all read one slice — or, if the verdict is deliberately unfiltered, compute the briefs from the same unfiltered slice and label the card as "all traffic".

**Verifier:** CONFIRMED by execution, not just by reading. I wrote a throwaway test in `internal/api` (since deleted) that ingested 60 free-plan users (10% signup→checkout) and 40 pro-plan users (90% signup→checkout), then rendered `/` and `/?f=plan:pro` through the real handler.

Ordering in /Users/arjun/smolanalytics/internal/api/dashboard.go (line numbers are 1425/1426, 1535-1541, 1633, 1655 in the current working tree — the report's 1384/1493/1591/1613 are off by ~42 but point at the same statements):
- 1425-1426: `verdictSteps, _ := detectFunnel(evs, ...)` and `verdict := insight.GenerateForFunnel(evs, verdictSteps)` — `evs` here is only site/env-scoped.
- 1494-1543: the `?f=` chip block reassigns `evs` via `query.ScopeUsers` / `query.ApplyMode`.
- 1633: `fixbrief.ComputeAll(evs, verdictSteps, nowT)` — filtered `evs`, unfiltered `verdictSteps`.
- 1655: `funnel.ComputeOpts(evs, fsteps, ...)` — filtered.
- 1743-1744: the stale `verdict` is handed to the template, which renders the callout unconditionally (dashboard.tmpl.html:1092), with no gate on `.Chips`.

Measured output with `?f=plan:pro` (zero free-plan events in scope):
- verdict card: "Free-plan users convert worst from signup to check

## 14. [high] anomalies() divides the baseline total by a hard-coded 7.0 regardless of how many days of data exist — a brand-new instance with perfectly flat traffic is told it "jumped 250%"

**Where:** `internal/insight/insight.go:357` · **Area:** insight-verdict

**How it fails:** An instance that has been live for 3 days at a perfectly flat 30 signups/day. baseTotal covers only the 2 days that exist inside [now-8d, now-24h) = 60 events; baseDaily = 60/7 = 8.6; last24 = 30; dev = +250%. Verified output: `[info] signup jumped 250% in the last 24h | 30 in the last 24h vs ~9/day normally. (n=60, small sample) | Rate=250 N=60`. The event was never at 9/day — that number is fabricated — and the whole finding is a false alarm on flat traffic. `s.baseTotal < minSample` does not guard it (60 >= 20) and neither does `baseDaily < 3` (8.6 >= 3). The same hazard runs the other way once the instance is older than the data retention window. This is precisely the failure the function's own doc disclaims: "Noise-guarded — only events with a real baseline, only big swings — so a low-volume product never gets false alarms", and it lands on exactly the audience the file says the digest serves ("the low-traffic products this digest serves", line 67-68).

**Fix:** Divide by the number of baseline days actually covered by data, not by 7: track the distinct UTC days in [baseStart, recentStart) that carry at least one event for that name (or clamp to the days since the instance's first event) and use that as the divisor — and suppress the finding entirely when fewer than, say, 5 baseline days are observed, the same way geoMinDays gates the AI-visibility shift.

**Verifier:** Reproduced directly. Wrote a throwaway test in internal/insight/ constructing an instance with 3 days of history at a flat 30 signups/day (distinct users) and ran Generate(). Output matched the report verbatim: `[info] signup jumped 250% in the last 24h | 30 in the last 24h vs ~9/day normally. (n=60, small sample)`. The base window [now-8d, now-24h) captures only the 2 days that exist, baseTotal=60 (>= minSample 20), baseDaily=60/7=8.57 (>= the 3 floor), dev=2.5 (>= the 0.4 swing floor) — every existing guard is cleared, so the hard-coded /7.0 at insight.go:357 fabricates a baseline the event never had.

No upstream guard exists. GenerateForFunnel never bounds evs by instance age, and every caller passes the full unbounded slice: internal/api/api.go:171 uses s.store.Range(time.Time{}, time.Time{}), plus internal/api/dashboard.go:1426, internal/brief/brief.go:104, internal/mcp/mcp.go:776, internal/api/explore.go:51.

Worse than reported: repeating the same flat-traffic input with a failureEvent name ($deadclick) flips rose==bad and produces severity warn — `[warn] dead clicks jumped 250% in the last 24h ... that many more people hitting something that did not work.` evaluateAnomalie

## 15. [high] segment.Scan snapshots the manifest and reads the hot WAL non-atomically — a seal that lands in between silently drops the entire hot block from the answer

**Where:** `internal/store/segment/segment.go:224` · **Area:** store-ingest

**How it fails:** Store has 1 sealed segment + 5 events in the hot WAL (6 total, Count() agrees). A query calls Scan. Scan copies the manifest under RLock and releases it. While it is decoding segment 0 from the blob backend, a concurrent Ingest crosses sealAt and runs sealLocked: it writes the hot block as segment 1, persists the manifest, then hot.Clear(). Scan then calls s.hot.Range(from,to) — now empty — and segment 1 is not in Scan's stale snapshot. Scan returns 1 event out of 6, with a nil error. Verified by probe: `Count() says: 6` / `Scan saw: 1 err: <nil>` / re-running Scan immediately after: `6`. With the default sealAt=50_000 this fires on every 50k-event boundary, i.e. precisely under the load where dashboards, HTTP API and MCP are all polling. Every surface silently under-reports by up to sealAt events, and two identical questions asked a second apart give two different numbers — the exact failure the product's "provably correct, byte-identical across surfaces" positioning forbids. TestConcurrentIngestNoLossOrDup does not catch it: it only Scans after wg.Wait(), never during ingest.

**Fix:** Make the manifest snapshot and the hot read one atomic observation. Cheapest correct fix: read the hot block into a slice inside the same RLock that copies the manifest (`segs := copy(...); hot, err := s.hot.Range(from,to)` both before RUnlock), then stream segments and finally the already-captured hot events. Alternatively give the store a monotonic epoch/generation and re-snapshot + restart the scan if the epoch changed, or hold RLock for the whole scan (bounded-memory is preserved either way since only the hot block, capped at sealAt, is materialized).

**Verifier:** CONFIRMED, not refuted. I reproduced it deterministically.

Code path (/Users/arjun/smolanalytics/internal/store/segment/segment.go):
- `Scan` (line 224) takes `s.mu.RLock()`, copies `s.manifest` into `segs`, then **releases the lock at line 228** before the segment loop and before `s.hot.Range(from, to)` at line 250. There is no guard anywhere between: `blob.Get`/`decodeSegment` run unlocked, and the hot read is a separate, independently-locked call into `file.Store.Range` (which only takes file.Store's own RLock).
- `Ingest` (line 130) holds `s.mu.Lock()` for its whole body and calls `sealLocked` (line 154) when `s.hot.Count() >= s.sealAt`. `sealLocked` appends to `s.manifest`, persists it, bumps `s.seq`, then `s.hot.Clear()` — all inside the same write-lock critical section.
- Because seal is atomic w.r.t. the manifest snapshot, the duplicate direction is impossible, but the *drop* direction is wide open: a Scan that snapshots the manifest pre-seal, then reads the hot WAL post-seal, sees neither the new segment (absent from its stale snapshot) nor the hot events (cleared). `Count()` (line 282) reads hot + manifest under one RLock and is therefore consistent, which is why Count a

## 16. [high] file.Store.Ingest does not deduplicate duplicate IDs *within* a single batch — the file backend double-counts where the memory backend does not

**Where:** `internal/store/file/file.go:95` · **Area:** store-ingest

**How it fails:** POST /v1/events with a body containing the same event twice (`[{"id":"same",...},{"id":"same",...}]`), or an `import_events` batch whose source export repeats an $insert_id, or an SDK that appends to its queue and retries the flush before clearing it. The dedup check reads `s.seen[e.ID]`, but `s.seen[e.ID] = true` is only set inside `index()`, which runs *after* the loop finishes. So both copies are marshalled, appended to the log, and indexed. Verified by probe with one identical event passed twice: `memory backend: 1 events` / `file backend: 2 events` / `file after reopen: 2 events`. The duplicate is durable — it survives restart and inflates every count, funnel step, trend bucket and revenue sum forever. This is a straight cross-backend divergence: identical input, two different answers depending on which store.Store is behind the same HTTP handler, against a Store contract that states "Ingestion is idempotent on Event.ID so a retried request never double-counts". internal/importer/mappers.go:52/92/154/222 explicitly build on this guarantee ("Ids are kept, so re-importing is idempotent"). TestIdempotentAcrossReopen only exercises two *separate* Ingest calls, so the batch case is untested.

**Fix:** Track within-batch IDs in the same pass, e.g. add `batchSeen := make(map[string]struct{}, len(events))` and change the guard to `if e.ID != "" { if s.seen[e.ID] { continue }; if _, dup := batchSeen[e.ID]; dup { continue }; batchSeen[e.ID] = struct{}{} }`. Add a regression test asserting `Ingest(e, e)` yields Count()==1 on every backend.

**Verifier:** CONFIRMED. In internal/store/file/file.go:83-119, Ingest checks `s.seen[e.ID]` in the marshal loop but `s.seen[e.ID] = true` is only written by index() (file.go:75-81), which runs in a separate loop after the marshal loop finishes. So duplicate IDs inside a single Ingest call are all marshalled, appended to the JSONL log, and indexed. memory/memory.go:29-34 marks seen inline and does dedup intra-batch, so the two backends diverge on identical input against store.go:14-17 ("Ingestion is idempotent on Event.ID").

Triggering input, with no upstream guard: POST /v1/events with body `[{"id":"same","name":"signup","distinct_id":"u1"},{"id":"same","name":"signup","distinct_id":"u1"}]`. api.go:694-717 decodes the array verbatim; the normalization loop at api.go:769-823 only fills IDs when empty (`if batch[i].ID == "" { batch[i].ID = newID() }`), so client-supplied duplicate IDs pass straight to s.store.Ingest(batch...) at api.go:825. The bot filter, name/distinct_id validation and timestamp clamping do nothing about IDs. The MCP import path (import_tool.go:70 via importer.IngestSender, importer.go:175-196 which buffers BatchSize events per Ingest call) hits the same path when an export re

## 17. [high] file.Store.Open never truncates a torn tail back to the last newline, so the first event appended after crash recovery is glued onto the partial line and silently lost on the next restart

**Where:** `internal/store/file/file.go:42` · **Area:** store-ingest

**How it fails:** A crash (or a short write from ENOSPC) leaves the JSONL log ending mid-record with no trailing newline. Open skips the unparseable tail line and opens the write handle O_APPEND *at that offset*. The next Ingest appends `{...}\n` directly after the partial bytes, so the torn fragment and the new event form one unparseable line. That new event was written, indexed, fsynced, and 202-ACKed to the client, and is returned by every query for the rest of the process's life — then it vanishes at the next restart. Verified by probe reproducing the exact shape of TestTornHotLogTailRecovers: after truncating 7 bytes, `after torn-tail reopen, resident: 4`, then two acked ingests give `resident after 2 acked ingests: 6`, then after restart `after restart, on-disk events: 5` with `post-recovery-1` gone (e0,e1,e2,e3,post-recovery-2 survive). This also undermines the segment store, whose hot WAL is a file.Store and whose crash-safety argument assumes the WAL replays completely.

**Fix:** During replay, track the byte offset of the end of the last successfully-parsed line (the scanner's cumulative consumed bytes) and `os.Truncate(path, lastGoodOffset)` before opening the append handle. A cheaper variant that is still correct: if the file does not end in '\n', write a leading '\n' before the first append so the torn fragment stays its own (discarded) line instead of swallowing the next record. Extend TestTornHotLogTailRecovers to ingest after recovery, reopen, and assert the post-recovery events survive.

**Verifier:** CONFIRMED — reproduced in both affected stores.

Code path: internal/store/file/file.go:42-71. Open() scans the JSONL log and `continue`s past any line failing json.Unmarshal ("skip a torn/partial line rather than refuse to start"), then at line 65 opens the write handle os.OpenFile(path, O_CREATE|O_WRONLY|O_APPEND) at the file's current size. Nothing truncates back to the last newline. Repo-wide grep for Truncate/repair/fsck: the only os.File.Truncate is Clear()'s Truncate(0); the only log-rewrite paths (compactToLocked via Prune / DeleteUser / enforceCapLocked) are all conditional and none run at startup.

Triggering input (exact shape of the repo's own segment/hardening_test.go TestTornHotLogTailRecovers): ingest e0..e3, abandon the store without Close, os.Truncate(path, size-7), reopen. Probe output:
  after torn-tail reopen, resident: 3
  resident after 2 acked ingests: 5
  raw log line 4: {"id":"e3",...,"timestamp":"2026-07-02T00:00{"id":"post-recovery-1",...}
  after restart, on-disk events: 4 ids=[e0 e1 e2 post-recovery-2]
post-recovery-1 returned nil from Ingest (written, indexed, fsynced, 202-ACKable), is served by every query for the process's life, and vanishes on resta

## 18. [high] An unknown or deleted ?cohort= id is silently ignored — the full population is returned as if it were the cohort

**Where:** `internal/api/query_api.go:112` · **Area:** api-surfaces

**How it fails:** A saved report / pasted URL / Explore panel references a cohort that was renamed, deleted, or typo'd (or the cohort store failed to open, leaving s.cohorts nil). Verified: with 4 users and no cohorts configured, `GET /v1/trends?event=signup&cohort=does-not-exist` → 200 `{"points":[{"count":4}],"total":4}` — the entire population, labelled nowhere in the response as unscoped. Every other bad scoping input on this path is a hard 400 (unknown filter property, unknown event, malformed filters JSON), so the caller reasonably reads 200 as "this is your cohort".

**Fix:** Return badRequestError when cid != "" and the cohort cannot be resolved (store nil or Get returns !ok), listing the known cohort ids — matching the FirstUnknownProp/knownEventOr400 honesty rule two lines above.

**Verifier:** Reproduced end-to-end. With a real cohort "upgraders" (1 member of 4 users) saved in the store: GET /v1/trends?event=signup&cohort=<realid> returns total:1, while GET /v1/trends?event=signup&cohort=<realid>typo returns 200 total:4 (the full population) with no marker that scoping was dropped. Same on /v1/funnel (converted:1 vs converted:4). Meanwhile GET /v1/cohorts/<badid>/users returns 404 "cohort not found" — the same id is an error on one endpoint and a silent no-op on the query path.

Code path: internal/api/query_api.go:112-116 (filtered) and 140-144 (funnelScoped) are the only query-path callers of s.cohorts.Get, and both discard the ok bool; cohort.Store.Get (internal/cohort/store.go) returns (Definition{}, false) for an unknown id with no error. No middleware, validation, type constraint, or test guards ?cohort=. delete_honesty_test.go:77 only covers DELETE of a missing cohort, not querying with one.

Two corrections to the reporter: (1) s.cohorts is never nil in practice — api.New() always installs an in-memory store and SetCohorts is only called with a non-nil store; but the worse real variant is cmd/smolanalytics/main.go:309, which only calls SetCohorts when cohort.Open

## 19. [high] "last N weeks" / "last N months" silently answers over ALL history instead of the asked window (and isn't refused either)

**Where:** `internal/api/ask.go:1183` · **Area:** ask-nl

**How it fails:** 60 days of data, 1 pageview/day. Ask "how many pageviews in the last 2 weeks" → "60 pageviews from 60 visitors." (verified by running answer()). The true 14-day number is 14. parseWindow has regexes only for `days?`, `hours?`, `min(ute)?s?`; nothing matches "2 weeks". unsupportedTimePhrase then fails to refuse because containsWord() is whole-word: the question contains "weeks", not "week". So win is the zero (all-time) window, and answerWeb prints the total with winSuffix(win)=="" — no time qualifier at all, no refusal. Same for "last 6 months", "past 3 weeks". The receipt compounds it: "Computed by the web-overview report over all recorded events" while the user asked for two weeks.

**Fix:** Add `(?:last|past)\s+(\d+)\s+weeks?` → days=7n and `...months?` → calendar months in parseWindow, and make unsupportedTimePhrase match the plural forms ("weeks", "months", "quarters", "years") so anything still unhandled is refused by name rather than silently widened to all time.

**Verifier:** CONFIRMED by execution. I wrote a throwaway test in package `api` (since deleted) that seeded 60 `$pageview` events, 1/day, ending at now = 2026-06-25T12:00Z, and called `parseWindow`, `answer`, and `computedBy` directly.

Output:
- q="how many pageviews in the last 2 weeks" -> parseWindow: scoped=false, label="", unsupported=""; answer: "60 pageviews from 60 visitors."; receipt: "Computed by the web-overview report over all recorded events..."  (true 14-day answer is 14)
- q="how many pageviews in the last 6 months" -> identical: unscoped, "60 pageviews from 60 visitors."
- q="how many pageviews in the past 3 weeks" -> identical.
- q="how many signups in the last 2 weeks" -> "60 \"$pageview\" events all time (60 days of history), about 1/day."

Code path, verified by reading the full context rather than the quoted lines:
1. /Users/arjun/smolanalytics/internal/api/ask.go:33 — the HTTP handler only does `strings.ToLower(strings.TrimSpace(...))`. No plural stripping, no client-side rewrite (nothing in dashboard.tmpl.html rewrites "weeks"), so the raw phrase reaches the parser.
2. /Users/arjun/smolanalytics/internal/api/ask.go:1119 `parseWindow` — the `strings.Contains` switch matches

## 20. [high] Two-segment questions silently drop BOTH filters in the trend and period-comparison paths

**Where:** `internal/api/ask_scope.go:672` · **Area:** ask-nl

**How it fails:** 10 pro/mobile signups and 10 free/desktop signups. "how many pro signups on mobile" → "10 ... for mobile + pro." (correct). But "pro signups on mobile over time" → "\"signup\" events the last 30 days (UTC): 18 total ..." and "did pro signups on mobile grow this week vs last week" → "\"signup\" events this week ...: 18." (both verified) — the plan and device qualifiers are dropped with no note and the unfiltered total is presented as the answer.

**Fix:** Loop over all extracted segments in both functions (segFilterUsers for userAttr props, segFilter otherwise), building the label as answerSegAnd does — or refuse explicitly rather than answering the unfiltered number.

**Verifier:** Reproduced with a concrete Go test against answer(). Input: 10 signup events with plan=pro/device=mobile and 10 with plan=free/device=desktop. extractSegments("pro signups on mobile over time") returns two found segments (device=mobile, plan=pro). ask.go:593 routes trend questions with `len(segs)==1 || m.kind=="event"`, so a 2-segment question on a named event reaches answerTrendText, whose only filtering is behind `if len(segs) == 1 {` (ask_scope.go:1783) — result: `"signup" events the last 30 days (UTC): 20 total …` (unfiltered, correct answer 10), no note that qualifiers were dropped. Same for the compare path: ask.go:568 dispatches answerCompareWindows before the len(segs)==2 AND branch at ask.go:597, and ask_scope.go:672 has the identical `len(segs)==1` guard — "did pro signups on mobile grow this week vs last week" returned `20 this week vs 8 last week — up (+150%)` when the true pro+mobile figures are 10 vs 4. Control cases confirm the surrounding code is otherwise correct: the single-segment compare says "from pro" and gives 10 vs 4, and answerSegAnd gives the correct "10 … for mobile + pro" for the plain count. No guard, validation, or type constraint blocks the path, and 

## 21. [high] Twitter alias matches the substring "t.co", hijacking any host containing it (producthunt.com) into a wrong-labeled zero

**Where:** `internal/api/ask_scope.go:138` · **Area:** ask-nl

**How it fails:** 15 visitors arrive with referrer=https://producthunt.com/posts/x. "how many visitors from producthunt.com" → "0 — no events with referrer = twitter have been sent, so there's no visitors from twitter in the data. If that's unexpected, check the tracking on that channel." (verified). "producthunt.com" contains the alias literal "t.co", so extractSegments resolves the twitter segment, finds no twitter referrer, and emits an authoritative zero under a channel the user never mentioned. Any "…t.com"/"…t.co" host (hubspot.com, gist.com, producthunt.com) is affected.

**Fix:** Match alias words on host/word boundaries (e.g. require the match be preceded by a non-word char and followed by end/non-alnum, or run hostOf() over any URL-ish token in the question first) instead of strings.Index over the whole padded question.

**Verifier:** Reproduced exactly. Wrote a throwaway test in internal/api with 15 pageviews carrying referrer=https://producthunt.com/posts/x and asked "how many visitors from producthunt.com". extractSegments returned [{prop:referrer value:twitter.com label:twitter found:false ...}] and answer() returned verbatim: "0 — no events with referrer = twitter have been sent, so there's no visitors from twitter in the data. If that's unexpected, check the tracking on that channel." The 15 real visitors are never counted.

Path: ask_scope.go:209 does strings.Index(padded, w) on the raw question with w="t.co" from the twitter alias row (ask_scope.go:138). "producthunt.com" contains "t.co" (…hun-t.co-m). realValue finds no twitter.com/t.co/x.com referrer (hostEquals correctly rejects producthunt.com), and the source/utm_source/channel fallback finds nothing, so a found=false segment reaches answerSegment (ask_scope.go:713) which emits the authoritative zero labeled "twitter".

The existing hostEquals guard (ask_scope.go:527, comment "never by substring (reddit.com literally contains t.co)") protects only the DATA side — comparing a stored referrer value to a wanted host. Nothing guards the QUESTION side; a

## 22. [high] Lifecycle over an hour-based window reports the wrong calendar day under the asked window's label

**Where:** `internal/api/ask_scope.go:1155` · **Area:** ask-nl

**How it fails:** 7 users active only 2 days ago (dormant as of yesterday), 5 users active daily. "how many users went dormant in the last 24 hours" → "7 users went dormant the last 24 hours (UTC)" while "how many users went dormant today" → "0" and "...yesterday" → "7" (all verified). parseWindow gives from = now-24h (12:00 yesterday), and lifecycleDayAt truncates that to yesterday 00:00, so the answer is yesterday's calendar-day row wearing the "last 24 hours" label — off by one day, and the receipt repeats the false window.

**Fix:** Only take the per-day lifecycle row when the window is exactly a calendar day (win.from == win.from.Truncate(24h) and win.to-win.from <= 24h); otherwise anchor on the day containing win.to, or say the report is per-calendar-day and answer that day by name.

**Verifier:** Reproduced end-to-end with a constructed dataset. Input: now=2026-08-04 12:00 UTC, 7 users last active Aug 2 (with older history so they aren't "new"), 5 users active daily. Query "how many users went dormant in the last 24 hours" returns "7 users went dormant the last 24 hours (UTC)", while "today" returns 0 and "yesterday" returns 7.

Path: classifyAsk hits hasAny(q,"churn","dormant",...) at ask.go:367 -> intentLifecycle -> answerScopedIntent case intentLifecycle (ask.go:728) -> answerLifecycle (ask_scope.go:1154). parseWindow's lastNHoursRe branch (ask.go:1159-1164) runs BEFORE unsupportedTimePhrase (which would reject the bare token "hour"), so the hour window is accepted with from = now-24h = yesterday 12:00. win.to.Sub(win.from)=24h <= 48h passes the guard, then lifecycleDayAt (ask_scope.go:1169) does t.UTC().Truncate(24*time.Hour), landing on yesterday 00:00, and prints yesterday's calendar-day lifecycle row under win.label "the last 24 hours (UTC)". computedBy repeats the same false window in the receipt.

No guard prevents it: scoped(), the <=48h check, and unsupportedTimePhrase all let hour/minute windows through, and nothing constrains win.from to be midnight-aligned. No

## 23. [high] Map iteration order leaks into the top-N lists: identical question + identical data returns different countries/browsers/segments each call

**Where:** `internal/api/ask.go:2298` · **Area:** ask-nl

**How it fails:** 8 countries with exactly 3 visitors each. Calling answer("which countries are my users in") 12 times produced 7 DIFFERENT answers (verified), e.g. "JP (3), BR (3), CA (3), US (3), IN (3) and 3 more" vs "US (3), IN (3), DE (3), FR (3), GB (3) and 3 more" — which countries are named changes run to run because rows are built from a map and sorted with an unstable sort.Slice that has no tie-break. Same for answerWebDim (12/12 distinct browser lists) and answerConvBy (6 distinct orderings). The dashboard/API rendering the same data will disagree with the ask bar, and the ask bar with itself.

**Fix:** Add a deterministic final tie-break (value/name ascending) to every sort.Slice whose rows come from a map — as answerTopPages and answerPropBreakdownWith already do (`|| (rows[i].n == rows[j].n && rows[i].v < rows[j].v)`).

**Verifier:** CONFIRMED by direct reproduction against the real answer() entrypoint, not by reading.

Triggering input: 8 countries x 3 distinct visitors each, one $pageview per visitor carrying `country` and `browser`. 30 calls to answer("which countries are my users in", evs, askNow) returned 8 DISTINCT strings (e.g. "US (3), IN (3), DE (3), FR (3), GB (3) and 3 more" vs "JP (3), BR (3), CA (3), US (3), IN (3) and 3 more"). The same fixture with "what browsers do my visitors use" returned 30/30 distinct. answerConvBy(evs, "plan", "signup", ...) with 7 plans at an identical 5-of-10 rate returned 7 distinct orderings, dropping a different plan off the 6-item cap each time.

Code path: internal/api/ask.go:2298-2305 (answerGeo) builds rows by ranging the byCC map then sorts with an unstable sort.Slice whose comparator is only `visitors >`. internal/api/ask.go:1777-1784 (answerWebDim.pick) is the same shape with a second map hop (it also ranges firstPV to build counts). internal/api/ask_scope.go:1313-1326 (answerConvBy) breaks ties on rate then users and leaves fully-equal rows in map order. All three are reached live from answer(): ask.go:749 (intentGeo), ask.go:751 (intentWebDim), ask.go:740 -> a

## 24. [high] Segment visitor counts are any-touch while the traffic-by-source report is first-touch — the two ask answers double-count the same visitors

**Where:** `internal/api/ask_scope.go:721` · **Area:** ask-nl

**How it fails:** 10 visitors land from google, later return via reddit. "where is our traffic coming from" → "Traffic by source (visitors): google.com 10." but "how many visitors from reddit" → "10 visitors from reddit." and "how many visitors from google" → "10 visitors from google." (verified) — 20 attributed visitors out of 10 real ones, and the reddit number contradicts the first-touch report the receipt points the user to ("first-touch attributed", computedBy line 138).

**Fix:** For visitors/pageviews metrics on acquisition props (referrer/source/utm_*), attribute each visitor by their earliest pageview in the window (reuse the firstPV logic in answerSources) instead of matching every event.

**Verifier:** CONFIRMED, and understated. Triggering input (verified by running the real code paths in package `api`): 10 visitors, each with a $pageview referrer=https://www.google.com/ at T-10h and a second $pageview referrer=https://www.reddit.com/r/x at T-2h.

Code path: `answer()` in /Users/arjun/smolanalytics/internal/api/ask.go:637 falls to `default: return answerSegment(...)` because intentSources is NOT in the intent list at ask.go:626 that pre-filters/reroutes segment-scoped questions. `answerSegment` (/Users/arjun/smolanalytics/internal/api/ask_scope.go:721) calls `segFilter(evs, s)` — ANY pageview whose referrer host matches — then `metricCount` counts distinct users. No guard, and no first-touch stamping anywhere on the ask path (ask.go:38-49 applies only query.Apply + WithoutSampler; it never calls query.StampForFilters/StampFirstTouch, unlike Server.filtered at internal/api/query_api.go:110).

Measured output on that fixture:
- ask "where is our traffic coming from" -> "Traffic by source (visitors): google.com 10."
- ask "how many visitors from reddit"   -> "10 visitors from reddit."
- ask "how many visitors from google"   -> "10 visitors from google."
- receipt on both segment an

## 25. [high] readOnly guard allowlist names 8 tools that do not exist and misses 10 that do — delete_user_data, set_project and add_webhook are writable on the unauthenticated public demo

**Where:** `internal/mcp/mcp.go:114` · **Area:** mcp-tools

**How it fails:** The public demo runs `app.SetMCPReadOnly(demoMode)` (cmd/smolanalytics/main.go:397) and `handleMCP` lets anyone in because `authorized()` returns true when no keys are configured (internal/api/api.go:472). A stranger POSTs to https://<demo>/mcp: {"method":"tools/call","params":{"name":"delete_user_data","arguments":{"distinct_id":"u1","confirm":true}}}. `mutatingTools["delete_user_data"]` is false, so the guard at mcp.go:282 lets it through and `s.store.DeleteUser` permanently erases that user's events across all storage tiers. Same for set_project (renames the project and changes the instance TIMEZONE, which re-buckets every report), add_webhook (points the daily digest + alert fires at an attacker URL), delete_saved_report, revoke_api_key, revoke_share_link, set_flag_enabled, set_survey_active, define_event and create_share_link. Meanwhile 8 of the 30 map entries — create_webhook, delete_report, update_flag, delete_api_key, create_defined_event, create_share, delete_share, set_settings — match no registered tool at all, which is why the list reads as complete.

**Fix:** Derive the guard from the registered tools instead of a hand-kept parallel list: add a `mutating bool` field to each toolList entry (or a `mutatingTools` set built in the same init() that registers the tool), and add the mirror of TestPromptsOnlyNameRealTools (control_test.go:199) — a test asserting every key in mutatingTools appears in tools/list, and that every tool whose handler writes to a store or s.store is in mutatingTools.

**Verifier:** CONFIRMED by execution, not just reading. I wrote a throwaway test in /Users/arjun/smolanalytics/internal/mcp/ that sets `s.readOnly = true` (exactly what `SetMCPReadOnly(demoMode)` does) and called the tools directly through `s.callTool`. Results:

- `callTool("delete_user_data", {"distinct_id":"a","confirm":true})` with readOnly=true returned `{"deleted_events":3,...,"note":"erased across all storage tiers — irreversible"}` and no error. The guard did not fire; `s.store.DeleteUser` ran.
- `callTool("set_project", {"name":"pwned","timezone":"Europe/Berlin"})` with readOnly=true succeeded; `settings.ProjectName()` afterwards returned "pwned".
- Enumerating `toolList` against `mutatingTools` confirmed all 8 phantom entries the reporter named: update_flag, delete_report, delete_api_key, set_settings, create_webhook, delete_share, create_defined_event, create_share. None of these are registered tool names.
- The registered-but-unguarded mutators are exactly as reported: add_webhook, delete_saved_report, set_project, revoke_api_key, delete_user_data, set_flag_enabled, set_survey_active, create_share_link, revoke_share_link, define_event (plus test_webhook, which fires a real outbound H

## 26. [high] whats_notable computes the verdict over RAW events while the dashboard's verdict card applies the default production scope — the two surfaces diagnose different data

**Where:** `internal/mcp/mcp.go:771` · **Area:** mcp-tools

**How it fails:** A solo dev runs the app on localhost; sdk.js stamps every one of those events env="development" (internal/api/sdk.js:39) and query.Apply hides them from every dashboard report. The dashboard verdict card calls insight.GenerateForFunnel on `query.Apply(evs, nil)` (internal/api/explore.go:42, dashboard.go:1426) and the anomaly-alert path does the same (api.go:175). The MCP whats_notable tool passes the unfiltered slice. Result: the agent reports an anomaly/funnel-leak/retention read computed over localhost traffic mixed with production, and states numbers the dashboard has never shown and that the user cannot reproduce anywhere. Every other report tool in the same switch (trends, retention, breakdown, web_overview, lifecycle, paths, fix_brief) wraps its input in query.Apply; whats_notable is the one that does not.

**Fix:** insight.Generate(applyDefaultScope(evs)) — the helper already exists at actions.go:542 and is used by goal_report, deploy_impact, flag_impact and survey_results.

**Verifier:** Confirmed, reproduced with a test. internal/mcp/mcp.go:776 calls insight.Generate(evs) on the raw slice from s.all() -> store.Range (mcp.go:249), which does no env filtering in any store backend. insight.GenerateForFunnel (internal/insight/insight.go:95-122) only strips $geo_check events; the default production scope lives solely in query.Keeper (internal/query/query.go:237-261) and is never reached on this path.

Concrete triggering input (memory store, run through s.callTool("whats_notable")): 40 production users doing signup->activate->checkout (30 activate, 24 checkout) plus 100 users with env="development" who only signup. Measured output:
- MCP whats_notable: "only 21% go on to activate, so 110 people stop here. End to end, 17% get from signup to checkout", anomaly n=140, "Day-1 retention 0% ... of 140 users".
- insight.Generate(query.Apply(evs, nil)) (what internal/api/explore.go:42, internal/api/api.go:175 and the dashboard verdict card at internal/api/dashboard.go:1574 compute, scoped via query.Keeper at dashboard.go:1372): "only 75% go on to activate, so 10 people stop here. End to end, 60%", anomaly n=40, "of 40 users".
Every number in all three findings differs for the 

## 27. [high] run_sql bypasses the default dev-env scope every other tool applies, so the escape hatch answers the same question with a different number

**Where:** `internal/mcp/sql_tool.go:41` · **Area:** mcp-tools

**How it fails:** With any localhost/staging/preview traffic in the store (sdk.js auto-stamps env=development for localhost and env=preview for staging.*/netlify branch deploys), `run_sql("SELECT date(timestamp) AS day, count(distinct distinct_id) AS users FROM events WHERE name='signup' GROUP BY day")` — the grammar's own worked example — counts dev users, while `trends(event="signup", unique=true)` excludes them via query.Apply. Two MCP tools, one question, two numbers, and nothing in the tool description or the grammar warns the model. sqlq.Run scans s.store directly and internal/sql contains no reference to query.Keeper/NonProduction. Worse, the agreement test that is supposed to catch exactly this compares run_sql against `overview` — the ONLY other tool that also skips the scope — so it passes for the wrong reason and would keep passing with dev data present.

**Fix:** Wrap the scanner passed to sqlq.Run in a Keeper-filtered decorator (query.Keeper(nil)) so SELECTs inherit the production scope, and let an explicit `WHERE prop.env = 'development'` opt back in — mirroring Keeper's filtersTouchEnv rule. Then re-point TestRunSQLMatchesTheDedicatedTools at a scoped tool (trends unique=true) and seed one env=development event so the test actually exercises the divergence.

**Verifier:** Reproduced directly. With a store containing signups from p1/p2 (env=production), d1 (env=development) and s1 (env=preview), run_sql("SELECT count(distinct distinct_id) AS users FROM events WHERE name='signup'") returns users=4 while trends(event="signup", unique=true) returns total=2 and breakdown(property="env") returns only the production bucket. sql_tool.go:41 calls sqlq.Run(q, s.store, lim) and internal/sql contains no reference to query.Keeper/query.NonProduction; internal/mcp/mcp.go:289-296 dispatches run_sql (and event_source) before the shared s.all() load, so it never reaches the query.Apply(query.StampForFilters(...)) that every other report uses (mcp.go:427-1046). query.go:195-197 states the design invariant this violates verbatim ("Living inside Apply means every surface ... inherits the same rule"). No guard, validation, or type constraint prevents it: Grammar() (internal/sql/exec.go:565-590) documents columns/operators/unsupported syntax but never mentions env, the tool description only says "raw event stream", and sdk.js:39-43 auto-stamps localhost, private IPs, .local/.test and tunnel hosts as development, so non-production events are routinely present. The agreeme

## 28. [high] funnel's inputSchema declares no time window at all, and the handler silently drops `hours`, so windowed funnel questions are answered all-time

**Where:** `internal/mcp/tools.go:48` · **Area:** mcp-tools

**How it fails:** The server instructions tell the model "last 6 hours = hours=6", and every other windowed tool (trends, breakdown, paths, heatmap, agent_*) declares days/hours/from/to. funnel declares none of them, so a schema-validating MCP client strips days/from/to before the call and the tool runs all-time — the exact bug the handler comment at mcp.go:344 claims was fixed. And `hours` is not even a field on the handler's argument struct, so funnel(steps=[...], hours=6) is silently discarded by encoding/json and the model presents an ALL-TIME conversion rate as "conversion in the last 6 hours". A 2-year-old instance answers with two years of data.

**Fix:** Add days/hours/from/to to the funnel inputSchema with the same descriptions the other tools use, add an `Hours float64 \`json:"hours"\`` field, and pass it: mcpWindow(a.Days, a.Hours, a.From, a.To).

**Verifier:** CONFIRMED by execution, both prongs. I wrote a throwaway test against the real MCP dispatcher (internal/mcp/mcp_test.go harness, memory store seeded with 5 users converting 3h ago and 5 users converting 30 days ago) and then deleted it.

Triggering input and observed result (tools/call name=funnel):
- `{"steps":["signup","checkout"],"hours":6}` -> `"steps":[{"event":"signup","count":10},...]`, converted:10 — i.e. ALL TIME.
- `{"steps":["signup","checkout"]}` (no window) -> identical, count 10. So `hours` is provably a no-op.
- `{"steps":["signup","checkout"],"days":1}` -> count 5, so the handler DOES honour days (undeclared in the schema).
- `{"steps":["signup","checkout"],"hours":-5}` -> count 10, silently accepted; trends/paths reject negative hours via mcpWindow (internal/mcp/mcp.go:1324).

Schema evidence dumped from a live tools/list:
- funnel props: step_filters, steps, window_hours, breakdown, breakdown_limit, exclude, filters, order — no days/hours/from/to.
- trends props: ... days, from, hours, to. paths props: days, depth, filters, from, hours, start, to.
So funnel is the only windowed report tool that advertises no time window.

Code path (no guard exists anywhere on it)

## 29. [high] identify() aliases one REAL user into another: switching accounts in a browser irreversibly merges two people

**Where:** `internal/api/sdk.js:682` · **Area:** sdk-js

**How it fails:** Browser is shared (or a user switches accounts without calling reset()). Visit 1: app calls identify("u-alice") → localStorage smol_did = "u-alice". Visit 2 (same browser): distinctId() loads did = "u-alice" from localStorage; Alice logs out via a plain app-side logout (no smolanalytics.reset()) and Bob logs in → identify("u-bob"). prev = "u-alice" ≠ "u-bob", so the SDK sends $identify{$anon_distinct_id: "u-alice"}. Server: alias.RecordFrom → Add("u-alice", "u-bob"); the only guards in Add are empty/self/"$anon" (alias.go:52), so the edge is accepted and persisted to disk. From then on alias.Store.canon() rewrites EVERY historical Alice event to "u-bob" at read time — two humans collapse into one. Unique-user counts, DAU, retention cohorts, funnels and per-user journeys are all silently wrong, permanently (the map is written to the aliases file), and DeleteUser("u-alice") now erases Bob's data too.

**Fix:** Only emit the breadcrumb when prev is an SDK-minted anonymous id. uid() already namespaces them: `if (prev && prev !== id && prev.indexOf("a-") === 0) props.$anon_distinct_id = prev;`. Belt-and-braces server-side: have alias.Add refuse an anon key that is already a canonical target (i.e. appears as a value in a.m), since a previously-identified id has been an alias target before.

**Verifier:** CONFIRMED. identify() at internal/api/sdk.js:672-685 writes the account id into the same localStorage key ("smol_did") that distinctId() reads back into `did` (sdk.js:93-107, 676). enqueue() calls distinctId() on every event (sdk.js:250) and default autocapture fires a $pageview at init, so on a returning visit `did` already holds the PREVIOUS ACCOUNT'S id. The only guard is `if (prev && prev !== id)` — nothing checks that prev is anonymous, even though anonymous ids are distinguishable ("a-" prefix from uid() at sdk.js:71), and no identified-flag is kept. Two identify() calls with different ids in a single page load trigger it too, no reload needed.

Server side offers no second line of defense: internal/api/api.go:804 calls alias.RecordFrom unconditionally on ingest (internal/mcp/import_tool.go:103 likewise), and internal/alias/alias.go:50-61 guards only empty / self / "$anon". No check that the anon-side id is not itself a canonical id with events.

I constructed and ran the concrete input (temporary Go test in internal/alias, since removed; tree clean):
RecordFrom(am, event.Event{Name:"$identify", DistinctID:"u-bob", Properties: map[string]any{"$anon_distinct_id":"u-alice"}})
R

## 30. [high] flush() splices the WHOLE queue into one keepalive request — once the queue exceeds the 64KiB keepalive limit it can never be delivered again

**Where:** `internal/api/sdk.js:176` · **Area:** sdk-js

**How it fails:** keepalive:true is set on every flush, not just the unload one. Per the Fetch spec a keepalive request whose body exceeds 64KiB (and Chrome's shared per-origin inflight quota, also 64KiB) fails immediately with a TypeError — it never reaches the network. Two ways to get there: (a) a normal 20-event batch of autocaptured $click events, each carrying $elements (up to 5 elemDesc, each up to ~900B of id/classes/aria/href/data/text) → 20-90KB; (b) any 5xx or offline period requeues, the queue grows to the 1000 cap, and the next flush splices ALL 1000 events into a single body. In both cases fetch rejects synchronously-ish, .catch(requeue) puts the identical oversized batch back, and the 3s timer retries the same doomed body forever. Every event on that page is lost, with no console warning (the warn path only runs on an HTTP response, which never arrives), and the queue cap then starts discarding the oldest events. On a click-heavy page the dashboard silently shows zero for that visitor.

**Fix:** Cap the batch (`queue.splice(0, Math.min(queue.length, 50))`) AND cap the serialized body: build the body incrementally and stop before ~60000 bytes, pushing the remainder back. Use keepalive:true only on the pagehide/visibilitychange-hidden flush (pass a flag into flush), where the request must outlive the document; normal timer flushes should use a plain fetch with no size ceiling.

**Verifier:** CONFIRMED. internal/api/sdk.js:174-206 is the only send path in the shipped SDK (//go:embed sdk.js at internal/api/api.go:57, served at GET /sdk.js; only one copy of the file exists in the repo, no dist build with chunking). flush() splices the whole queue into a single body and sets keepalive:true on EVERY flush — the 3s setInterval (line 645), the >=20-event flush in enqueue (line 254), and the unload handlers all call the same function. There is no byte-size cap, no chunking, and no unload-only keepalive branch. requeue() (line 183) caps by count (1000), never by bytes, so it can never shrink an oversized body.

No guard, validation, or type constraint prevents it, and no test covers it: the only SDK test is internal/api/sdktest/env.test.mjs, which regex-extracts detectEnv and tests hostname classification only — nothing touches flush, batching, or requeue. The server does not save it either: internal/api/api.go:615 accepts up to 4 MiB, so the body would be fine if it ever left the browser; the rejection happens client-side in the Fetch layer (a keepalive request with a body over 64 KiB returns a network error, so the promise rejects with TypeError and lands in .catch(requeue)).

## 31. [high] $click captures input VALUES (checkbox/radio value attributes) despite the SDK promising metadata only

**Where:** `internal/api/sdk.js:452` · **Area:** sdk-js

**How it fails:** isClickable() explicitly treats <input type=checkbox|radio|submit|button> as a click target (sdk.js:321). For an input element innerText is "", so the || chain falls through to el.value. A page with `<input type="checkbox" name="invite" value="jane@corp.com">` (a completely standard multi-select over records — emails, account ids, order numbers) sends `$click{text: "jane@corp.com"}` plus the same string again inside $elements[0].text. The value is PII the operator never opted into collecting, it is stored raw in the event store, and it shows up in recent_events, session timelines and any breakdown on `text`. The same code path in elemDesc (sdk.js:289) re-reads el.value for the chain.

**Fix:** Drop the `|| el.value` fallback entirely and derive a label for inputs from safe metadata only: the associated <label> text, aria-label, or the value ONLY for type=submit/button (where it is the rendered caption). Same in elemDesc. While there, strip the query string from d.href/props.href (sdk.js:279, 456) — reset tokens and mailto: addresses land there today.

**Verifier:** CONFIRMED — not refuted. I reproduced the exact code path in a real browser (Playwright), running the `isClickable` and text-extraction expressions verbatim from `/Users/arjun/smolanalytics/internal/api/sdk.js`.

TRIGGERING INPUT (concrete, standard HTML):
`<input type="checkbox" name="invite" value="jane@corp.com">`

MEASURED RESULT: `innerText === ""` (falsy), `value === "jane@corp.com"`, `isClickable() === true`, and the sdk.js:452 expression `((target.innerText || target.value || "") + "").trim().slice(0,80)` yields `"jane@corp.com"`. Radio behaves identically (`value="user_44821"` -> captured). The same string is emitted a second time via `elemChain(target)` -> `elemDesc` (sdk.js:289), which uses the identical `innerText || value` fallback.

PATH IS REACHABLE BY DEFAULT: sdk.js:641 gates on `opts.autocapture !== false`, so the standard one-line snippet enables the click listener. sdk.js:435 binds on `document` in capture phase; the walk at 440-445 selects the input itself as `target` because `isClickable` explicitly admits `input[type=checkbox|radio]` (sdk.js:321).

NO GUARD EXISTS:
- No redaction/masking/scrubbing anywhere — grep for redact|mask|scrub|sanitiz across `internal

## 32. [high] $deadclick fires on working controls: the MutationObserver watches childList only, so attribute/text-only updates, new-tab links and shadow-DOM apps are all reported as dead clicks

**Where:** `internal/api/sdk.js:354` · **Area:** sdk-js

**How it fails:** armDeadClick declares a click dead if no childList mutation occurs under document.documentElement within 1s and the pathname is unchanged. Every one of these WORKING interactions satisfies that: (1) a dropdown/accordion/modal opened by toggling a CSS class — an attributes mutation, not childList; (2) a React text update, which sets node.nodeValue on an existing text node — a characterData mutation, not childList; (3) `<a target="_blank">`, `mailto:`, `tel:` and download links — page stays put, DOM untouched; (4) any web-component/shadow-DOM UI, since the observer is not registered with a shadow root; (5) a button whose fetch resolves after 1s; (6) a filter/tab that only changes location.search or the hash (pathname compare is unchanged). Every such click emits $deadclick, which insight/human.go renders to the user as "clicked something that did nothing" and defined.go exposes as a first-class base event for retroactive definitions — so the product confidently tells the builder to go fix a control that works.

**Fix:** Observe `{ childList: true, subtree: true, attributes: true, characterData: true }`, and skip arming entirely for targets that intentionally cause no local DOM change: anchors with target=_blank / download / a non-http(s) protocol (mailto:, tel:), and elements inside a form that is submitting. Also compare location.href rather than location.pathname so query/hash-driven UI is not counted as dead.

**Verifier:** CONFIRMED. Read sdk.js in full plus the caller and the Go consumers; no guard prevents the scenario.

Code path: internal/api/sdk.js:465 calls armDeadClick(target) unconditionally inside the capture-phase click listener. The only early returns above it are data-sa-ignore and "no clickable ancestor" — there is no check for href, target="_blank", mailto:/tel:, download, or element kind. armDeadClick (sdk.js:350-366) observes document.documentElement with { childList: true, subtree: true } only. grep over the entire file finds zero occurrences of `attributes:`, `characterData:`, `shadowRoot`, or `attachShadow`, so attribute-only mutations (CSS class toggles: dropdowns, accordions, modals), characterData-only mutations (React setting node.nodeValue on an existing text node), and anything inside a shadow root are all invisible. The navigation check compares location.pathname only, so query-string/hash-only filter and tab changes also read as "no navigation".

Concrete triggering input: a static page containing <a href="https://twitter.com/x" target="_blank">Follow</a>. Click → isClickable returns true on the anchor → $click enqueued → armDeadClick arms. The new tab opens, the current do

## 33. [high] Cookieless mode silently disables feature flags and surveys entirely — flag() returns the default for 100% of visitors and onFlags() never fires

**Where:** `internal/api/sdk.js:510` · **Area:** sdk-js

**How it fails:** With init(key, {anonymous: true}), distinctId() returns the "$anon" sentinel WITHOUT ever assigning the module-level `did` (sdk.js:96 returns before the assignment on line 99-103). fetchFlags() and fetchSurveys() are both gated on `did` being truthy, so neither request is ever made. Consequences: flag(name, def) always returns def, so every cookieless visitor is force-bucketed into the fallback branch; no $feature_flag_called exposures are ever enqueued, so an A/B test on a cookieless site reports zero exposures and an empty/garbage experiment result rather than an error; onFlags(cb) callbacks are pushed onto flagListeners and never invoked, so an app that gates its render on onFlags never renders at all. Nothing is logged.

**Fix:** Gate on host+key only and send the resolved id: `var id = distinctId(); if (!host || !key || !id) return;` then `...?distinct_id=` + encodeURIComponent(id) + `&bucket_id=` + encodeURIComponent(bucketId()). The server already understands "$anon" on ingest (api.go:794) and bucketing is done off bucket_id anyway, so cookieless flag evaluation is well-defined. If cookieless flags are genuinely unsupported, say so once via console.warn and fire the onFlags listeners with {} so callers unblock.

**Verifier:** CONFIRMED by execution, not just reading. I loaded /Users/arjun/smolanalytics/internal/api/sdk.js into a Node `vm` context with stubbed window/document/localStorage/fetch and ran two cases.

Triggering input: `smolanalytics.init("wk", { host: "https://h", anonymous: true })`.

Result — cookieless: ZERO fetches issued, `flag("checkout-copy","ctrl")` returned `"ctrl"` (the default), `onFlags(cb)` never fired. Control run without `anonymous: true`: both `/v1/flags/evaluate?distinct_id=…&bucket_id=…` and `/v1/surveys/active` were requested, `flag()` returned the server value `"test"`, and `onFlags` fired.

Code path: sdk.js:96 `if (anon) return "$anon";` returns before the `did` assignment at 99-103. grep shows `did` is only ever assigned at 99/101/105 (the non-anon branch) and 677 (`identify`). init() calls distinctId() (638), fetchFlags() (639), fetchSurveys() (640) — with `did === null`, the guards at 510 (`!did`) and 541 (`!did`) both return silently. Nothing is logged.

No existing guard/validation/test prevents it. There is no SDK test exercising `anonymous: true` anywhere in the repo; internal/api/bucket_stability_test.go tests the Go endpoint directly and never touches the SDK 

## 34. [high] Retention grid's observability check counts DAYS even when the bucket is weeks or months, so future periods render as "0%"

**Where:** `internal/api/dashboard.go:1930` · **Area:** dashboard-render

**How it fails:** ?rbucket=week&rdays=7 on 2026-08-04. retention.ComputeBucketed buckets by bucketSeconds("week")=604800, so the newest cohort's Date is the epoch-week start 2026-07-30 (cohortDay=20664, today=20669). The cell guard is `cohortDay+d > today` with d counted in WEEKS but added as DAYS, so for d=1..5 the guard is false and cells W1..W5 render with Label "0%" — those are the weeks starting 2026-08-06, 08-13, 08-20, 08-27 and 09-03, none of which have begun. The reader sees "this week's cohort: 0% week-1, 0% week-2 …" for weeks in the future. retention.SerializeCohorts (what /v1/retention and the MCP retention tool return) nulls exactly those periods (`if n >= 1 && cp+int64(n) >= cur { cj.Returned[n] = nil }`, using bucketSeconds), so the dashboard prints fabricated zeros where the API says null. Month buckets are worse: bs=30d, so up to 29 future months render as 0%.

**Fix:** Use the same bucket length the Result was computed with, exactly as retention.SerializeCohorts does: derive `bs := bucketSeconds(rr.Bucket)` (export it, or call retention.SerializeCohorts and render its []*int directly), then compare `cohortPeriod+int64(d)` against `now/bs`. Rendering the already-nulled SerializeCohorts output is the version that cannot drift from /v1 and MCP.

**Verifier:** REAL — reproduced with a live test against the actual code paths.

Triggering input: dashboard with `?rbucket=week&rdays=7` (reachable from the UI: `internal/api/dashboard.tmpl.html:1872` `<select id="retBucket">` → line 3004 `setParam('rbucket', ...)`; `rbucket` is validated to day|week|month at `/Users/arjun/smolanalytics/internal/api/dashboard.go:1626-1631` and passed straight to `retention.ComputeBucketed(evs, rdays, retEvent, rbucket, rroll)` at line 1663).

I ran a throwaway test in `internal/api` at now = 2026-08-04T12:00Z with four events (two users active this week, one user active 20 days ago and again 6 days ago), replicating the exact grid loop from `dashboard.go:1930-1956` and comparing it against `retention.SerializeCohorts` (what `/v1/retention` and the MCP retention tool return). Output:

  cohort 2026-07-09 (cohortDay 20643, today 20669) size=1 returned=[1 0 1 0 0 0 0 0]
    dashboard:  W0=100% W1=0% W2=100% W3=0% W4=0% W5=0% W6=0% W7=0%
    api:        W0=1   W1=0   W2=1    W3=null W4=null W5=null W6=null W7=null
  cohort 2026-07-30 (cohortDay 20664, today 20669) size=2 returned=[2 0 0 0 0 0 0 0]
    dashboard:  W0=100% W1=0% W2=0% W3=0% W4=0% W5=0% W6=[blank] W7=

## 35. [high] Retention grid renders the in-progress period as a finished number, contradicting /v1/retention and the summary

**Where:** `internal/api/dashboard.go:1930` · **Area:** dashboard-render

**How it fails:** Daily bucket, a cohort from 3 days ago, viewed at 00:20 UTC. The cell guard blanks only STRICTLY future periods (`cohortDay+d > today`), so d=3 — the day that started 20 minutes ago — renders as a final "4%". retention.SerializeCohorts nulls it (`cp+int64(n) >= cur`) and retention.PeriodN excludes that cohort from the summary denominator (`cp+int64(n) < cur`), so /v1/retention, the MCP tool and the dashboard print three different reads of the same cell, and the dashboard's is the one that reads as a collapse. RetentionReady at line 1916 has the same `<= today` off-by-one, so a grid can be declared "ready" on the strength of a period that has not finished.

**Fix:** Match SerializeCohorts: blank the cell when `cohortPeriod+int64(d) >= currentPeriod` for d>=1 (period 0 always shown), and use the same `>=` rule in the retObservable loop at line 1916.

**Verifier:** CONFIRMED and understated. Reproduced against the real handler (s.dashboard) with a memory store.

Daily case, exact triggering input: cohort anchored at today-2 days (10 users), 5 return on D1, 1 returns today 30 minutes after midnight UTC. Dashboard renders `Aug 2 | 10 | 100% | 50% | 10% | empty...` while GET /v1/retention?days=7 on the same store returns "returned":[10,5,null,null,...] and the summary reports only day1_retention_pct=50 (day 2 omitted from the denominator). So the grid prints a finished 10% for a period 30 minutes old, contradicting both the API cohort grid and the summary, and reading as a 50%->10% collapse. Cause is exactly as reported: dashboard.go:1934 blanks only `cohortDay+int64(d) > today` (period STARTED), while retention.SerializeCohorts uses `cp+int64(n) >= cur` and retention.PeriodN uses `cp+int64(n) < cur` (period FULLY ELAPSED). internal/retention/retention_test.go:97 (TestSerializeCohortsNullsInProgressPeriod) deliberately pins the fully-elapsed rule for the package; the dashboard never adopted it, and no dashboard test covers the grid cells.

Severity raised to high because the same expression has a second, worse defect the reporter missed: `today`

## 36. [high] "<event> by channel" pane counts converters over ALL history while the tile above it counts the same event over the selected window

**Where:** `internal/api/dashboard.go:2076` · **Area:** dashboard-render

**How it fails:** Set the range to 7d on an instance with a year of data. The KPI tile reads "signup · 7d — 12" (tr.Total over curFrom..curTo) while the sources pane directly below reads "signup by channel: direct 1,840 · 61%" — the `converters` set is built from the unwindowed `evs`, so it is every user who ever fired the event, and switching the range control changes nothing in that pane. The provenance line renders "window <b>{{.RangeLabel}}</b> · recomputed from raw events on every load · the API and your agent get byte-identical answers", so the page states a window it does not honour here. (funnel/ConvBySeg are computed on the same unwindowed slice.)

**Fix:** Scope this block to the page window before tallying (filter `evs` to [curFrom, curTo) for both the firstOf map's conversion side and the converters set, exactly as web.ComputeRange and trends.ComputeInterval are scoped), or label the pane "all time" so the number and its caption agree.

**Verifier:** CONFIRMED by running the real handler, not by reading it. I could not find any guard, and I built the exact triggering input.

**The code path.** `evs` is loaded once at `internal/api/dashboard.go:1376` via `s.store.Scan(time.Time{}, time.Time{}, ...)` — zero/zero bounds, i.e. ALL HISTORY. Tracing every subsequent assignment (lines 1388, 1414, 1524, 1526, 1529) shows `evs` is only ever narrowed by `?site=`, `?env=`, and the `?f=` chips. It is never trimmed to the range window. Windowing is applied per-report by passing `curFrom/curTo` down (e.g. `trends.ComputeInterval(evs, trendEvent, curFrom, curTo, …)` at 1693, `web.ComputeRange(evs, curFrom, curTo)` at 2204). The block at 2074-2126 passes no bounds at all: `converters` is built by scanning the full `evs` for `e.Name == trendEvent`, so it is every user who ever fired the event.

**Repro 1 — the pane ignores the range control.** 365 days of history, one signup/day from a distinct user. Rendered output:
- `/?days=7` → tile `signup · 7d` = **6**, provenance `window <b>7d</b>`, pane `signup by channel: direct 365 · 100%`
- `/?days=30` → tile **29**, pane `direct 365 · 100%`
- `/?days=90` → tile **89**, pane `direct 365 · 100%`

The 

## 37. [medium] Rules are not first-match-wins: a user filtered into a low-rollout rule falls through to the next rule, so targeting can be bypassed entirely

**Where:** `internal/flag/flag.go:66` · **Area:** flag-experiments

**How it fails:** Flag with Rules = [{Filters: plan==free, RolloutPct: 10}, {RolloutPct: 100}] — the standard "free users get 10%, everyone else gets it" shape. A free user who falls outside the 10% bucket hits `continue` and is then served by the catch-all rule 2. Measured over 5,000 free ids: 5000/5000 (100%) served, where the configured rule says 10%. The flag's own doc comment promises "Rules are evaluated in order, first match wins", and POST /v1/flags accepts arbitrary multi-rule flags, so this is reachable from the API and from any hand-written flags.json.

**Fix:** Once a rule's Filters match, that rule decides the outcome: return ("", false) when RolloutPct<=0 or the user is outside the bucket, instead of continuing to later rules. Only a filter miss should advance to the next rule.

**Verifier:** CONFIRMED, with severity downgraded from high to medium.

Reproduced it. I dropped a temporary test into /Users/arjun/smolanalytics/internal/flag/ with exactly the reported shape:

  Flag{Key: "checkout_v2", Enabled: true, Rules: []Rule{
    {Filters: []query.Filter{{Property: "plan", Op: query.Eq, Value: "free"}}, RolloutPct: 10},
    {RolloutPct: 100},
  }}

evaluated for 5,000 ids with context {"plan":"free"}. Result: 5000/5000 (100.0%) served, against a configured 10%. The rollout percentage on the targeted rule is fully ignored. Temp test removed afterwards; the package is back to its original 10 files.

Why nothing prevents it:
- /Users/arjun/smolanalytics/internal/flag/flag.go:59-71 — the loop `continue`s on all three misses (filters, RolloutPct<=0, rollout bucket), so a user who matched a rule's Filters but lost its rollout falls through to later, broader rules. This directly contradicts the type's own doc at flag.go:26-27 ("Rules are evaluated in order, first match wins").
- /Users/arjun/smolanalytics/internal/flag/store.go:63-85 — Save validates only key presence, rollout in 0..100, and variant weights. No rule-count or ordering constraint, so the multi-rule flag persists

## 38. [medium] delta_pct silently stays 0 when the control arm has zero conversions — an infinite lift renders as "no change"

**Where:** `internal/flag/measure.go:142` · **Area:** flag-experiments

**How it fails:** Control 0/200, test 60/200. Measure returns for the test arm {RatePct:30, DeltaPct:0, Significant:true} (verified). The dashboard prints delta_pct with the neutral class (`v.delta_pct>0?'up':v.delta_pct<0?'down':'mut'`) so the row reads "0" vs control while also reading "significant at 95% against control" — the single biggest win the tool can find is displayed as no difference, and 0 is indistinguishable from a genuinely flat result.

**Fix:** Make the absent case explicit rather than 0: use *float64 (omitempty) for DeltaPct, or a DeltaDefined bool, and have the dashboard/MCP render "n/a — control converted 0 of N" instead of 0. Same treatment for PValue, which also defaults to 0 ("p=0") on the control row itself.

**Verifier:** Reproduced with the exact stated input. Measure(evs, "banner", "purchase", 30) with control arm "a" = 200 exposed / 0 converted and arm "b" = 200 exposed / 60 converted returns for "b": {"rate_pct":30,"delta_pct":0,"significant":true,"small_sample":false}. Code path confirmed at internal/flag/measure.go:140-153 — ctrl.exposed>0 passes (200) so the comparison block runs, ctrlRate = 0/200 = 0 fails the `if ctrlRate > 0` guard at line 142, so DeltaPct keeps its zero value, while significant() at line 170 (which has no control-conversion guard, only minArmForStats=30 on each arm; se≈0.0357, z≈8.4) returns true. No earlier validation, type constraint, or caller filter prevents it.

No existing test covers this. TestLiftRefusedWhenControlIsNearZero (internal/flag/interval_test.go:112) deliberately pins liftInterval returning ok=false for a zero-conversion control, but that governs DeltaCI, not DeltaPct. TestMeasureABWin only exercises a healthy control.

The dashboard claim is accurate: internal/api/dashboard.tmpl.html:3771-3778 constructs its own read string (v.significant ? 'significant at 95% against '+j.control) and never renders the server's v.read field, so the sentence that explai

## 39. [medium] When the TEST arm has zero conversions, the report blames the CONTROL rate and hides a total-loss arm behind "read the raw counts"

**Where:** `internal/flag/interval.go:85` · **Area:** flag-experiments

**How it fails:** Control 60/200 (30%), test 0/200. liftInterval bails on `cTest <= 0`, so ok=false, and readLift emits the not-ok branch: "the control rate is too close to zero for a relative comparison to mean anything — read the raw counts instead" (verified verbatim on that input). The control rate is 30%, nowhere near zero; the arm that killed conversion outright gets a sentence that misdiagnoses which arm is degenerate and reads as a data-quality shrug rather than "this arm converts nobody".

**Fix:** Distinguish the failure reasons: return a reason code (control-near-zero vs test-zero-conversions vs rate-at-100%) from liftInterval and have readLift say "this arm converted 0 of 200 while control converted 60 of 200 — a relative lift is undefined, but this arm is strictly worse" for the cTest==0 case.

**Verifier:** Reproduced verbatim. Input: liftInterval(cTest=0, nTest=200, cCtrl=60, nCtrl=200, z95). Path: measure.go:147 sets enough=true (both arms 200 >= minArmForStats=30), so readLift's !enoughSample branch does not fire. interval.go:85 bails on `cTest <= 0` — the first guard, reached before the genuine near-zero-control check at line 96 — returning ok=false. readLift (interval.go:118-120) then emits "the control rate is too close to zero for a relative comparison to mean anything — read the raw counts instead", even though the control rate is 30%. Measured output from a scratch test compiled into internal/flag: ci={0,0,0} ok=false, p=4.4e-17, significant=true, and the string above exactly. The identical string is produced by the mirror-image input liftInterval(60,200,0,200), so the two opposite degenerate conditions are indistinguishable to the reader.

No guard prevents it: interval_test.go:116 deliberately covers only the CONTROL-zero case (liftInterval(10,100,0,100)); interval_test.go:148 asserts merely that the not-ok string contains "raw counts", which passes regardless of which arm is degenerate. Nothing pins the wording against a zero-test-arm input.

Blast radius: measure.go:153 s

## 40. [medium] experiment_health coerces days:0 to 30, contradicting its own "0 = all time" contract

**Where:** `internal/mcp/flags.go:214` · **Area:** flag-experiments

**How it fails:** An agent calls experiment_health(key:"exp", days:0) to check the split over the whole experiment, exactly as the tool schema documents. p.Days becomes 30, CheckSRM windows to the last 30 days, and an SRM that occurred on day 40 of a long-running experiment is invisible. There is no way to request all-time from MCP (omitting days is also 0), and the returned SRMResult carries no field saying which window was used, so the caller reports "traffic split looks correct" for a window it never asked for. flag.CheckSRM/Measure both treat days<=0 as all-time, so the engine supports what the schema promises; only this coercion blocks it.

**Fix:** Make days a *int: nil (absent) → 30, explicit 0 → all time. Also echo the effective window in SRMResult so the number is self-describing on every surface.

**Verifier:** CONFIRMED by execution, not just reading.

Code path (all in /Users/arjun/smolanalytics/internal/mcp/flags.go):
- Schema line 74 documents: `"days": {"type":"integer","description":"window in days, default 30 (0 = all time)"}`.
- Handler lines 200-224: `Days int` (so an omitted key and an explicit `0` are indistinguishable), then `if p.Days == 0 { p.Days = 30 }`, then `flag.CheckSRM(evs, f, p.Days)`.
- /Users/arjun/smolanalytics/internal/flag/srm.go:147-151 `firstExposures` sets `cutoff = MinInt64` only when `days > 0` is false, i.e. the engine genuinely implements all-time for `days <= 0`. Nothing between the handler and the engine re-widens the window, and MCP is the only production caller of CheckSRM (grep: only flags.go:224 plus internal/flag/srm_test.go).

No guard exists: `unmarshalArgs` (internal/mcp/filters.go:95) only reports type errors; there is no validation, clamp, or normalization of Days for experiment_health, and no test in internal/mcp covers experiment_health at all (the only days-related test there is TestTrendsDaysClampNoDoS for trends).

Concrete triggering input, run against a real server (temporary test, since deleted):
- Seeded 5,200 control / 4,800 test exp

## 41. [medium] Exposure dedupe is per page-load, not per session, so $feature_flag_called is re-logged on every navigation

**Where:** `internal/api/sdk.js:54` · **Area:** flag-experiments

**How it fails:** flagExposed is a plain in-memory object with no sessionStorage backing, so it resets on every full page load. A visitor who views 12 pages in one session on a measured flag emits 12 $feature_flag_called events instead of 1. The A/B math is protected (Measure and firstExposures both dedupe to the user's earliest exposure), but the raw event count, the plan meter, and the trends/list_events numbers for $feature_flag_called are inflated by roughly pageviews-per-session — while the SDK comment, the dashboard note and the create-flag copy all state "exactly one event per visitor per session".

**Fix:** Persist the dedupe in sessionStorage keyed by the session id (the same key the rest of the SDK uses for `sess`), falling back to the in-memory object when storage is unavailable — or change the three copy strings to say "per page load".

**Verifier:** CONFIRMED — the bug is real.

Code path (internal/api/sdk.js):
- Line 54: `var flagExposed = {};` lives inside the SDK's top-level IIFE. It is plain in-memory state. `grep -n flagExposed internal/api/sdk.js` returns exactly three hits (54 declaration, 697 read, 698 write) — there is no sessionStorage/localStorage backing, no rehydration, and `grep -rn sessionStorage internal/` returns nothing repo-wide. The IIFE re-executes on every full document load, so the map is empty again after each hard navigation.
- Lines 697-700: `if (has && flagMeasured[name] && !flagExposed[name]) { flagExposed[name] = true; enqueue("$feature_flag_called", ...) }`. Nothing else guards the enqueue.
- The SDK *does* have a real session concept it could have keyed this to — `ensureSession()` (lines 145-166) persists `smol_session` in localStorage with a 30-minute idle rotation, and `enqueue` even stamps `session_id` on the very exposure event being emitted (line 244). So the same session id is attached to each duplicate, which is what makes the duplication demonstrable rather than theoretical.

Concrete trigger:
- A server-rendered / multi-page site (Astro, Rails, plain HTML, or a Next.js app doing hard nav

## 42. [medium] Per-step property filters only match string-typed values — a numeric or boolean property silently zeroes the step and every step after it

**Where:** `internal/funnel/funnel.go:236` · **Area:** funnel-retention

**How it fails:** Properties are `map[string]any` and arrive from JSON, so a numeric property is a float64 and a boolean is a bool (see internal/api/r5b_regressions_test.go:117 `Properties: map[string]any{"amount": 50.0}`). Call GET /v1/funnel?steps=signup|checkout&sf1=amount:50 (or MCP step_filters: [null, {"is_pro":"true"}]). stepMatches does `e.Properties[k].(string)`, which fails on float64/bool and yields got=="", so `"" != "50"` and the step matches NOTHING. The funnel returns step 1 count = 0, conversion 0%, DroppedFromPrev = full population — a confident, real-looking zero with no error. The same value matched through the ordinary filter engine does match, because query.Filter.match stringifies both sides with toStr: `filters=[{"property":"amount","op":"eq","value":50}]` returns the events. So the two filtering surfaces of the same product disagree on the same property. contracts_test.go:276 only ever exercises a string value ({"plan":"pro"}), so nothing catches it.

**Fix:** Use the same stringification as the filter engine: `v, ok := e.Properties[k]; if !ok || toStr(v) != want { return false }` (export query.ToStr, or mirror its 3-line body in funnel to avoid the import cycle). Add a test with Properties{"amount": 50.0} and StepFilters [nil, {"amount":"50"}] asserting the step matches.

**Verifier:** Confirmed real and reproduced end-to-end. internal/funnel/funnel.go:234 does `got, _ := e.Properties[k].(string)`, which yields "" for float64/bool values, so any per-step filter on a non-string property matches nothing. Concrete trigger, verified by running a test against the real HTTP handler with real ingest: POST /v1/events with u1 checkout {"amount":50,"plan":"pro"} and u2 checkout {"amount":10,"plan":"free"}; then GET /v1/funnel?steps=signup,checkout&sf1=plan:pro returns checkout count=1, conversion 0.5, converted=1 (correct), while GET /v1/funnel?steps=signup,checkout&sf1=amount:50 returns checkout count=0, conversion 0, dropped_from_prev=2, converted=0 — a confident, error-free zero. Same with a boolean (sf1=is_pro:true -> 0). The other filtering surface disagrees on the identical property: /v1/trends?event=checkout&filters=[{"property":"amount","op":"eq","value":50}] (and "50") returns total=1, because query.Filter.match stringifies both sides via toStr (internal/query/query.go:50-58, 305-313). No guard prevents it: Options.StepFilters is []map[string]string so `want` is always a string; properties arrive as map[string]any from JSON (float64/bool) and nothing normalizes th

## 43. [medium] ComputeMeasure's Total is computed over all matched events while its Points are span-capped, so the series contradicts its own total

**Where:** `internal/trends/trends.go:296` · **Area:** trends-query

**How it fails:** Two "buy" events: amount=1000 dated 1970-01-01 (bad epoch) and amount=5 dated yesterday. `GET /v1/trends?event=buy&measure=sum&property=amount` (unbounded window). `all` is built before the cap, so res.Total = 1005, but lo is moved to hi-4200 so the 1970 bucket is never emitted. Verified by running the real code: `measure Total: 1005 N: 2  sum of emitted points: 5  points: 4201`. The revenue headline says 1005 while every rendered bar sums to 5 — a 200x self-inconsistency inside a single response, which is exactly the failure the engine's positioning forbids. Note this is the mirror image of the Compute bug above: there the Total under-counts, here it over-counts, so the two surfaces also disagree with each other.

**Fix:** Make one decision about the capped days and apply it to both outputs: either drop the capped-out values from `all` before `applyMeasure` (Total then matches the points), or don't move `lo` at all and cap only the rendered slice while keeping Total over `all`. Right now Compute and ComputeMeasure pick opposite answers for the same data.

**Verifier:** CONFIRMED and reproduced end-to-end, but the reporter's literal repro is wrong and severity is medium, not high.

Code path (/Users/arjun/smolanalytics/internal/trends/trends.go:240-312): `all` is accumulated inside the ingest loop, gated only by the [from, to) window filter (lines 254-259). The span cap at 296-298 (`lo = hi - maxDayBuckets`) runs afterwards and bounds only the emission loop at 300. `res.Total = applyMeasure(m, all)` / `res.N = len(all)` (308-309) therefore include events whose bucket was cut off. No guard prevents it: Finite() only checks Inf/NaN, and neither caller (internal/api/query_api.go:322, internal/mcp/mcp.go:544) reconciles Total against Points.

Reachability of the reporter's stated input: the 1970-01-01 event is NOT storable via HTTP ingest — internal/api/api.go:759 sets minPast = 2000-01-01 and rewrites anything older to now (pinned by TestIngestClampsAncientTimestamp, internal/api/r5_regressions_test.go:41). So the report's exact repro fails on that path. It is still reachable two ways: (a) any timestamp from 2000-01-01 up to ~11.5 years ago passes the floor untouched, and today is 26 years past that floor, so a >4200-day span is trivially achieved; (

## 44. [medium] ComputeXAU's lookback floor keeps `from`'s time-of-day, so WAU/MAU silently undercount on any window that doesn't start at midnight

**Where:** `internal/trends/trends.go:694` · **Area:** trends-query

**How it fails:** Eight users, each active on exactly one of eight consecutive days at 02:00 UTC. Query the 7-day rolling actives for the last day with an unaligned start — which is what `?hours=N` produces (`parseTrendWindow` returns `now.Add(-n hours)`) and what an explicit `?from=<RFC3339>` produces: from = day7 10:00. lookbackFrom = from.AddDate(0,0,-6) = day1 **10:00**, so day1's 02:00 event is filtered out before the rolling window ever sees it. Verified by running the real code: unaligned from → wau total 6; midnight-aligned from → wau total 7. `GET /v1/trends?measure=wau&hours=6` and the MCP `trends(measure="wau", hours=6)` therefore report a WAU that is short by up to a full day's worth of users, with no indication anything was clipped. Only the days=N path is safe, because parseTrendWindow midnight-aligns it.

**Fix:** Truncate to the day before subtracting: `lookbackFrom = from.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(windowDays-1))`. The buckets are whole UTC days (`d := ts.Truncate(24*time.Hour)...`), so the collection floor must be a whole UTC day too.

**Verifier:** CONFIRMED — reproduced the reporter's exact numbers by running the real code.

Code path (all absolute paths):
- /Users/arjun/smolanalytics/internal/trends/trends.go:690-700 — `lookbackFrom = from.AddDate(0, 0, -(windowDays - 1))` preserves `from`'s time-of-day, and the collection loop drops any event with `ts.Before(lookbackFrom)`.
- /Users/arjun/smolanalytics/internal/trends/trends.go:712-717 — but the *display* floor is midnight-truncated: `loD = from.UTC().Unix() / 86400`.
- /Users/arjun/smolanalytics/internal/trends/trends.go:731-738 — the rolling loop then unions `byDay[d-back]` for back=0..windowDays-1, i.e. it claims full coverage of day `loD-(windowDays-1)`, but that day's data was clipped mid-day by the unaligned floor.

So the data floor and the label floor disagree by exactly `from`'s time-of-day. Only events are dropped, so the error is always an undercount, never an overcount, and nothing in the response signals the clipping.

Triggering input (concrete, verified):
- 8 users, one active per day on 8 consecutive days at 02:00 UTC.
- `ComputeXAU(evs, "open", now.Add(-6h), now, 7)` with now = day7 16:00 → Total 6.
- `ComputeXAU(evs, "open", day7 00:00, now, 7)` → Total 7

## 45. [medium] any-mode (fm=any) rebuilds its base with Apply(evs, nil), which re-applies the dev/preview exclusion and makes an env filter in an OR row match nothing

**Where:** `internal/query/query.go:114` · **Area:** trends-query

**How it fails:** Two events: one env=development/source=hn, one env=production/source=google. All-mode `env eq development` correctly returns 1 (Keeper sees filtersTouchEnv and steps out of the way). Any-mode `env eq development OR source eq nope` returns 0, because `base := Apply(evs, nil)` is computed with NO filters, so filtersTouchEnv is false and every development event is stripped before the OR ever runs. Verified by running the real code: `all-mode env=development -> 1 / any-mode env=development OR source=nope -> 0`. This is directly reachable from the dashboard, which emits both parameters together (`if showDev { reportQ.Add("f", "env:eq:development") }` and `if anyMode { reportQ.Set("fm", "any") }`, dashboard.go:1671-1677) and applies them the same way at dashboard.go:1498 — turning on "show dev traffic" while in OR mode blanks the page instead of showing dev traffic.

**Fix:** Pass the real filter set into the base so the env escape hatch is honored — e.g. `base := Apply(evs, nil)` becomes a Keeper built from `filters` with the per-filter predicates skipped, or simply `base := filterByEnvOnly(evs, filters)` that computes filtersTouchEnv from `filters`. Keeper already has the logic; ApplyMode just needs to reuse it instead of calling Apply with nil.

**Verifier:** Not refuted — reproduced by running the real code. ApplyMode (internal/query/query.go:110-125) builds its OR base with Apply(evs, nil); Keeper(nil) has filtersTouchEnv=false, so every env in NonProduction is stripped before the OR rows run, defeating the explicit-env escape hatch that AND mode relies on (query.go:237-261). Measured output from a test I wrote against the real package: all-mode env=development -> 1, any-mode "env=development OR source=nope" -> 0. Two reachable paths: (1) GET /v1?fm=any&f=env:eq:development&f=source:eq:nope hits query.ApplyMode directly at internal/api/query_api.go:111, and FirstUnknownProp does not reject it since "env" exists on events, so it returns a silent real-looking 0 — this is precisely the querystring the dashboard emits for client-fetched panels (dashboard.go:1719-1723). (2) Broader than the reporter stated: on the dashboard page itself, ?env=development scopes evs at Scan time via query.Keeper(scopeFilters) (dashboard.go:1362-1390), so evs already contains only development events; any TWO chips in OR mode then reach query.ApplyMode(evs, filters, anyMode) at dashboard.go:1540 and Apply(evs, nil) re-strips them — verified: "dashboard dev-sco

## 46. [medium] gt/lt coerce non-numeric property values to 0, so `lt` matches every event whose value is a string, bool or object

**Where:** `internal/query/query.go:65` · **Area:** trends-query

**How it fails:** Four "p" events with amount = 50, "n/a", true, 500. Filter `amount lt 100`. Validate passes (it only checks the COMPARAND is numeric, not the event value), then `toNum("n/a") = 0` and `toNum(true) = 0`, and 0 < 100 is true. Verified by running the real code: `Validate err: <nil>` and `amount lt 100 -> 3` where the honest answer is 1. The same coercion makes `gt` silently exclude those events, so `lt 100` plus `gt 100` do not partition the data and the two counts don't add up to the unfiltered total. It also disagrees with the measure path in the same product: trends.numOf SKIPS non-numeric values ("never coerced to 0", trends.go:238), so avg/sum over `amount` sees 2 values while the filter over `amount` sees 4.

**Fix:** Give toNum an ok return (or reuse trends.numOf, which already has exactly the right shape) and make Gt/Lt require the EVENT value to parse: `n, ok2 := numOf(v); return ok && ok2 && n < toNum(f.Value)`. That matches the measure path and the Validate comment's own stated intent about the silent-wrong-number trap.

**Verifier:** CONFIRMED by executing the real code. Triggering input: events with a mixed-type property, e.g. JSON-decoded properties [{"amount":50},{"amount":"n/a"},{"amount":true},{"amount":500},{"amount":{"x":1}}], filtered with {Property:"amount", Op:Lt, Value:100}. Measured output: `Validate err: <nil>`, `amount lt 100 -> 4` (honest answer 1), `amount gt 100 -> 1`, total 5.

Code path reached, no guard prevents it:
- query.go:161 Validate only type-checks f.Value (the comparand) via isNumericValue; it never inspects the event-side value. Its own comment names this exact trap ("silently coerces to 0 and returns a real-looking filtered number") but guards only one side.
- query.go:328 toNum returns a bare float64 with `return 0` as the default, so "n/a", true, and map[string]any all become 0; 0 < 100 is true for Lt and false for Gt.
- No upstream type constraint: event.Event.Properties is map[string]any (internal/event/event.go:16) with no ingest-side coercion or rejection of non-scalar/non-numeric property values (no UseNumber anywhere, no sanitizer in the ingest handlers).
- Reachable from user-facing surfaces: gt/lt are in the MCP filter op enum (internal/mcp/tools.go:15) and the dashboard

## 47. [medium] "Live now" is computed 5 minutes before the selected range's END, so a historical range reports people online right now

**Where:** `internal/web/web.go:98` · **Area:** web-engagement-session

**How it fails:** Operator opens ?from=2026-01-01&to=2026-01-31. rangeAsof becomes 2026-02-01 00:00 UTC and the dashboard calls web.ComputeRange(evs, curFrom, curTo) with curTo in the past. liveCutoff = curTo - 5m, so LiveNow counts distinct visitors in the five minutes ending months ago. Measured with the real code: `historical range ending 2026-02-01 00:00:00 +0000 UTC -> LiveNow=2`. The header then renders the green live pill as "2 now" and the pane claims two people are on the site at this moment. The public share page has the same defect via web.Compute with a caller-supplied asof.

**Fix:** Anchor live to wall-clock, never to the range: compute liveCutoff from time.Now().UTC() (and require the event to be within [now-5m, now]), or drop LiveNow from Result entirely and have the dashboard/share page compute it from a separate always-now call. Suppress the live pill when the selected range does not include now.

**Verifier:** Confirmed and reproduced end to end.

Triggering input: GET /?from=2026-01-01&to=2026-01-31 with two $pageview events at 2026-01-31 23:57 and 23:58.

Code path, all unguarded:
- dashboard.go:1452-1460 parses from/to, sets rangeAsof = toT (2026-02-01, inclusive-end +1d)
- dashboard.go:1637 endT = rangeAsof
- dashboard.go:1666 curTo = rangeAsof; the clamp at :1670 (`curTo.IsZero() || curTo.After(nowT)`) only catches FUTURE ends, so a past end passes through untouched
- dashboard.go:2197 web.ComputeRange(evs, curFrom, curTo)
- web.go:97-98 `cutoff, asof := from, to; liveCutoff := asof.Add(-5*time.Minute)` — "live" is the 5 minutes ending at the RANGE end, not wall clock
- dashboard.go:2203 vm.LiveNow = wv.LiveNow, nothing resets it afterwards
- dashboard.tmpl.html:1068 renders the green .livepill "N now"

Measured: a temporary test at the engine level printed `historical range ending 2026-02-01 00:00:00 +0000 UTC -> LiveNow=2`, and a temporary handler test rendering the real s.dashboard with that querystring produced `id="livecount">2 now</span>` in the HTML. Both temp test files were deleted afterwards.

No guard exists: the only clamp is for future ends; no customRange check gates t

## 48. [medium] The fix-brief sheet silently opens a DIFFERENT finding's brief when the fingerprint misses, with no warning and not even the lead finding

**Where:** `internal/api/dashboard.tmpl.html:2135` · **Area:** insight-verdict

**How it fails:** Any fingerprint miss (which is now the normal case whenever a filter chip is set — see the verdict/brief desync above, and because titles carry live numbers so "Day-1 retention 43%" hashes differently from "Day-1 retention 44%") makes fxOpen walk SA_FIX with `for(var k in SA_FIX)` and open the FIRST key it finds. Go's json.Marshal sorts map keys, so that is the lexicographically smallest sha1 prefix — an arbitrary finding, not the lead one. The sheet then replaces #fxtitle/#fxdetail/#fxevidence with that other finding's content and offers its "copy this prompt for your agent" block. The reader clicked "Fix with your agent" on the checkout drop-off and gets a paste-ready brief telling an agent to go rewrite onboarding for retention. The inline comment claims it will "fall back to today's lead and say so" — it does neither: nothing in the DOM is set to say a substitution happened, and the chosen brief is hash-ordered, not severity-ordered. This directly violates fixbrief.go:131-133: "Never substitute a different finding for the one that was asked for. A brief handed to an agent under the wrong title is how someone ships a change for a problem they do not have."

**Fix:** On a miss, do not substitute: render the sheet in an explicit "this finding is no longer in the verdict" state listing the fingerprints that ARE available (the same shape fixbrief.Result.Note already produces server-side). If a fallback is genuinely wanted, serialise the briefs as an ORDERED array (they already arrive severity-sorted from GenerateForFunnel) instead of a map, take element 0, and set a visible banner naming the substitution.

**Verifier:** The defect is real but the reporter's frequency premise is false.

REFUTED PART — the filter-chip premise. The reporter claims a fingerprint miss is "the normal case whenever a filter chip is set." It is not. Both the verdict's `data-fix` attributes and the `SA_FIX` island are built in the same request from the same `evs` + `verdictSteps` (internal/api/dashboard.go:1425-1426 `verdict := insight.GenerateForFunnel(evs, verdictSteps)` and :1632-1634 `for _, fb := range fixbrief.ComputeAll(evs, verdictSteps, nowT) { fixBriefs[fb.Fingerprint] = fb }`), and `fixbrief.ComputeAll` calls the identical `insight.GenerateForFunnel(evs, steps)`. The verdict is server-rendered once with no client refetch (comment at dashboard.tmpl.html:3940). I verified empirically with a throwaway test rendering `/`, `/?f=plan:pro` and `/?f=plan:free` against a 60-free/40-pro fixture: every rendered `data-fix` value was present as a key in `SA_FIX` on all three pages. So the click path (dashboard.tmpl.html:2190-2191, `.co-fix[data-fix],.vfix[data-fix]`) can never miss.

CONFIRMED PART — the deep-link path. The second caller is the hash handler at dashboard.tmpl.html:2200-2204: `var m=/[#&]fix=([a-f0-9]{1,12})/.

## 49. [medium] segment.Scan hard-fails with a raw "no such file" when Prune/DeleteUser/Scrub deletes a blob the in-flight scan still holds a stale reference to

**Where:** `internal/store/segment/segment.go:234` · **Area:** store-ingest

**How it fails:** Same root cause as the Scan/seal race: Scan copies the manifest, releases the lock, then Gets each key. Prune, DeleteUser and Scrub all persist the new manifest and *then* `s.blob.Delete(k)` the now-unreferenced blobs — but an in-flight Scan is still iterating the pre-delete snapshot. Verified by probe: two segments, a GDPR DeleteUser("b") fires while the scan is on segment 0; the scan then Gets the old seg/0000000001.sms and returns `err: open .../cold/seg/0000000001.sms: no such file or directory` after emitting only 1 of 3 events. In production this means every dashboard/API/MCP query in flight during the 6-hourly retention prune (cmd/smolanalytics/main.go:470) or during a GDPR erasure returns a 500 with a filesystem path in the error body. On S3/R2 the same window exists and, being remote, is wider. Note the partial-result shape too: fn has already been invoked for the events from earlier segments, so any caller that accumulates into a shared structure before checking err sees a truncated set.

**Fix:** Two options. (a) Never delete inline: mark dropped keys as garbage and let Scrub remove them after a grace period longer than any query (segments are immutable and keys are never reused thanks to the monotonic seq, so a delayed delete is safe). (b) Treat an os.ErrNotExist from blob.Get on a snapshotted key as "this segment was superseded mid-scan" and restart the scan against a fresh manifest snapshot rather than propagating the error. Either way, do not surface the raw filesystem/S3 path to API callers.

**Verifier:** CONFIRMED — no guard exists, and I reproduced both variants deterministically.

Code path (/Users/arjun/smolanalytics/internal/store/segment/segment.go):
- `Scan` (line 224) copies the manifest under `s.mu.RLock`, calls `s.mu.RUnlock()` at line 228, and only then does `s.blob.Get(m.Key)` at line 234, returning the raw error on failure. The snapshot it iterates can go stale the instant the RLock is dropped.
- `Prune` (line 294) persists the shrunken manifest at line 313 and then `_ = s.blob.Delete(k)` at line 321; `DeleteUser` (line 384) does the same at line 455 after rewriting affected segments under fresh keys. Both hold `s.mu.Lock`, but the write lock is irrelevant because Scan holds nothing while it Gets.
- `blob.Local.Get` (internal/store/blob/blob.go:79) is a bare `os.ReadFile`, so a removed key yields ENOENT with the full filesystem path; `blob.S3` returns `os.ErrNotExist` (s3.go:85). Nothing in Scan, in the `alias`/`defined`/`demo` decorator chain, or in any caller treats not-exist as "skip this segment".

Concrete triggering input (probe, since deleted, used a Blob wrapper that fires the mutation on the first `Get` of `seg/0000000000.sms` — a deterministic stand-in for the

## 50. [medium] /v1/who applies the funnel filter at event level while /v1/funnel applies it at user level — the drill-down list contradicts the bar it explains

**Where:** `internal/api/query_api.go:727` · **Area:** api-surfaces

**How it fails:** Filter a funnel by a property that only the first step carries (plan, cohort trait, any non-acquisition attribute). Verified: 4 users, signup carries plan, checkout does not. `GET /v1/funnel?steps=signup,checkout&f=plan:eq:pro` → step "checkout" count 2. `GET /v1/who?steps=signup,checkout&step=1&state=reached&f=plan:eq:pro` → `{"mode":"funnel","total_users":0,"users":[]}`. The chart says 2 people converted; the microscope that claims to use "the exact engine that computed the aggregate" lists nobody. funnelScoped() exists precisely to avoid this ("would otherwise drop the later steps and report a broken funnel"), and apiWho never calls it.

**Fix:** In the funnel-step branch of apiWho, load events with s.funnelScoped(r) instead of s.filtered(r) (the other two modes can keep s.filtered). Add a contract test asserting len(who.users) == funnel step count for a filter carried by only one step.

**Verifier:** CONFIRMED by execution, not inspection. Triggering input: ingest 4 users where `signup` carries `plan` and `checkout` does not, then `GET /v1/funnel?steps=signup,checkout&f=plan:eq:pro` returns checkout count 2 / converted 2, while `GET /v1/who?steps=signup,checkout&step=1&state=reached&f=plan:eq:pro` returns `{"mode":"funnel","total_users":0,"users":[]}` (same for state=converted). Reproduced via httptest against s.Handler() in internal/api/zz_repro_who_test.go.

Code path: apiWho (internal/api/query_api.go:727) calls s.filtered(r) for all three modes, which does query.ApplyMode(query.StampForFilters(all, fs), fs, anyMode) — event-level. StampForFilters (internal/query/query.go:376-395) only first-touch-stamps acquisition props (referrer/source/utm_*/device/os/browser/country/platform); `plan` is intentionally left event-level, so every checkout event is dropped and funnel.Users at line 790 sees Reached=1/Converted=false for everyone. apiFunnel (internal/api/api.go:882) instead calls s.funnelScoped(r) → query.ScopeUsers (user-level). MCP (internal/mcp/mcp.go:373,386) and the dashboard (internal/api/dashboard.go:1524) also use ScopeUsers for funnels, so /v1/who is the lone divergen

## 51. [medium] Dashboard's "last N days" window is only calendar-day-aligned when gran==day, so ?gran=week changes the headline number and breaks agreement with /v1/trends?days=N

**Where:** `internal/api/dashboard.go:1638` · **Area:** api-surfaces

**How it fails:** Two signups: one at now-1h, one 2 hours before midnight of (today-29d) — inside a rolling now-30d window, outside the day-aligned one. Verified by rendering the dashboard: `/?days=30&metric=signup` headline = 1 (agrees with `/v1/trends?event=signup&days=30` → total 1), `/?days=30&metric=signup&gran=week` headline = 2. The toolbar says "30d" in both cases. Changing a purely visual granularity selector silently changes the measured window (curFrom moves from midnight-29d to now-30d), so the number the operator quotes disagrees with what the same question returns over /v1 and MCP.

**Fix:** Compute the window from rangeDays/rangeHours/rangeAsof alone and drop `gran == trends.Day` from the condition — the bucket size must never change the window. (Same applies to the priorFrom/priorTo ghost series.)

**Verifier:** Reproduced with the real handler. Seeded two signup events (now-1h, and midnight(today-29d) minus 2h) into a memory store and rendered s.dashboard: `/?days=30&metric=signup` headline KPI = 1, `&gran=week` = 2, `&gran=month` = 2, while parseTrendWindow for `/v1/trends?event=signup&days=30` yields from=2026-07-06 00:00 UTC (total 1). Code path: dashboard.go:1683 sets curFrom = endT.AddDate(0,0,-rangeDays) (rolling), and the calendar-day realignment at dashboard.go:1687 is gated on `gran == trends.Day`, so week/month keep the rolling start which is up to 24h earlier than parseTrendWindow's today.AddDate(0,0,-(n-1)) (query_api.go:396-398). trends.ComputeInterval filters strictly on [from,to) before bucketing (trends.go:593-598), so Total itself changes, and sig30 := tr.Total feeds the KPI card labelled "<event> · 30d" (dashboard.go:1043-1046, 1744). No guard: the grain selector is user-reachable in the template (dashboard.tmpl.html:1189-1193, JS at :2999) and titled "bucket size" — purely visual. No test covers it: contracts_test.go:53-73/:118 exercise trends.ComputeInterval directly with a fixed window, never the handler's window selection; there are no `gran` references in internal/a

## 52. [medium] Dashboard microscope descriptors omit the page's filters/env/site scope and collapse hourly bars to a whole day

**Where:** `internal/api/dashboard.tmpl.html:1209` · **Area:** api-surfaces

**How it fails:** Two independent misses on the same click path. (1) The chart-bar descriptor is only `event=X&date=YYYY-MM-DD` and the funnel-step descriptor is only `steps=…&step=N&state=reached` — neither carries SA_SCOPE, so on a dashboard filtered by chips, ?site=, or ?env=development, /v1/who re-answers over the production, unfiltered population while the panel prints "N people … computed by /v1/who, same engine as the chart". (2) ISO is day-resolution even when the chart is drawn in HOUR buckets (the 6h/12h presets and any 1-day view force gran=Hour at dashboard.go:1610), so clicking the bar tooltipped "Mon 3pm · 4" returns everyone from that whole UTC day. Every other client-fetched panel on the page correctly uses agURL()/SA_SCOPE (dashboard.tmpl.html:3200).

**Fix:** Build both descriptors as SA_SCOPE + the point descriptor (like agURL does), and emit an hour-precision key (RFC3339 bucket start, e.g. a `bucket=` param apiWho honours alongside `date=`) whenever gran==Hour.

**Verifier:** CONFIRMED — I could not find any guard, and I reproduced both halves end-to-end against the real handlers.

Code path:
- `/Users/arjun/smolanalytics/internal/api/dashboard.tmpl.html:1212` renders each bar as `data-who="event={{$.ChartMetric}}&date={{.ISO}}"` — no window, no `f=` chips, no site, no env.
- `:3099-3100` hands that raw string to `openWho`, and `openWho` (`:3084`) does `fetch('/v1/who?'+query)` — it is the ONLY client fetch on the page that does not go through `agURL()`/`SA_SCOPE` (`:3244-3250`); every other panel (agent conversations/tools/errors/labels, heatmap) does.
- `/Users/arjun/smolanalytics/internal/api/query_api.go:725 apiWho` -> `s.filtered(r)` -> `filterSetFrom(r)` reads only `?f=`/`?filters=`/`?fm`, and `parseTrendWindow(r)` with no from/to/days/hours returns an unbounded window. `query.Keeper` (internal/query/query.go:237-253) then applies the DEFAULT "hide non-production" rule because no env filter was passed. Site/env/chips are property filters on the dashboard side (`dashboard.go:1414`, `:1361-1365`, `reportQ` at `:1717-1727`) and simply never reach `/v1/who`.
- `dashboard.go:1660` forces `gran = trends.Hour` whenever `?hours=6|12` or `days=1` and no ex

## 53. [medium] days/from/to on /v1/web, /v1/retention, /v1/lifecycle, /v1/sessions, /v1/paths, /v1/groups silently fall back to a default window instead of erroring

**Where:** `internal/api/query_api.go:683` · **Area:** api-surfaces

**How it fails:** These handlers parse their own numbers with `strconv.Atoi(...); err == nil && v > 0`, so a malformed, zero, negative or fractional value is discarded without a word, and /v1/web, /v1/retention, /v1/lifecycle and /v1/stickiness never read from/to/hours at all. Verified: `/v1/web?days=abc` → 200 `{"period_days":30,…}`; `/v1/web?days=-5` → 200 period_days 30; `/v1/web?from=2020-01-01&to=2020-02-01` → 200 period_days 30 (a 2020 question answered with the last 30 days); `/v1/retention?days=abc` → 200 (7-day grid); `/v1/lifecycle?days=abc` → 30 days; `/v1/sessions?days=abc` → 7 days; `/v1/paths?depth=abc` → depth 3. Meanwhile `/v1/trends?days=abc` correctly returns 400 "days must be a positive integer" — so the same typo is honest on one report and a wrong window on six others, with a 200 and no provenance to reveal it.

**Fix:** Route every windowed report through one parser: return 400 when a supplied days/limit/depth is unparseable or <= 0, and either honour from/to/hours on these endpoints or 400 with "this report takes days= only" instead of quietly answering over a different range.

**Verifier:** CONFIRMED by direct execution. I built a throwaway test in internal/api (New(memory.New()).Handler(), memory store seeded with $pageview/signup events) and issued each request. Observed: /v1/web?days=abc -> 200 {"period_days":30,...}; days=-5, days=0, days=1.5 -> same; /v1/web?from=2020-01-01&to=2020-02-01 -> 200 period_days:30; /v1/web?hours=6 -> 200 period_days:30; /v1/retention?event=signup&days=abc -> 200 max_days:7; /v1/lifecycle?days=abc -> 200 with a 30-day array; /v1/sessions?days=abc and ?limit=abc -> 200 with defaults 7/100; /v1/paths?start=signup&depth=abc -> 200 with 3 levels; /v1/groups?property=company&limit=abc -> 200 default 50; /v1/stickiness?days=abc -> 200. Meanwhile /v1/trends?event=signup&days=abc -> 400 {"error":"days must be a positive integer"}.

No guard exists anywhere on the path. Routes are registered bare on http.ServeMux at internal/api/api.go:296-307 with no param-validating middleware. s.filtered(r) (query_api.go:92) validates only filters/cohort (FirstUnknownProp) and never touches window params. The strconv.Atoi(...); err == nil && v > 0 idiom IS the entire validation, so both err != nil and v <= 0 fall through to the default silently. No existing 

## 54. [medium] "worst day" / "slowest day" is routed to the peak-day report, which only computes the maximum

**Where:** `internal/api/ask.go:347` · **Area:** ask-nl

**How it fails:** "what was our worst day last month" → "Biggest day for visitors last month (July 2026, UTC): Fri Jul 31 with 2." (verified). The classifier explicitly captures "worst day"/"slowest day" but answerPeakDay has no minimum branch, so the user asking for the trough is handed the peak — the exact opposite fact.

**Fix:** Pass the direction into answerPeakDay (min when the question says worst/slowest/quietest) and word the answer accordingly, or drop those phrasings from the intent so they don't get an inverted answer.

**Verifier:** Reproduced by running the real code path. With a fixture of 5 visitors on Jul 5, 1 on Jul 10, 3 on Jul 20 and now=2026-08-04, answer("what was our worst day last month", evs, now) returns "Biggest day for visitors last month (July 2026, UTC): Sun Jul 5 with 5." The correct trough answer is Fri Jul 10 with 1. Same for "what was our slowest day last month" and "which day last month had the fewest visitors".

Path: classifyAsk (internal/api/ask.go:354) puts "worst day"/"slowest day"/"which day"/"what day" in the SAME hasAny as "best day"/"peak day" and returns intentPeakDay; answer (ask.go:734) calls answerPeakDay (internal/api/ask_scope.go:1063-1082), which has a single max loop (`if n > bestN || (n == bestN && d > best)`) and a single format string ("Biggest day for %s%s: %s with %d."). answerPeakDay is not even passed `q`, so it cannot see the polarity word.

No guard prevents it: isAction, the intentBrief verdict case, and isMeasureAsk all miss the phrasing (verified: intent printed as "peakday"); unsupportedTimePhrase's comment at ask.go:1198 mentions "best/worst day" but the function only ever returns "since" there; the segment-scoped branch (ask.go:620) re-enters the same answe

## 55. [medium] mcpWindow accepts a fractional days<1 and builds an INVERTED window (from = tomorrow, to = now), so breakdown/paths/heatmap/funnel/agent_* return a confident empty report

**Where:** `internal/mcp/mcp.go:1328` · **Area:** mcp-tools

**How it fails:** A model asked for "the last 12 hours" calls breakdown(event="signup", property="source", days=0.5) — a natural reading of the schema's "Rolling window in days ending now". int(0.5)==0, so from = now.Truncate(24h).AddDate(0,0,+1) = TOMORROW 00:00 UTC while to = now. I ran the arithmetic: days=0.5 -> from=2026-08-05T00:00:00Z, to=2026-08-04T09:58:29Z, from.After(to)==true. scopeWindow then drops every event, `len(filtered) > 0` is false so the unknown-property guard never fires, and the tool returns {"groups":[]} as a real answer. funnel(days=0.5) returns every step with count 0 — the model reports "0 signups in the last 12 hours". trends rejects the identical input with "days must be a positive integer" (mcp.go:456), so the two tools disagree about whether the question is even askable.

**Fix:** Move the trends validation into mcpWindow: reject `days != math.Trunc(days)` with the same "days must be a positive integer — use hours for sub-day windows" message, and defensively refuse any resolved window where !from.Before(to).

**Verifier:** CONFIRMED by execution, not just reading. mcpWindow (internal/mcp/mcp.go:1317) rejects only NaN/Inf/negative days; for 0<days<1, int(days)==0 makes the offset -(0-1)=+1, so from = tomorrow 00:00 UTC while to = now — an inverted window. No caller re-validates: funnel (:345), breakdown (:591), paths (:820), heatmap (:853), agent_* (:933-1042) all pass a.Days float64 straight through, and unmarshalArgs (internal/mcp/filters.go:95) is a bare json.Unmarshal with no JSON-Schema enforcement while tools.go:131 declares days as "type":"number", making 0.5 schema-legal. I ran a temp test in internal/mcp with two signup events at now-1h and now-3h: breakdown(event=signup, property=source, days=0.5) returned isErr=false {"groups":[]} while the equivalent hours=12 returned both groups; funnel(steps=[signup,checkout], days=0.5) returned isErr=false with every step count 0 and overall_conversion 0 while hours=12 returned 1 converter at 100%; trends(days=0.5) returned isErr=true "days must be a positive integer" — proving the cross-tool disagreement the reporter described, and violating the "One definition, so no tool can disagree with another on the window" contract in the comment at :1312. The u

## 56. [medium] label_conversation writes the conversation_id as the event's distinct_id, so every label invents a new USER in overview, lifecycle, retention and stickiness

**Where:** `internal/mcp/agent_labels.go:89` · **Area:** mcp-tools

**How it fails:** The model follows the documented loop — sample_conversations, then label_conversation once per conversation. Each call appends an agent_label event whose DistinctID is the conversation id (e.g. "c17"), which matches no real user (agent_turn events carry the user's id and put the conversation id in a property). Label 22 conversations and overview's total_users jumps by 22, active_users_7d by 22, events_7d by 22; engagement.ComputeLifecycle counts 22 brand-new users on the day of labelling, retention gets 22 single-event cohort members that never return, and stickiness's DAU/MAU shifts. The user reads "22 new users" that are the agent's own bookkeeping. The demo seeder does NOT do this — it writes agent_label with the agent USER's id (internal/demo/demo.go:510 emit("agent_label", fmt.Sprintf("au%d", c), ...) while the conversation id is "c%d") — so the demo and the live loop produce differently-shaped data for the same feature.

**Fix:** Set DistinctID to the labelling identity rather than the conversation (e.g. "$agent_label" or the conversation's own distinct_id looked up from its agent_turn events) — the join already happens on the conversation_id property (agent/labels.go:246), so nothing in agent_labels depends on DistinctID. Add a test asserting overview.total_users is unchanged after a label round-trip.

**Verifier:** Reproduced end-to-end. Seeded two agent_turn events with DistinctID "u1" and property conversation_id "c1", called the MCP tool label_conversation {"conversation_id":"c1","labels":{"intent":"billing"},"labeled_by":"claude-sonnet-4-5"}. overview went from {"total_users":1,"active_users_7d":1,"events_7d":2} to {"total_users":2,"active_users_7d":2,"events_7d":3}; the stored event was distinct_id="c1"; engagement.ComputeLifecycle reported new=2 for the label day instead of new=1.

No guard exists. internal/mcp/agent_labels.go:86-92 hard-codes DistinctID: cid, and the tool schema (internal/mcp/tools.go) plus the dispatch in internal/mcp/mcp.go:1019 expose no distinct_id argument, so the caller cannot pass the real user. hasConversation (agent_labels.go:140) only checks that some agent_turn carries that conversation_id in Properties; it never reads that turn's DistinctID, so it neither validates nor recovers the owning user. The only "our own writes" exclusion list is query.SamplerEvent (internal/query/sampler.go:25) = $geo_check, $site_readable, $ai_crawl; agent_label is absent, so WithoutSampler (retention.go:65, engagement.go:28/91, paths.go:38, ask.go:51) passes it through, and disti

## 57. [medium] overview — the tool the instructions say to call FIRST — counts users and events over unscoped data, so its headline disagrees with the dashboard

**Where:** `internal/mcp/mcp.go:1146` · **Area:** mcp-tools

**How it fails:** toolOverview receives the raw slice from s.all() and never applies the production scope, so total_users / active_users_7d / events_7d / total_events include env=development, preview, staging, test and ci traffic that query.Apply hides everywhere else. On a dev machine where the SDK auto-stamps localhost as development, the orient tool reports (say) 340 users while web_overview and trends(unique=true) report 190 and the dashboard shows 190. The agent leads its answer with the wrong number, and the WoW read line built from it ("340 users · 61 active in the last 7d") is wrong too.

**Fix:** return s.toolOverview(applyDefaultScope(evs)) — and state the scope in the tool description ("production traffic; dev/preview excluded") so the model can explain the difference if asked.

**Verifier:** Real and reproduced. internal/query/query.go:192-250 (Keeper/Apply) defines the single default scope: events with env in NonProduction (development, preview, staging, test, ci) are excluded unless filters explicitly reference "env". Every other MCP tool applies it via query.Apply(query.StampForFilters(evs, a.Filters), a.Filters) (mcp.go:427,516,596,646,695,709,734,752,774,824,857,874,909,937,960,982,1007,1046), and the dashboard applies the same rule (internal/api/dashboard.go:1378). toolOverview (mcp.go:1143) receives the raw slice from s.all() (mcp.go:249,304,310 -> store.Range with zero bounds) and iterates it directly for total_users / active_users_7d / events_7d / total_events / headline_* / read.

Concrete trigger, executed via the existing scratch probe internal/mcp/zzz_probe_test.go (untracked, log-only, no assertions): ingest signup events for p1,p2 with env=production, d1 with env=development, s1 with env=preview. Result: overview returns {"total_users":4,"active_users_7d":4,"events_7d":4,"read":"4 users · 4 active in the last 7d (new WoW) · signup 4 (new)"} while trends(event=signup,days=1,unique=true) returns total 2 and breakdown(property=env) returns only the producti

## 58. [medium] data-sa-ignore does not suppress clicks on clickable children — the documented subtree opt-out is bypassed by exactly the elements it is meant to protect

**Where:** `internal/api/sdk.js:441` · **Area:** sdk-js

**How it fails:** The walk-up loop checks saIgnore and clickability on the SAME node and `break`s the moment it finds a clickable one, so an ancestor's opt-out is never reached. Given `<div data-sa-ignore><button>Reveal SSN</button></div>`, the first iteration is the <button>: no saIgnore on it, isClickable true → target = button, break. The ancestor div is never examined. Result: the SDK captures $click with the button's innerText, id, classes and the full $elements chain — including elemDesc of the data-sa-ignore container and its innerText — from a region the site owner explicitly marked private. The opt-out only works for clicks on non-interactive filler inside the subtree, i.e. the clicks nobody cared about.

**Fix:** Separate the two walks: first walk the full ancestor chain (uncapped, or use `e.target.closest('[data-sa-ignore]')`) and return if anything matches; only then walk up to find the clickable target. Same fix for $form_submit (sdk.js:476), which only checks saIgnore on the form itself, not its ancestors.

**Verifier:** Confirmed by tracing the click handler at /Users/arjun/smolanalytics/internal/api/sdk.js:437-467. Triggering input: `<div data-sa-ignore><button id=\"ssn\">Reveal SSN</button></div>`, click the button. Iteration 1 of the walk-up loop has node = e.target = the button: `button.dataset.saIgnore` is undefined so line 441 does not return; `isClickable(button)` returns true at line 320 (tag === \"button\") so line 442 sets target and breaks. The ancestor div carrying data-sa-ignore is never examined. Execution proceeds to enqueue at line 465 with text (line 452, button innerText), id, classes, and `$elements: elemChain(target)` — elemChain (line 306-314) walks 4 ancestors unconditionally, and elemDesc (line 269-302) emits each ancestor's innerText (line 289-290) and its data-* attributes (line 282-288), so the ignored container's own text ships too. armDeadClick(target) also arms follow-up capture.\n\nNo guard prevents it: repo-wide grep for saIgnore/sa-ignore yields exactly two hits — line 441 and the form-submit check at line 476, which likewise only tests the <form> element itself. No closest('[data-sa-ignore]') check, no filtering in enqueue. No test covers it: internal/api/sdktest/e

## 59. [medium] elemDesc exfiltrates every data-* attribute of the clicked element and 4 ancestors, despite being documented as opt-in

**Where:** `internal/api/sdk.js:283` · **Area:** sdk-js

**How it fails:** The loop copies the entire dataset of each element in the chain, not just the data-sa-* attributes the comment describes as the opt-in mechanism. Real apps routinely carry identifiers there: `<tr data-user-email="jane@corp.com" data-account-id="...">`, `<div data-stripe-customer="cus_...">`, `<button data-invoice-total="...">`. elemChain runs elemDesc over the target plus up to 4 ancestors, so one click on a row action ships the whole row's PII into $click.$elements[*].data, where it is stored raw and surfaced by recent_events / session_timeline. The site owner has no way to know this is happening and no way to opt a single attribute out (data-sa-ignore is subtree-level and, per the separate finding, does not even work over clickable children).

**Fix:** Make it actually opt-in: capture only keys the SDK owns or the operator whitelists — `if (dk.indexOf("sa") === 0 || dk === "testid" || allowedData[dk])` — and expose an init option ({ dataAttributes: [...] }) for anything else.

**Verifier:** CONFIRMED. The loop at internal/api/sdk.js:283 (`for (var dk in el.dataset)`) copies every data-* attribute, not just data-sa-*, and elemChain (sdk.js:306-314) applies elemDesc to the click target plus up to 4 ancestors.

Concrete trigger: `<tr data-user-email="jane@corp.com" data-stripe-customer="cus_ABC"><td><button>Delete</button></td></tr>`. Clicking the button: the listener at sdk.js:439-445 walks from e.target, hits isClickable(button)=true on the first iteration and BREAKS — so it never evaluates the <tr>. Then elemChain(button) at sdk.js:460 runs elemDesc over button/td/tr/tbody/table, and the <tr>'s full dataset lands in $click.$elements[2].data (values truncated to 80 chars).

No guard prevents it: (1) elemDesc has no prefix allowlist — the only filter is `if (dv)`, which just skips empty strings; (2) the data-sa-ignore subtree opt-out (sdk.js:441) is only checked on nodes walked before the first clickable node, so putting it on the <tr> (or on the button when e.target is an inner span) never fires — the opt-out is unusable for exactly this case; (3) the only working control is the global `opts.autocapture !== false` kill switch (sdk.js:641); (4) server side does nothing 

## 60. [medium] Country/AI-referrer percentages use a denominator that is already truncated to the top N rows, so shares are inflated and always sum to 100%

**Where:** `internal/api/dashboard.go:2274` · **Area:** dashboard-render

**How it fails:** A site with 40 countries: web.rank(countries, 10) returns only the top 10 rows, so sumRows(wv.Countries) is the visitor count of those 10 — not of all visitors with a country. With US=100, GB=30, the next 8 = 28 and a tail of 30 countries totalling 190, the pane prints "US 100 · 63%" when the US is really 100/348 = 29% of geolocated visitors, and the ten visible rows sum to exactly 100%, asserting there is no tail. The comment directly above claims this denominator makes "an honest share-of-recorded that adds up". Same shape for vm.AIRefs (rank cap 6, 10 possible AI hosts).

**Fix:** Have web.ComputeRange report the untruncated per-dimension totals (e.g. Result.CountriesTotal, or Row list plus a `recorded` count) and divide by that; or render an explicit "other" row for the residual so the visible rows are allowed not to sum to 100.

**Verifier:** CONFIRMED — reproduced end-to-end through the real dashboard handler.

Code path (all verified by reading, no guard anywhere):
1. `/Users/arjun/smolanalytics/internal/web/web.go:240` — `Countries: rank(countries, 10)`.
2. `/Users/arjun/smolanalytics/internal/web/web.go:317-331` — `rank` sorts desc then hard-truncates: `if len(out) > limit { out = out[:limit] }`. The tail is discarded and nothing carries the pre-truncation total (`Result` has no such field).
3. `/Users/arjun/smolanalytics/internal/api/dashboard.go:2285` — `vm.Countries = toRows(wv.Countries, 10, sumRows(wv.Countries))`. `sumRows` (line 2244) sums the already-truncated slice, so the denominator is the top-10 subtotal, not the geolocated-visitor total. Because the rank cap (10) equals the display cap (10), `toRows`' own `if len(rows) > n` never fires and every row that contributed to the denominator is also printed — so the shares necessarily sum to ~100%.
4. `/Users/arjun/smolanalytics/internal/api/dashboard.tmpl.html:1386` renders `{{.Count}} · {{.Pct}}%`.

Concrete triggering input (I wrote it as a temp test, ran it, then deleted it): 40 distinct `$pageview` visitors-by-country — US=100, GB=30, eight countries at 5

## 61. [medium] An empty window makes the page claim the instance has never received a $pageview, and silently drops the Visitors KPI

**Where:** `internal/api/dashboard.go:2198` · **Area:** dashboard-render

**How it fails:** Pick the 6h preset at night, or a custom range over a quiet week, on a site with months of pageview history. wv.Pageviews == 0, so HasWeb is never set, and the template's {{else}} arm renders "this instance has events but no <b>$pageview</b> yet … drop <script src=…/sdk.js> into your app" — a false statement telling the operator to reinstall an SDK that is working. buildKPIs (gated on vm.HasWeb) also omits the Visitors tile entirely, so the KPI row silently loses a card when the window is quiet. The AI-visibility and AI-crawl panes solve exactly this with an `Ever` flag read from store Names(); the web block has no equivalent even though `names` is already in scope.

**Fix:** Split "never" from "none in this window": set HasWeb (and the Visitors tile) whenever hasName(names, "$pageview"), and give the pane a windowed empty state ("no pageviews in this window — widen the range") distinct from the never-installed copy, mirroring aivisVM.Ever / aicrawlVM.Ever.

**Verifier:** Reproduced against the real handler. dashboard.go:1375-1390 loads `evs` over ALL history (store.Scan with zero bounds), and line 2204 `web.ComputeRange(evs, curFrom, curTo)` narrows to the selected window; `vm.HasWeb = true` (2209) sits inside `if wv.Pageviews > 0`, so HasWeb is a WINDOW fact used everywhere as a LIFETIME fact. Concrete trigger: 300 $pageview events from 30h ago back ~13 days plus one recent non-pageview `signup` 1h ago, then GET /?hours=6 (or /?days=1). Test output: both render `this instance has events but no <b>$pageview</b> yet` (dashboard.tmpl.html:1378, which also tells the operator to drop in sdk.js) and the `Visitors ·` KPI card is missing; the control request GET / (30d) renders neither problem and does show the Visitors tile. buildKPIs runs at 2320, after HasWeb is set at 2209, and gates the Visitors card on vm.HasWeb at 1035 — so the ordering does not save it. dashboard.tmpl.html:1702 has the identical false claim for the AI-referrer half. No guard, validation, or type constraint prevents this, and no test covers it (dashboard_inventory_test.go only asserts panes exist in the template source, not which arm renders). The reporter's suggested fix material 

## 62. [medium] The chart's data table compares the in-progress newest bucket against a full prior bucket with no partial marker

**Where:** `internal/api/dashboard.go:2054` · **Area:** dashboard-render

**How it fails:** Load the dashboard at 09:00 UTC with daily grain. The chart's newest bar carries Partial and the tip "today so far", but chartRow has no such field, so the table's first row reads "Aug 4 · 14 · prior 96 · -85%" in warning red — a crash that is purely the clock. The trendBar.Partial doc comment says the flag exists precisely because otherwise "the change column always reads like a crash — an artefact of the clock, not the product", yet the change column is the one place the flag never reached. This is the only reading available to a screen reader or a phone, since the per-bar numbers live in a display:none .tip.

**Fix:** Add Partial to chartRow, set it for i == len(tr.Points)-1 when rangeAsof.IsZero() (the same condition the bar uses), and either suppress the delta or render it with a "so far" qualifier for that row.

**Verifier:** CONFIRMED — reproduced end-to-end against the real handler, not by reading.

Triggering input: any preset range (`rangeAsof` zero) on an instance with prior-window traffic, loaded mid-day. Concrete repro I ran in /Users/arjun/smolanalytics/internal/api (memory store + `s.dashboard` via httptest): 96 pageviews/day for the 28 prior days, plus today's elapsed portion at the same 96/day rate, request `GET /?days=7`. At 11:32 UTC (46 of today's 96 events elapsed) the same render produced:

- chart bar (dashboard.tmpl.html:1212): `<div class="col part" ...><span class="tip">Aug 4 · 46 (prior window Jul 28: 96) · today so far</span>` — the hatch class `.bars .col.part .bar` (line 395) plus the "today so far" clause.
- chart table (dashboard.tmpl.html:1227): `<td>Aug 4</td><td class="n">46</td><td class="n mut">96</td><td class="n down">-52%</td>` — `.down` is `color:var(--warn)` (line 339). No partial marker anywhere in the row, the header, or the `<summary>`.

Code path reaching the defect: internal/api/dashboard.go:2011 sets `b.Partial = true` on the last trendBar when `rangeAsof.IsZero()`, but the table loop at 2049-2064 builds `chartRow` (type at 433-439: Label/Count/Prior/Delta/Bar —

## 63. [medium] KPI and engagement sparklines are drawn over a different period than the number above them under custom ranges and 90d

**Where:** `internal/api/dashboard.go:1040` · **Area:** dashboard-render

**How it fails:** Open ?from=2026-01-01&to=2026-01-07. The tiles report the custom window correctly (web.ComputeRange(curFrom,curTo)), but dailySeries/cumulativeUserSeries/engagementSeries are all handed `now`/`nowT` and clamp days to [2,30], so every sparkline is drawn over the last 7 days ending TODAY (August) — for a January window that is a flat zero line sitting under "Visitors · 7d 412", with a good/warn end-dot coloured from the January delta. With ?days=90 the tiles say 90d and the sparkline covers only the trailing 30 days.

**Fix:** Pass the page's window end (endT/rangeAsof, not nowT) into buildKPIs/dailySeries/cumulativeUserSeries/engagementSeries, and when the range exceeds the 30-point cap, bucket the series to the window instead of truncating it to the trailing 30 days.

**Verifier:** CONFIRMED (with two corrections to the report).

Path traced in /Users/arjun/smolanalytics/internal/api/dashboard.go:
- Custom range parsing (l.1441-1452): `?from=2026-01-01&to=2026-01-07` → customRange=true, rangeDays=7, rangeAsof=2026-01-08.
- Tiles: `endT = rangeAsof` (l.1643-1646), `curFrom, curTo := endT.AddDate(0,0,-rangeDays), rangeAsof` (l.1673); `wv := web.ComputeRange(evs, curFrom, curTo)` (l.2204) → vm.Visitors / vm.VisitorsDelta are the January window. Correct.
- Sparks: `buildKPIs(&vm, evs, trendEvent, rangeDays, rangeHours, nowT)` at l.2320 passes `nowT` (time.Now().UTC(), l.1633), never curTo/rangeAsof. Inside buildKPIs, l.1040 and l.1046 call `dailySeries(..., days, now, ...)`, which does `today := now.Truncate(24*time.Hour); from := today.AddDate(0,0,-(days-1))` (l.875-876). So the "Visitors · 7d" and "<stat event> · 7d" sparklines are drawn over Jul 29–Aug 4 while the numbers and the good/warn end-dot come from Jan 1–Jan 8. No guard, no clamp, no earlier validation resets `now` for the custom case — I grepped every use of nowT/rangeAsof and nowT is never reassigned between l.1633 and l.2320.

Executed proof (temporary test, since removed): with two January pagevie

## 64. [low] All-time trend Total silently drops every real bucket when one event is far-dated (span cap moves `lo`, and Total is summed only over emitted buckets)

**Where:** `internal/trends/trends.go:99` · **Area:** trends-query

**How it fails:** An instance has 30 real "view" events over the last month plus ONE event whose ms-epoch was read as seconds / imported with a bad clock, dated 2200-01-01. `GET /v1/trends?event=view` (no window params = all recorded history) and the MCP `trends` tool both call Compute with from/to zero. hi = day(2200-01-01), lo = day(1970 or the earliest real event); hi-lo > 4200, so lo := hi-4200 = 2188-07-02. Every real 2026 day is now BELOW lo and is never emitted, so it is never added to r.Total. Verified by running the real code: `all-time total: 1 of 31 matching events; points: 4201; first bucket: 2188-07-02 last: 2200-01-01`. The endpoint reports total=1 for 31 events, with no error and no warning. A milder version fires with a 1970-dated event: total=2 of 3.

**Fix:** Compute r.Total from perDay over the FULL matched set (a separate accumulation loop before the bucket-emit loop), so the display cap only trims which buckets are rendered and never changes the answer. Better still, clamp the span against `to`/now rather than against the max observed event day, so a single future-dated event cannot define the window at all.

**Verifier:** The reported CRITICAL scenario is refuted; a much milder defect at the same line is real.

Refuted part: ingest normalization (internal/api/api.go:748-823) clamps EVERY future timestamp to now (maxFuture = now, zero skew tolerance) and every pre-2000 timestamp to now (minPast floor), so no stored event can be future-dated or 1970-dated. I ran the reporter's exact scenario through the real HTTP handlers (POST /v1/events with 30 real "view" events + one 2200-01-01 event, then GET /v1/trends?event=view with no window params): result was total=31, points=31, first=2026-07-05, last=2026-08-04 — not total=1 of 31. Their stated milder 1970 variant also returns total=3 of 3. The reporter's "verified by running the real code" was a direct trends.Compute call on a fabricated event slice, bypassing the normalization no stored event can bypass (the CLI importer posts to /v1/events; internal/mcp/import_tool.go:101 clamps future to now+1h). Since hi can never exceed today, the span cap can only ever move lo backward-of-real-data, never past it. internal/api/r5_regressions_test.go:41 (TestIngestClampsAncientTimestamp) pins this deliberately.

Real residual: the mechanism (Total summed only over e

## 65. [low] A JSON-null property value produces two different breakdown keys — "" in query.Breakdown, "<nil>" in trends.ComputeBreakdown — and the drill-down filter matches neither

**Where:** `internal/trends/trends.go:183` · **Area:** trends-query

**How it fails:** An SDK sends `track("signup", {plan: null})` (nothing strips nulls on ingest — event.Properties is a plain map[string]any). The breakdown card (`GET /v1/breakdown?property=plan` → query.Breakdown → toStr) labels the group "", while the trend breakdown (`GET /v1/trends?breakdown=plan` → trends.ComputeBreakdown → valueOf) labels the same group "<nil>". Verified by running the real code: `query.Breakdown key=""` vs `trends.ComputeBreakdown key="<nil>"`. Neither is "(none)" (the key exists), and clicking through from the trend legend produces `f=plan:eq:<nil>`, which f.match evaluates as toStr(nil)=="" vs "<nil>" → 0 events: a real segment that renders as an empty report.

**Fix:** Delete valueOf and have trends.ComputeBreakdown / ComputeMeasureBreakdown call query's stringifier (or export one shared Stringify), and treat a nil value as absent so it falls into "(none)" like every other missing property — one definition, one label, and the drill-down filter round-trips.

**Verifier:** CONFIRMED (with one fabricated detail in the report's UI story).

Reproduced end-to-end against a real `smolanalytics serve` instance on a fresh event log:

  POST /v1/events  [{"name":"signup","distinct_id":"a","properties":{"plan":null}}, ...b null, ...c "pro"]  -> {"accepted":3}
  GET /v1/breakdown?event=signup&property=plan
    -> groups: [{"value":"","count":2}, {"value":"pro","count":1}]
  GET /v1/trends?event=signup&breakdown=plan
    -> series: [("<nil>", 2), ("pro", 1)]

Same three events, same property, two different labels for the same group. Nothing prevents it:
- /Users/arjun/smolanalytics/internal/event/event.go declares Properties as a plain map[string]any; there is no sanitize/strip step anywhere (grep for delete(...Properties/sanitiz/normalizeProps returns nothing).
- The ingest handler at /Users/arjun/smolanalytics/internal/api/api.go:770-795 only *adds* browser/os/country; it never inspects existing values, so a JSON null round-trips to a nil map entry, and `_, ok := e.Properties[property]` is true, so neither surface routes it to "(none)".
- /Users/arjun/smolanalytics/internal/query/query.go:305 toStr has `if v == nil { return "" }`; /Users/arjun/smolanalytics/i

## 66. [low] ComputeRange is closed [from, to] though documented [from, to), so a boundary event lands in BOTH the current and prior window and disagrees with trends over identical bounds

**Where:** `internal/web/web.go:126` · **Area:** web-engagement-session

**How it fails:** The dashboard passes the same bounds to both engines, with priorTo == curFrom exactly (both `endT.AddDate(0,0,-rangeDays)`, day-aligned to midnight for presets). internal/web excludes only events strictly after `to`, so it is closed at both ends; internal/trends excludes `to`, so it is half-open. An event at exactly curFrom is counted by web in the current window AND in the prior window, corrupting VisitorsDelta/PageviewsDelta ("vs prior"); an event at exactly curTo is counted by web but not by trends, /v1/trends or the MCP trends tool, so the "Visitors · 30d" tile and the "$pageview · 30d" tile beside it disagree by one visitor on the same row. Exact-midnight timestamps are routine, not theoretical: the importer accepts a bare date layout.

**Fix:** Make ComputeRange match its documented half-open contract and the trends engine: replace `e.Timestamp.After(asof)` with `!e.Timestamp.Before(asof)`. Add a boundary test asserting an event at exactly `to` is excluded and an event at exactly `from` is included, and that cur+prior over adjacent windows never double-count.

**Verifier:** Confirmed by construction against the reviewed code. internal/web/web.go:126 used `e.Timestamp.Before(cutoff) || e.Timestamp.After(asof)` (closed at both ends) while internal/trends/trends.go (Compute and ComputeInterval) uses `!ts.Before(to) -> skip` (half-open), and internal/api/dashboard.go hands the SAME bounds to both.

Trigger 1 (double count): dashboard.go:1680-1685 sets priorTo = day0.AddDate(0,0,-(rangeDays-1)), identical to curFrom. A probe mirroring that math (nowT=2026-08-04T15:22:07Z, rangeDays=30, one $pageview at exactly 2026-07-06T00:00:00Z) produced cur.pv=1 cur.vis=1 AND prior.pv=1 prior.vis=1 — the same event in both windows, so VisitorsDelta/PageviewsDelta are computed against a prior containing one of the events it is compared to.

Trigger 2 (cross-engine disagreement): custom range ?from=2026-07-01&to=2026-07-31 yields curTo=2026-08-01T00:00:00Z (dashboard.go:1458 inclusive-end +1 day). With three $pageviews at exactly that instant plus one inside, web.ComputeRange returned pageviews=4/visitors=4 while trends.ComputeInterval over identical bounds returned total=1 — the web tile counts an out-of-range day the neighbouring trend tile excludes.

Reachability: int

## 67. [low] Bounce/engaged sparklines are computed over a different window than the number printed above them

**Where:** `internal/api/dashboard.go:2160` · **Area:** web-engagement-session

**How it fails:** The KPI value comes from web.ComputeRange(evs, curFrom, curTo) — which honours the custom range end (rangeAsof) and the 6h/12h sub-day presets — but the sparkline under it comes from engagementSeries(evs, rangeDays, nowT), which anchors to real wall-clock now and clamps days to [2,30]. With ?from=2026-01-01&to=2026-01-31 the tile reads January's bounce rate while the line beneath it plots the last 30 days ending today: two different windows, one label, no indication. With ?hours=6 (rangeDays forced to 1, then clamped up to 2) the tile reads a 6-hour bounce rate over a 2-day sparkline. With ?days=90 the tile reads 90 days and the line reads 30. The function's own doc comment asserts the opposite guarantee.

**Fix:** Pass the tile's own bounds through: change engagementSeries to take (from, to) instead of (days, now), derive the bucket count from that span, and call it with curFrom/curTo. Where the span is under a day or over 30, either bucket at a finer/coarser interval or omit the sparkline rather than silently plotting a different window.

**Verifier:** CONFIRMED, with severity downgraded to low.

Code path verified end to end in /Users/arjun/smolanalytics/internal/api/dashboard.go:
- `evs` is the FULL history (dashboard() streams the whole store at :1374-1390, filtered only by env/site/chips), so a now-anchored helper genuinely sees events outside the selected range — no upstream windowing saves it.
- Tile value: `wv := web.ComputeRange(evs, curFrom, curTo)` (:2197), where curFrom/curTo honour `rangeAsof` (custom ?from/?to) and the sub-day `rangeHours` presets (:1666-1684).
- Sparkline: `engagementSeries(evs, rangeDays, nowT)` (:2210) with `nowT = time.Now().UTC()` (:1626). engagementSeries (:746-753) ignores rangeAsof entirely (`today := now.Truncate(24h)`) and clamps `days` to [2,30].
- The tile is explicitly labelled with the selected window in dashboard.tmpl.html:1159 and :1166 ("Bounce rate · {{.RangeLabel}}", RangeLabel = rangeWindowLabel(rangeDays, rangeHours)), so label, number and line are asserted to be one window.
- buildSpark (:705) bails only on len<2, so the wrong-window line always renders.

Concrete trigger, executed as a temporary Go test in package api (since deleted) with wall clock 2026-08-04, January traffic 

## 68. [low] The week-over-week trend finding never calls UpIsBad, so a failure event collapsing is filed as a warning and one that tripled is filed as info — the exact inversion polarity_test.go fixed for anomalies() only

**Where:** `internal/insight/insight.go:273` · **Area:** insight-verdict

**How it fails:** When `signup` is absent, `head` becomes names[0], the highest-volume event. On an instance where a failure event is the top event, dead clicks collapsing from 100 to 30 (good news) renders `[warn/trend] dead clicks is down 70% week-over-week` while dead clicks tripling from 100 to 300 (a real regression) renders `[info/trend] dead clicks is up 200% week-over-week` — verified both outputs. The warn then sorts to the top of the verdict card by insight.go:316 and the good news becomes the lead item, while the actual regression sits below the fold among the wins. human.go:60 exports UpIsBad precisely for this and anomalies() calls it 100 lines lower (insight.go:375, `rose, bad := dev > 0, UpIsBad(n)`); polarity_test.go covers only anomalies(), so the same bug survives untested in the sibling detector.

**Fix:** Mirror the anomaly branch: `rose, bad := change > 0, UpIsBad(head)`; set sev = "warn" only when `rose == bad` and |change| >= 15, and extend polarity_test.go to cover the KindTrend path with the same table it already uses for KindAnomaly.

**Verifier:** CONFIRMED, but narrower than reported.

The defect exists as described. /Users/arjun/smolanalytics/internal/insight/insight.go:270-288 (the week-over-week trend finding) hardcodes polarity: `sev, dir := "info", "up"` and only escalates to "warn" when `change < 0`. It never consults `UpIsBad` (/Users/arjun/smolanalytics/internal/insight/human.go:75), which its sibling detector `anomalies()` does call at insight.go:375 (`rose, bad := dev > 0, UpIsBad(n)`), escalating on `rose == bad`. No guard, earlier validation, or type constraint prevents a failure event from reaching the trend branch: `head` is "signup" only when `has("signup")`, otherwise `names[0]` (insight.go:250-253), and `names` is the raw event set sorted by volume (insight.go:162-167). The only upstream filter is `withoutGeoChecks`, which removes `$geo_check` only. Callers (dashboard.go:1426, explore.go:51, fixbrief.go:94, mcp.go:776, brief.go:104) filter by env/site, never by event name, so they cannot prevent it either.

I reproduced it with a temporary test in the package (since deleted), calling `insight.Generate` on synthetic events:
  $deadclick 100 -> 30  => [warn/trend] dead clicks are down 70% week-over-week
  $de

## 69. [low] fixbrief.lowN describes every finding's N as "people", but for four of the seven kinds N is events, bot fetches or sampled model runs

**Where:** `internal/fixbrief/fixbrief.go:369` · **Area:** insight-verdict

**How it fails:** An anomaly finding carries N = s.baseTotal, a count of EVENTS over 7 days (insight.go:377); a trend finding carries N = prev7, also events (insight.go:284); a crawl finding carries N = worst.Hits / r.Hits, HTTP fetches by a bot; an AI-visibility finding carries N = curRuns, sampled model answers. lowN turns all of them into "Sample: 40. That is thin. A percentage over 40 people is directional, not a measurement", and that sentence is embedded verbatim in b.Prompt — the block whose whole purpose is to be pasted into a coding agent as ground truth. An agent reading "40 people" for what is actually 40 GPTBot fetches will size its confidence on a population that does not exist.

**Fix:** Switch on f.Kind to pick the noun ("people" for KindDropoff/KindSegment/KindRetention, "events" for KindAnomaly/KindTrend, "crawler fetches" for KindCrawl, "sampled answers" for KindAIVis), or carry the unit on the Finding itself so both the caveat and any renderer read one source.

**Verifier:** CONFIRMED by execution. lowN (fixbrief.go:364-373) branches only on f.N's magnitude, never on f.Kind, and build() assigns b.Note = lowN(f) at line 186 *before* the kind switch, so every finding kind gets the same "people" sentence; renderPrompt embeds it verbatim (lines 381-383).

Concrete triggering input I ran as a temporary test in package fixbrief (since removed, tree clean): 25 `signup` events all from ONE distinct_id timestamped 8-13 days ago, plus 5 in the last 7 days, passed to ComputeAll(evs, nil, now). insight.GenerateForFunnel produces a KindTrend finding with N = prev7 = 25 (an EVENT count, insight.go:284). Actual observed output:

  KIND=trend N=25 NOTE=Sample: 25. That is thin. A percentage over 25 people is directional, not a measurement — ...
  EV: signup volume = -80% week-over-week (25 in the prior week)

One person, 25 events, described as "25 people". The 20..99 window is guaranteed to be non-empty by design: minSample=20 gates the finding in, smallSample=100 is the caveat ceiling.

No guard, validation, or type constraint prevents it. insight.Finding.N is documented only as "the base the rate is computed over" (insight.go:53) and the detectors assign four diffe

## 70. [low] segment.Store.Prune does not rebuild the name index, so Names() keeps reporting event names whose only data was pruned away

**Where:** `internal/store/segment/segment.go:294` · **Area:** store-ingest

**How it fails:** Retention prunes the only segment containing `legacy_event`; `s.names` is never recomputed (Prune touches only s.manifest and s.hot). Verified by probe: after pruning the sole segment holding it, `Names() -> [current_event legacy_event]` with `Count: 1`. Both other backends rebuild names on Prune (memory/memory.go:101 rebuilds `names` from kept; file/file.go:250 rebuilds it inside compactToLocked), so this is a third cross-backend divergence: the same retention policy leaves the segment store's event list stale forever. Downstream, list_events and the funnel/trends step validators accept the phantom name and confidently return a 0%/empty report for it instead of the 400 a genuinely unknown name would get.

**Fix:** Reuse DeleteUser's rebuild block in Prune: after the manifest is persisted (and after hot.Prune returns), recompute s.names from the surviving segMeta.Names plus s.hot.Names(). Factor it into a `rebuildNamesLocked()` helper so the three call sites cannot drift again.

**Verifier:** Not refuted — reproduced with a concrete input. segment.Store.Prune (segment.go:291-327) mutates s.manifest, persists it, deletes blobs, and calls s.hot.Prune, but never touches s.names; Names() (line 271) reads only that cached map, which is filled at Open (lines 65/70) and Ingest (line 136) and only grows. Trigger: ingest two `legacy_event` rows at T-90d so they seal into a cold segment whose only name is legacy_event, plus one `current_event` at now, then Prune(now-30d). Executed result: `pruned=2 segments=0 names=[current_event legacy_event] count=1`, and `after reopen names=[current_event]` — so it is stale for the life of the process and self-heals only on restart. The store's own invariant is names = union(manifest names, hot names): Clear (line 339) resets it and DeleteUser (lines 457-468) explicitly rebuilds it with the comment "names may have shrunk — rebuild from what remains"; Prune is the only mutation that skips it. Cross-backend claim also verified: memory/memory.go:103 rebuilds names from kept, file/file.go:184 prunes via compactToLocked which rebuilds names at line 250. Reached in production: pruneLoop in cmd/smolanalytics/main.go:462-481 calls st.Prune on boot and

## 71. [low] GET /v1/cohorts/{id}/users emits the member list in Go map-iteration order and is unbounded

**Where:** `internal/api/cohort_api.go:56` · **Area:** api-surfaces

**How it fails:** Two identical requests for the same cohort over the same immutable event log return the same `count` but a different `users` ordering every time, so nothing downstream (a diff, a cached snapshot, a byte-comparison agreement test, an agent re-reading the list) can rely on the response being reproducible — the one property the product sells. The slice is also uncapped, unlike every other user-list surface (apiWho caps at 200), so a large cohort serialises every distinct_id in one response.

**Fix:** sort.Strings(ids) before writing, and cap the returned slice (keeping the true `count`) the way apiWho's respond() does.

**Verifier:** Reproduced directly. I wrote a temp test in package api: seeded 30 users via POST /v1/events, created a cohort via POST /v1/cohorts, then called GET /v1/cohorts/{id}/users six times against the same immutable memory store. count was 30 every time, but the users array ordering differed on 5 of 5 repeats (e.g. ["u16","u23","u04","u19","u01",...]). Triggering input is any cohort with more than one member; the code path is unconditional.

No guard exists: cohort.Resolve (internal/cohort/cohort.go:51) returns map[string]bool, cohort_api.go:56-59 ranges it into a slice with no sort, and writeJSON emits it verbatim. The route (internal/api/api.go:334) has no wrapper, and there is no limit/offset handling. The package convention is the opposite — apiWho sorts by LastSeen and caps at 200 with a separate total_users (query_api.go:757,762-764), and query_api.go:221,492 plus dashboard.go:1408 sort.Strings their map-derived lists. No test anywhere touches this endpoint, and no docs, dashboard code, or MCP tool consumes it (MCP only uses len(cohort.Resolve(...))).

So the defect is real but its blast radius is narrower than described: count is stable, no aggregate is wrong, and the agreement/sel

## 72. [low] reset() clears only the distinct id — the previous user's session_id, flag values and exposure dedupe survive logout

**Where:** `internal/api/sdk.js:687` · **Area:** sdk-js

**How it fails:** App calls smolanalytics.reset() on logout. `did` is cleared but `sess`, `flagCache`, `flagMeasured`, `flagExposed`, `surveyShownThisLoad`, `engPath` and `engAccum` are untouched and no flag re-fetch happens. So: (a) the next page's events carry the LOGGED-OUT user's session_id (ensureSession sees a non-null sess within its 30-minute window and keeps it), and web.go computes entry/exit pages per session_id — one session now spans two different visitors, so 'where do people land / where do visits die' attributes user A's landing page to user B's visit; (b) flag("x") keeps returning the previous user's resolved value to the now-anonymous visitor — a paid/beta gate stays open after logout; (c) flagExposed still marks measured flags as exposed, so the new visitor's variant is never logged as an exposure while their conversion events still land, skewing the experiment; (d) the in-flight $engagement is attributed to whichever id happens to resolve at report time.

**Fix:** In reset(): flush() the pending queue first (so the outgoing events keep the old identity), then null out sess and remove localStorage "smol_session", clear flagCache/flagMeasured/flagExposed, reset surveyShownThisLoad, zero engAccum/engStart, mint a fresh did via distinctId(), and call fetchFlags() again. Deliberately keep `bid` (bucket stability), as the comment at sdk.js:74-78 intends.

**Verifier:** Primary claim confirmed, three of four consequences refuted, severity overstated.

CONFIRMED (a): sdk.js:687-690 `reset()` only removes `smol_did`. `sess` survives both in memory and in localStorage["smol_session"], and ensureSession() (sdk.js:144-166) reuses it for the rest of the 30-minute window, including across full page reloads. I executed the real sdk.js under a stubbed browser: identify("user-A") -> track -> reset() -> track produced three events all carrying sid=s-72yhu8cm109msejfkr6 while distinct_id flipped from user-A to a fresh anon id, and smol_session was still present in storage after reset. internal/web/web.go:168-177 keys entryFirst/exitLast on session_id alone, so that merged session reports user A's landing page as the entry and the post-logout visitor's page as the exit. Trigger is exact and the path reaches the defect, so not refuted.

REFUTED (b) "flag() keeps returning the previous user's value; a paid/beta gate stays open": flags here are per-browser, not per-user. fetchFlags (sdk.js:509-528) sends only distinct_id and bucket_id and never sends `context`. evaluateFlags (internal/api/flags_api.go:92-108) sets bucketKey = bucket_id and passes ctx=nil to Flag.
