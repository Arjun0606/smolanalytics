# glossary

The load-bearing nouns, defined once. Overloaded terms are flagged: say which one
you mean.

- **event**: one row: `name` + `distinct_id` + `timestamp` + `properties`. The only
  thing the engine stores.
- **distinct_id**: the user/visitor an event belongs to. Stable per user; the SDK
  persists an anonymous one, `identify()` upgrades it at login, `$anon` (cookieless
  mode) derives a daily-rotating one server-side.
- **property**: a key/value on an event (`plan=pro`). Every report can filter or
  break down by any of them.
- **hot log**: the append-only JSONL file that ingestion fsyncs into. Fast to
  append, replayed on startup.
- **seal**: the act of freezing the hot log's contents into a segment (every 50k
  events by default), after which the hot log is cleared.
- **segment** ⚠️ overloaded:
  1. *storage segment*: an immutable compressed columnar `.sms` file produced by a
     seal (this repo's usual meaning);
  2. *audience segment*: a filtered slice of users in a report (we say **cohort**
     or **breakdown** instead, precisely to avoid this);
  3. *Segment-the-company*: the CDP; unrelated.
- **manifest**: the JSON index of live segments (key, count, time range) kept in
  the blob backend. The single source of truth for what cold data exists.
- **blob backend**: where segments live: a local directory, or S3/R2/Tigris.
- **tier** ⚠️ overloaded: *storage tier* (hot log vs sealed segments) vs *pricing
  tier* (Pro/Scale on the cloud, plus free self-host). Say "storage tier" or "plan."
- **cohort**: a named, reusable group of users defined by events they did
  (`create_cohort`), usable as a filter on any report.
- **tracking plan**: the declared intent: which events an app *should* send, with
  expected properties (`set_tracking_plan`); `instrumentation_health` diffs reality
  against it.
- **MCP tool**: one callable capability exposed to the user's AI (73 of them:
  reports, actions, instance control). Distinct from a *prompt* (a pre-canned
  multi-tool workflow the client surfaces, 14 of them).
- **write key**: the bearer token that authorizes ingestion and MCP access for an
  instance. Not the dashboard password.
