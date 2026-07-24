# smolanalytics

[![ci](https://github.com/Arjun0606/smolanalytics/actions/workflows/ci.yml/badge.svg)](https://github.com/Arjun0606/smolanalytics/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-f5a623)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/Arjun0606/smolanalytics)](https://goreportcard.com/report/github.com/Arjun0606/smolanalytics)
[![release](https://img.shields.io/github/v/release/Arjun0606/smolanalytics?color=f5a623)](https://github.com/Arjun0606/smolanalytics/releases)

**your ai assistant admits it hallucinates your numbers. mine can't. it's a ci test.**

Open-source web + product analytics that runs as one MIT Go binary: funnels, retention, paths, cohorts, and the Plausible-shaped web view, plus feature flags, A/B testing, click heatmaps, in-product surveys, a session inspector, and deploy-impact. No Kafka, no ClickHouse, no cluster, and your data never leaves your box.

You ask any of it in plain English right where you write code, Cursor or Claude Code over [MCP](https://modelcontextprotocol.io), answered by the model you already pay for, so there is no AI bill and no black box. Every answer is computed from an exact, deterministic report, never generated SQL, and a CI [agreement test](internal/api/agreement_test.go) fails the build if the MCP answer and the `/v1` HTTP API ever disagree by a single byte. The dashboard renders from those same reports, so it cannot drift either. It literally cannot hallucinate a number.

![The smolanalytics dashboard: a "fix first" verdict that names the biggest lever to pull (computed, not guessed, with a CI test that proves it equals the dashboard), an ask bar with your real events and pages as one-click chips, KPI cards, and a live trend chart.](docs/dashboard.png)

<sub>That's the real product on demo data, running right now at [smolanalytics-demo.fly.dev](https://smolanalytics-demo.fly.dev). Run it yourself in 30 seconds below.</sub>

## Run it in 30 seconds

```sh
# Docker
docker run -p 8080:8080 ghcr.io/arjun0606/smolanalytics demo
```

or grab the binary:

```sh
curl -fsSL https://raw.githubusercontent.com/Arjun0606/smolanalytics/main/install.sh | sh
smolanalytics demo
```

or with Go:

```sh
go run github.com/Arjun0606/smolanalytics/cmd/smolanalytics@latest demo
```

Give it a few seconds to pull and seed, then open `localhost:8080`: a fully populated dashboard, the verdict up top, the ask bar with your real event names and pages as chips. Asking is a pick, not a guess. Click `checkout` or type "visitors to /pricing" and get the exact computed answer. (If port 8080 is busy, use `-p 8081:8080` and open `localhost:8081`.)

## See it live, no install

**[smolanalytics-demo.fly.dev](https://smolanalytics-demo.fly.dev)** is the real product on demo data, running right now. The verdict, the ask bar, your events and pages as chips. One click away.

## Ask your analytics where you write code

This is the point of the whole tool. smolanalytics is an MCP server, so your coding agent can query your real analytics without you leaving the editor. It has your codebase, your tracking plan (`smolanalytics.plan.json`), and smolanalytics over MCP, so it answers in your own terms. Ask "what's the MAU for the PQR page" and the agent knows PQR is the `/pqr` route from your code, then queries smolanalytics for it.

Your model does the reasoning, so there are no API keys and no metered AI credits. And because the answers come from deterministic reports, your model can ask the wrong question but it cannot invent an answer.

Connect once:

```sh
smolanalytics connect          # wires it into every coding assistant you have installed
```

Restart the editor and ask *"where are users dropping off this week?"*. `connect` detects your installed assistants and merges the config in, keeping any MCP servers you already have. Target one with `smolanalytics connect <name>`:

| Assistant | `connect <name>` | It configures |
|---|---|---|
| Claude Desktop | `claude` | `claude_desktop_config.json` |
| Claude Code | `claude-code` | runs `claude mcp add` for you |
| Cursor | `cursor` | `~/.cursor/mcp.json` |
| Windsurf | `windsurf` | `~/.codeium/windsurf/mcp_config.json` |
| VS Code (Copilot) | `vscode` | user `mcp.json` (`servers` key) |
| Cline | `cline` | `cline_mcp_settings.json` |

Any MCP client works: it is a standard stdio + Streamable-HTTP server.

Then just ask, in the same window you write code:

```
you ▸ how's activation, and is pro converting better than free?
ai  ▸ Activation is 62% (657 of 1,051 signups reach "activate").
      Pro converts 2.4× better end-to-end: 45% signup→checkout vs 19% on free.
      The leak is activate→checkout on free (only 31% continue). Want the paths after activate?
```

Your model gets **73 tools + 14 built-in prompts**, and the editor runs the whole product, not just queries:

- **Ask:** `whats_notable` (the *what-to-fix* verdict), `overview`, `list_events`, `funnel`, `retention`, `trends`, `breakdown`, `web_overview` (traffic at a glance), `lifecycle`, `stickiness`, `paths`, `groups` (B2B accounts), `recent_events`, `user_activity`. Every one filterable by property (`plan=pro`, `source=hn`, and so on).
- **The full product toolkit, same surface:** roll out a feature flag (*"ship checkout_v2 to 20% of users"* becomes `create_flag`) and read the A/B result with real significance (`flag_impact`, a 95% two-proportion z-test); run an NPS/rating/choice/text survey (`create_survey` then `survey_results`); see where people click (`heatmap`); replay a user's journey step by step (`list_sessions` then `session_timeline`); define a behavioral cohort from an ordered sequence (`create_sequence_cohort`). All computed from the same event log you already have.
- **Do:** *"alert me if signups drop below 10 a day"* becomes `create_alert`; *"send alerts to Slack"* becomes `add_webhook`; *"track paying users as a group"* becomes `create_cohort`; *"pin that funnel to my dashboard"* becomes `save_report`, plus list/delete for each. Everything created in your editor shows up on the dashboard instantly (same stores, one source of truth). A saved report keeps rendering on the dashboard every visit, so recurring metrics never need re-typing.
- **Run the instance:** rename the project, set the timezone and retention, create/revoke API keys, full settings parity, no browser.
- **Verify the instrumentation:** the agent that wires your tracking declares it with `set_tracking_plan`, then `instrumentation_health` checks reality against the plan: which events are flowing, which never arrived, which expected properties are missing. The loop closes: *build → instrument → verify → watch*, all in the editor.
- **Prompts:** 14 named jobs surfaced natively by MCP clients, including `instrument-my-app` (full setup, end to end), `whats-broken-today` (the morning check), `weekly-review` and `monthly-report` (founder-grade recaps), `funnel-leak`, `launch-day`, `money-pages`, `retention-review`, `channel-review`, `did-my-deploy-break-anything`. The full library, with what each reads and the shape of the answer: [docs/prompts.md](docs/prompts.md).

### Two surfaces, and which is which

smolanalytics answers plain-English questions on two surfaces. They are different, and keeping them straight is the whole model:

- **Your coding agent over MCP** (above) asks **code-aware** questions. It bridges your codebase and your data: it can look up a route, an event name, or a component in your code, then query smolanalytics for it. This is also where you instrument and verify.
- **The dashboard ask bar** asks about your **data**. It is built in, zero setup, deterministic. It does not read your code: it knows your events and pages because they are in your data, and it shows them as chips so you never guess a name. Good for "how many checkout this week?", "visitors to /pricing", "where do people drop off?".

**Building with Lovable, Bolt, v0, or Replit?** You do not need any of the above. [Sign up](https://smolanalytics.com), create a project, and it hands you one prompt to paste into your app builder. The builder's AI installs the snippet and wires your key events itself. From then on the dashboard answers questions in plain English and the morning brief lands by email: zero code, zero terminal. More at [smolanalytics.com/for/lovable](https://smolanalytics.com/for/lovable).

<details><summary>Wire it up by hand (or point at a running/remote server over HTTP)</summary>

**stdio** (local, no server needed, reads your data file directly):
```json
{ "mcpServers": { "smolanalytics": { "command": "smolanalytics", "args": ["mcp"] } } }
```
**HTTP** (point at a running instance, local or remote, shares its live data):
```json
{ "mcpServers": { "smolanalytics": { "url": "http://localhost:8080/mcp" } } }
```
**Claude Code, HTTP:** `claude mcp add --transport http smolanalytics http://localhost:8080/mcp`
**Zed:** add to `context_servers` in settings. **VS Code:** the top-level key is `servers`, not `mcpServers`.

(When a read key is set, add `"headers": { "Authorization": "Bearer YOUR_KEY" }` next to the url.)
</details>

## The answer cannot drift from the dashboard

Every other analytics tool now has an AI assistant, but it is bolted inside their app, you pay for it, and it answers by generating a query (HogQL, SQL) that can silently disagree with the UI. PostHog's own MCP docs say results "sometimes differ from PostHog's dashboard: sampling, timezone handling, default filters, caching," and advise you to "verify in the UI" for numbers that matter.

smolanalytics removes that whole failure mode by construction. Every one of the 73 tools is either a deterministic report or an action, never generated SQL. Where it is a report, the MCP tool calls the exact same function the dashboard renders and the `/v1` HTTP API serves. There is no second query path that could drift.

And this is not a promise, it is CI. The [agreement test](internal/api/agreement_test.go) asserts, on every build, that the MCP answer equals the `/v1` API answer byte-for-byte for the same question, across `funnel`, `trends`, `retention`, `web_overview`, `lifecycle`, `paths`, `heatmap`, `flag_impact`, `survey_results`, `list_sessions`, and `session_timeline`. If they ever diverge, the build fails. The number the editor returns is the real computed number or nothing.

## Your agent instruments the app, CI keeps it honest

If Claude Code or Cursor writes your features, it can write your instrumentation too. One block in your repo's `CLAUDE.md` / `AGENTS.md` / `.cursorrules` tells your agent to add `track()` for every feature it ships, keep `smolanalytics.plan.json` current, and verify events actually flow with `smolanalytics plan check` after each deploy. The copy-paste block and the full loop: [docs/agents.md](docs/agents.md).

```sh
smolanalytics plan check                 # gate: does live data match the tracking plan?
smolanalytics plan check --source=posthog # run the same gate against an existing PostHog project
```

Already on PostHog? `plan check --source=posthog` runs the drift gate against your existing PostHog project over its query API, no smolanalytics server and no migration required, so you can try the discipline before you move anything ([docs/agents-ci.md](docs/agents-ci.md#already-on-posthog)). When you do switch, `smolanalytics import` replays your history with original timestamps intact.

## Send events (web, mobile, server)

Drop the snippet in and it **autocaptures pageviews + clicks instantly**, so you get real data with no manual event tagging. Add `track()` for the key moments (signup, checkout) when you want funnels.

```html
<script src="https://YOUR_HOST/sdk.js"></script>
<script>
  smolanalytics.init("YOUR_WRITE_KEY", { host: "https://YOUR_HOST" });
  // that's it: pageviews + clicks are captured automatically.
  // optional, for funnels:
  smolanalytics.track("signup", { plan: "pro" });
  smolanalytics.identify("user_123", { email: "a@b.com" }); // on login
</script>
```

Ingestion is one HTTP endpoint, so mobile apps, backends, and anything else send events the same way. No heavy SDK required, single event or a batch:

```sh
curl -X POST https://YOUR_HOST/v1/events \
  -H "Authorization: Bearer YOUR_WRITE_KEY" \
  -d '{"name":"signup","distinct_id":"user_123","properties":{"plan":"pro"}}'
```

```python
# any backend (Python shown; same 5-line POST in Go/Node/Ruby/PHP)
requests.post(f"{host}/v1/events", headers={"Authorization": f"Bearer {key}"},
              json={"name": "signup", "distinct_id": user_id, "properties": {"plan": "pro"}})
```

**Prefer a real native SDK?** Published packages with an offline-safe persisted queue, batching, retries, sessions, lifecycle events, and a cookieless anon id are live:

| Platform | Install | Package |
|---|---|---|
| Swift (iOS / iPadOS / tvOS) | Swift Package Manager | `github.com/Arjun0606/smolanalytics-swift`, product `SmolAnalytics` |
| Kotlin / Android | JitPack | `com.github.Arjun0606:smolanalytics-android` |
| React Native / Expo | npm | `smolanalytics-react-native` |
| Flutter / Dart | pub.dev | `smolanalytics` |

The raw POST above stays the zero-dependency fallback for anything else. Details: [docs/mobile.md](docs/mobile.md).

Even easier: paste this into Cursor or Claude Code and let it instrument your app.
> "Add smolanalytics: load `https://YOUR_HOST/sdk.js`, init with my key, and `track()` the key moments (signup, activate, checkout) plus `identify()` on login."

**Framework guides** (copy-paste, two minutes each): [Next.js](docs/nextjs.md) · [React](docs/react.md) · [Vue](docs/vue.md) · [Backend](docs/backend.md) (Node/Python/Go/Ruby/PHP) · [Mobile](docs/mobile.md) (iOS/Android/RN). See [`examples/`](examples/) for a runnable page + curl script.

## The full platform, one binary

The usual advice is "run Plausible for web analytics AND something heavier for product AND separate tools for flags, experiments, surveys." smolanalytics is all of it, computed from a single append-only event log, in one binary, every surface askable in your editor.

| | What you get | Ask it, or hit the API |
|---|---|---|
| **Product analytics** | funnels (ordered/strict/unordered, exclusions, per-step filters, breakdowns), retention (rolling + weekly buckets), trends (count/sum/avg/p90), paths, lifecycle, stickiness, cohorts, sequenced behavioral cohorts, B2B account groups | `funnel` `retention` `trends` `paths` `lifecycle` `stickiness` `create_cohort` `create_sequence_cohort` `groups` · `/v1/funnel` `/v1/retention` `/v1/trends` `/v1/paths` `/v1/lifecycle` `/v1/stickiness` `/v1/groups` |
| **Web analytics** | visitors, live-now, top pages, referrers, UTM sources, devices, the Plausible-shaped view | `web_overview` · `/v1/web` |
| **Feature flags** | boolean + multivariate, property targeting + percentage rollout, deterministic (FNV) bucketing so the SDK and the agent always agree; `smol.flag()` in the browser SDK | `create_flag` `evaluate_flag` `set_flag_enabled` `list_flags` `delete_flag` · `/v1/flags/evaluate` |
| **A/B testing** | flags measured on a goal event, per-variant conversion counted only after first exposure, lift vs control, 95% two-proportion z-test | `flag_impact` · `/v1/flags/{key}/measure` |
| **Click heatmaps** | click-density grid + top clicked elements per page and viewport, from `$click` autocapture, computed at query time | `heatmap` · `/v1/heatmap` |
| **In-product surveys** | NPS / rating / choice / text, URL + sampling targeting, dependency-free SDK widget | `create_survey` `survey_results` `set_survey_active` `list_surveys` `delete_survey` · `/v1/surveys/active` `/v1/surveys/{id}/results` |
| **Session inspector** | event-based journey replay: pages, clicks with positions, rage-clicks, millisecond timing, reconstructed from the event log | `list_sessions` `session_timeline` · `/v1/sessions` `/v1/session` |
| **Deploy impact** | before/after metric attribution per commit (see below) | `record_deploy` `deploy_impact` `list_deploys` · `/v1/deploys` |

Everything created in the editor appears on the dashboard immediately (same stores, one source of truth), and every report is pinnable so recurring metrics render on every visit.

**One instance, all your projects.** The SDK stamps every event with its site's hostname: point every product you run at the same instance, switch sites on the dashboard, filter any report (or any MCP question) by site, and the morning brief breaks down per product. You do not need a server per project.

## Which deploy moved the metric?

Every other analytics tool shows you that the graph dropped. It cannot tell you **which ship dropped it**, because it does not have your commits. smolanalytics does.

Record a marker when you ship (one line in CI), and it ties every metric change to the deploy behind it:

```bash
# in your CI, after deploy: records the current git HEAD as a deploy marker
smolanalytics deploy      # SMOLANALYTICS_HOST + SMOLANALYTICS_WRITE_KEY set
# or from anywhere:
# curl -XPOST $HOST/v1/deploys -H "Authorization: Bearer $WRITE_KEY" \
#   -d '{"sha":"'"$GIT_SHA"'","message":"tighten checkout validation"}'
```

Then ask your editor:

```
you ▸ did my last deploy move signups?
ai  ▸ signups −38% in the 3 days after deadbeef "tighten checkout validation"
      (was 22/day, now 13/day). Significant given the volume, likely a regression.
      Correlation, not proof, but that ship is the suspect.
```

It is the `deploy_impact` MCP tool (before/after per deploy, leading with any regression) and `GET /v1/deploys?event=<metric>`. Both compute the same numbers, and the agreement test pins the editor's answer equal to the dashboard's, so it cannot drift. Correlation, not proof: the copy always says so.

## Google Search Console, built in

`smolanalytics gsc auth` (bring your own OAuth client, two env vars) and your top search queries, clicks, impressions, position, biggest movers, appear on the dashboard and in the `search_console_report` MCP tool, right next to what those visitors did after they landed. The `money-pages` prompt turns that into the SEO wins already in reach.

## Own it: private by architecture, built to outlive its maker

Every hosted analytics tool, the privacy-first ones included, still asks you to trust their servers with your users' data. smolanalytics keeps no cloud in the loop: it is a binary on your own box, so the data physically never leaves your infrastructure.

- **No third party, ever.** Nothing to sign a DPA with, no processor to disclose, nothing crossing a border. The answer to "who can see this data?" is just: you.
- **No third-party cookies, no fingerprinting, no cross-site tracking.** The browser SDK uses a first-party anonymous id and nothing else.
- **Cookieless mode, no consent banner needed.** `smolanalytics.init(key, { anonymous: true })` stores nothing on the visitor's device; the server derives a daily-rotating anonymous id instead (Plausible's model). Visitors are unlinkable across days, funnels still work within a day, and identified users keep full analytics after login. Consent banners cost roughly 55% of your data; this mode needs none.
- **Right to erasure, built in.** `DELETE /v1/users/{id}/data` (or ask your AI: *"delete everything about user u123"*) erases a person's events across every storage tier. The GDPR request that takes a ticket queue elsewhere is one call here.
- **It never trains a model.** There is no model and no vendor, so there is no one to train on your data.

And betting on a small tool should not mean betting on the person behind it. The architecture makes us unnecessary:

- **MIT, no CLA.** There is no license to revoke and no relicense lever to pull ([LICENSE](LICENSE)). Fork it the day you stop liking us.
- **One static binary, no external services.** It calls no hosted API, has no license server, and never phones home. It does not know we exist.
- **Your data is open files on your own disk.** An append-only log that seals into columnar segments, format and compatibility guarantees written down in [STABILITY.md](STABILITY.md) and [docs/design/storage.md](docs/design/storage.md).
- **Export any time.** `GET /v1/export` hands you everything as CSV or JSONL, and the JSONL round-trips straight back into `/v1/events`.
- **Works forever without us.** If this repo went dark tomorrow, your instance would not notice.

## How it compares

Honest version: the big tools still have more raw surface area (a data warehouse, pixel-perfect replay, years of integrations). But on the analytics you actually use day to day the toolkit now matches them, and on three things they structurally cannot copy, this wins.

| | smolanalytics | Plausible / Fathom | Mixpanel / Amplitude | PostHog |
|---|:---:|:---:|:---:|:---:|
| Funnels · retention · paths · cohorts | ✅ | ⚠️ Plausible: paid tier · Fathom: ❌ | ✅ | ✅ |
| Flags · A/B · surveys · heatmaps · session inspector | ✅ **all one binary** | ❌ | ⚠️ separate / paid add-ons | ✅ |
| Web analytics (pages · referrers · live) | ✅ | ✅ | ⚠️ | ✅ |
| Ask in plain English | ✅ **your AI, free** | ❌ | 💲 their AI | 💲 their AI + MCP |
| The AI's numbers match the dashboard | ✅ **always, CI-enforced** | n/a | ⚠️ | ⚠️ their docs: *"may not match the UI"* |
| Which deploy moved the metric | ✅ | ❌ | ❌ | ❌ |
| Self-host | ✅ one binary | ✅ | ❌ | ⚠️ Kafka + ClickHouse cluster |
| Own your data · export | ✅ | ✅ | ⚠️ | ✅ |
| Price | free / self-host | 💲 | 💲💲💲 | free tier + 💲 |

The three they cannot match, because it would break their business: (1) the AI is yours, so it is free (they meter theirs); (2) answers come from exact reports, not generated SQL, and CI enforces that they match the dashboard, which their own MCP docs do not promise; (3) your data never leaves your box.

## The one thing it deliberately does not do

Feature flags, A/B testing, click heatmaps, in-product surveys, a session inspector, behavioral cohorts: all shipped, all from the same binary and the same event log, no cluster and no pricing calculator. The single deliberate exception is **pixel-perfect DOM / video session replay** (the screen-recording kind). That needs a heavy recorder and a separate blob store, which would break the single-binary, columnar model that makes this thing self-hostable in 30 seconds. The event-based **session inspector** ships instead: it reconstructs a user's journey, pages, clicks with positions, rage-clicks, timing, from the events you already capture. If you genuinely need screen recording, run a tool built for it alongside smolanalytics.

Also on the never-list: **multi-node, clustering, HA.** Exactly one writer per instance is why the storage engine needs no consensus protocol: crash recovery is replaying one log, not electing a leader. The moment we add a second node we inherit the distributed-systems tax that makes other tools a weekend to self-host. Need more isolation? Run more instances, one per project, which is the whole cloud's design too.

## Deploy it (production)

One static binary, no cgo, no cluster. It runs anywhere.

```sh
# Docker (persistent event log on a volume)
docker run -p 8080:8080 -v $PWD/data:/data \
  -e SMOLANALYTICS_WRITE_KEY=$(openssl rand -hex 16) \
  -e SMOLANALYTICS_PASSWORD=$(openssl rand -hex 12) \
  ghcr.io/arjun0606/smolanalytics

# Fly.io (persistent volume + health checks via fly.toml)
fly launch --copy-config && fly deploy
```

**Safe by default.** `serve` binds `127.0.0.1:8080` (local only). To expose it, set `ADDR=0.0.0.0:8080`, and then a dashboard password is required: the server refuses to serve real data unauthenticated on a public interface (override with `SMOLANALYTICS_ALLOW_UNAUTHENTICATED=1` only on a trusted network). `demo` is exempt (throwaway data). Health at `/healthz`, build at `/version`.

**Config (all env):**

| Var | Purpose |
|---|---|
| `ADDR` | listen address, default `127.0.0.1:8080` |
| `SMOLANALYTICS_DB` | event log path |
| `SMOLANALYTICS_WRITE_KEY` | PUBLIC ingest key, gates `POST /v1/events` only; it ships in your pages' HTML |
| `SMOLANALYTICS_READ_KEY` | SECRET read key, gates the `GET /v1` reports, `/v1/export`, and MCP; never put it in client code |
| `SMOLANALYTICS_PASSWORD` | dashboard login, required to expose the server |
| `SMOLANALYTICS_RETAIN_DAYS` | drop events older than N days (default: keep forever) |
| `SMOLANALYTICS_MAX_EVENTS` | keep only the newest N events resident, a memory guardrail so a flood degrades to compaction instead of an OOM |
| `SMOLANALYTICS_COLD` | dir for the scale tier: sealed columnar segments on local disk, bounded RAM regardless of history |
| `SMOLANALYTICS_SEAL_EVENTS` | events per columnar segment once a cold tier is set (default 50,000) |
| `SMOLANALYTICS_S3_BUCKET` (+ `_ENDPOINT` / `_REGION` / `_ACCESS_KEY` / `_SECRET_KEY` / `_PREFIX`) | cold tier to blob storage: seal old segments to S3, R2, or Tigris |
| `SMOLANALYTICS_GSC_CLIENT_ID` / `_SECRET` | Google Search Console OAuth client |

**The morning brief, self-hosted.** `smolanalytics brief` prints the pulse (visitors + events vs the prior week) and the "what to look at" findings from the same event log the server uses. A running server also serves the digest as JSON at `GET /v1/brief`. Cron it and it arrives every morning:

```sh
# 8am daily, by email or straight into Slack
0 8 * * * SMOLANALYTICS_DB=/data/smolanalytics.data smolanalytics brief | mail -s "analytics brief" you@example.com
0 8 * * * SMOLANALYTICS_DB=/data/smolanalytics.data smolanalytics brief --webhook=https://hooks.slack.com/services/XXX/YYY/ZZZ
```

Once more than one site reports, the brief adds a per-product breakdown, so one brief covers every product you run.

Manage everything from **Settings** (`/settings`): account + password, API keys, data retention, event taxonomy, exports, SSRF-guarded webhooks, threshold alerts, and an audit log.

**No lock-in.** Export everything any time with `GET /v1/export?format=csv` or `?format=jsonl` (the JSONL round-trips straight back into `/v1/events`). It works on the way in too: `smolanalytics import --format=<jsonl|csv|posthog|mixpanel|amplitude|umami>` replays your history with original timestamps intact ([docs/migration.md](docs/migration.md)).

**Backups.** Your data is plain files, so a backup is a copy. Nightly cron of the export endpoint:

```sh
0 3 * * * curl -s -H "Authorization: Bearer $KEY" "https://YOUR_HOST/v1/export?format=jsonl" | gzip > /backups/events-$(date +\%F).jsonl.gz
```

**If we disappear:** nothing happens to you. The MIT binary keeps running forever, your events live in plain files on your disk or your bucket, and exports are one curl. The binary you run in dev is production: there is no separate "self-host mode" that is secretly the second-class version. The on-disk format and its compatibility guarantees are written down in [STABILITY.md](STABILITY.md) and [docs/design/storage.md](docs/design/storage.md), so you can verify the exit paths before you send a single event.

## What's inside

How an event is stored, the whole write path, inside one binary:

```
SDK / POST /v1/events
        │
        ▼
append-only hot log ──(fsync per batch, an ACK means it's on disk)
        │  seals every 50k events
        ▼
immutable columnar segment (compressed, CRC'd, ~7 bytes/event)
        │  optional
        ▼
S3 / R2 / Tigris  (flat RAM regardless of history size)
```

There is exactly one writer, your instance, which is why this needs no consensus protocol, no coordination service, and no cluster. Crash recovery is "replay one log," not "rebuild a quorum." The full reasoning (and what we knowingly traded away) is in [docs/design/storage.md](docs/design/storage.md); terms are pinned in the [glossary](docs/glossary.md).

- `internal/{funnel,retention,trends,paths,engagement,groups,cohort,query}`: the deterministic analytics engine (every report, fully tested).
- `internal/{flag,survey,heatmap,session,deploys}`: flags + measured A/B, surveys, click heatmaps, the session inspector, and deploy-impact attribution.
- `internal/store`: the `Store` interface with three backends behind it: in-memory, the durable append-log (single box), and the columnar segment tier (`store/segment` + `store/blob`) that seals into compressed segments on local disk, S3, R2, or Tigris.
- `internal/mcp`: the MCP server (stdio + Streamable HTTP), the "ask with your own AI" layer.
- `internal/api`: the single-binary HTTP server: ingestion, SDK, dashboard, settings, auth, webhooks, alerts, audit.
- `cmd/smolanalytics`: the binary (`serve`, `demo`, `mcp`, `connect`, `gsc`, `import`, `brief`, `deploy`, `plan`, `instrument`, `scrub`).

```sh
make demo    # seed + serve
make race    # tests with the race detector
```

## Don't want to run it? → smolanalytics Cloud

**Self-hosting is the free tier, unlimited, forever, MIT.** If you would rather not run a server, or you are a team, the [hosted Cloud](https://smolanalytics.com) does the parts a single binary cannot:

- **Zero ops:** we host an isolated instance per project; scale, backups, and uptime are ours.
- **Your whole team:** invites, roles, shared projects (the OSS is single-operator by design).
- **The brief, delivered:** your "what to fix" digest by email + Slack every morning, without you keeping a server up or wiring cron.
- **Scale + retention:** millions of events, longer history, no ops.

Pricing is simple: a **14-day full-product trial** (every feature, no credit card), then **Pro $29/mo** (1M events) or **Scale $99/mo** (10M events), with a flat **$5 per extra million**. Overage never locks your dashboard. Same product, same "ask in your editor," same own-your-data, just managed. **[Start the trial →](https://smolanalytics.com)**

## Contributing

PRs welcome. Keep it small, correct, and dependency-free. See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE), forever. No CLA, no rug-pull relicense: the business is the hosted cloud, never the license. Use it, fork it, host it, sell hosting of it if you want.

## Show it off

Using smolanalytics? Add the badge, it helps others find the tool:

```md
[![analytics: smolanalytics](https://img.shields.io/badge/analytics-smolanalytics-f5a623?labelColor=0a0a0a)](https://github.com/Arjun0606/smolanalytics)
```
[![analytics: smolanalytics](https://img.shields.io/badge/analytics-smolanalytics-f5a623?labelColor=0a0a0a)](https://github.com/Arjun0606/smolanalytics)