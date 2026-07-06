# smolanalytics

[![ci](https://github.com/Arjun0606/smolanalytics/actions/workflows/ci.yml/badge.svg)](https://github.com/Arjun0606/smolanalytics/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-f5a623)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/Arjun0606/smolanalytics)](https://goreportcard.com/report/github.com/Arjun0606/smolanalytics)
[![release](https://img.shields.io/github/v/release/Arjun0606/smolanalytics?color=f5a623)](https://github.com/Arjun0606/smolanalytics/releases)

**your ai assistant admits it hallucinates your numbers. mine can't. it's a ci test.**

The only analytics whose AI answer provably matches the dashboard. [The agreement test](internal/api/agreement_test.go) runs in CI and fails the build if they ever differ. Ask *"did activation improve this week?"* right in Cursor or Claude Code and your own model answers from your real data over [MCP](https://modelcontextprotocol.io), so there is no AI bill and no black box. It opens with a verdict that tells you what to fix. One MIT binary you run yourself, and your data never leaves your box.

![smolanalytics in 25 seconds: the verdict finds your biggest drop-off, you ask "which channel converts best?" and get the exact computed answer, then the full product view of funnels, retention, cohorts.](docs/demo.gif)

<sub>That's the real product on demo data. Run it yourself in 30 seconds below. ([still image](docs/dashboard.png))</sub>

## See it live, no install

**[smolanalytics-demo.fly.dev](https://smolanalytics-demo.fly.dev)** is the real product on demo data, running right now. The verdict up top, the ask bar, your events and pages as chips. Click `checkout`, or type "visitors to /pricing", and get the exact computed answer. The whole thing, one click away.

## Run it yourself (30 seconds)

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

Give it a few seconds to pull and seed, then open `localhost:8080`. Same fully populated dashboard, now on your machine. (If port 8080 is busy, use `-p 8081:8080` and open `localhost:8081`.) The ask bar shows your real event names and pages as chips, so asking is a pick, not a guess: click `checkout` or type "visitors to /pricing" and get the exact computed answer.

## Two ways to ask (and which is which)
smolanalytics answers plain-English questions on two surfaces. They are different, and keeping them straight is the whole model:

- **The dashboard ask bar** asks about your **data**. It's built in, zero setup, deterministic. It does not read your code: it knows your events and pages because they're in your data, and it shows them as chips so you never guess a name. Good for "how many checkout this week?", "visitors to /pricing", "where do people drop off?".
- **Your coding agent over MCP** (Cursor / Claude Code) asks **code-aware** questions. It has your codebase, your tracking plan (`smolanalytics.plan.json`), and smolanalytics over MCP, so it bridges the two: ask "what's the MAU for the PQR page" and the agent knows PQR is the `/pqr` route from your code, then queries smolanalytics for it. This is also where you instrument ("add tracking to the checkout flow I just built") and verify.

Recurring metrics never need re-typing: pin any report you build (or have your agent call `save_report`) and it renders on the dashboard every visit.

## Why this exists
Every analytics tool now has an AI assistant, but it's bolted *inside their app*, you pay for it, and you still leave your editor to use it. smolanalytics flips it: the analytics comes to where you already work, answered by the model you already pay for.

- **Ask in your editor, for free.** It's an MCP server: connect Claude / Cursor / Claude Code and ask in plain English. Your model does the reasoning, so there are no API keys and no metered AI credits. The dashboard has a built-in ask bar too, zero setup.
- **Answers are computed, never generated.** Every other tool's AI assistant admits it hallucinates. Ours calls exact, deterministic reports (not guessed SQL), so the number it returns is the real computed number or nothing. Your model can still ask the wrong question, but it cannot invent an answer. And this isn't a promise, it's CI: [the agreement test](internal/api/agreement_test.go) asserts the MCP answer and the HTTP API answer are identical for the same question, every build, forever. There is no second query path that can drift.
- **Google Search Console, built in.** `smolanalytics gsc auth` (BYO OAuth client, two env vars) and your top search queries (clicks, impressions, position, biggest movers) appear on the dashboard, in the `search_console_report` MCP tool, next to what those visitors did after landing.
- **Real product analytics AND web analytics, one tool.** Funnels, retention, trends, segmentation, lifecycle, stickiness, paths, cohorts, B2B accounts, plus the Plausible-shaped web view (visitors, live-now, top pages, referrers, UTM sources, devices). The usual answer is "run Plausible AND something heavier"; this is both, in one binary.
- **One binary, not a cluster.** No Kafka/ClickHouse/Redis, no 12-hours-debugging-self-host. `docker run` and it's up. Your data never leaves your box and never trains anyone's model.
- **One instance, all your projects.** The SDK stamps every event with its site's hostname: point every product you run at the same instance, switch sites on the dashboard, filter any report (or any MCP question) by site, and the morning brief breaks down per product. You do not need a server per project.
- **Beautiful by default.** Server-rendered, instant, opinionated. It looks designed, not assembled.
- **Open source (MIT), genuinely self-hostable.** Own the whole thing, with no paywalled features stripped from the self-hosted edition.

**Why not just use Mixpanel or PostHog?** They're deeper, but there are three things they *structurally can't* match, because it would break their business: (1) **the AI is yours, so it's free** (they meter theirs); (2) **answers come from exact reports, not generated SQL**, and CI enforces that they match the dashboard, which their own MCP docs don't promise; (3) **your data never leaves your box** (theirs lives in their cloud). Same funnels/retention, a fraction of the price, and it tells you what to fix instead of making you dig. You can even try it before moving anything: `plan check --source=posthog` runs our tracking-drift gate against your existing PostHog project, and when you switch, `smolanalytics import` replays your history with original timestamps ([docs/migration.md](docs/migration.md)).

## The most private analytics you can run
Every hosted analytics tool, the privacy-first ones included, still asks you to trust *their* servers with your users' data. smolanalytics keeps no cloud in the loop: it's a binary on your own box, so the data physically never leaves your infrastructure.

- **No third party, ever.** Nothing to sign a DPA with, no processor to disclose, nothing crossing a border. The answer to "who can see this data?" is just: you.
- **No third-party cookies, no fingerprinting, no cross-site tracking.** The browser SDK uses a first-party anonymous id and nothing else.
- **Cookieless mode, no consent banner needed.** `smolanalytics.init(key, { anonymous: true })` stores *nothing* on the visitor's device; the server derives a daily-rotating anonymous id instead (Plausible's model). Visitors are unlinkable across days, funnels still work within a day, and identified users (after login) keep full analytics. Consent banners cost ~55% of your data; this mode needs none.
- **Right to erasure, built in.** `DELETE /v1/users/{id}/data` (or ask your AI: *"delete everything about user u123"*) erases a person's events across every storage tier. The GDPR request that takes a ticket queue elsewhere is one call here.
- **It never trains a model.** There's no model and no vendor, so there's no one to train on your data.
- **Private by architecture, not by policy.** It isn't private because of a promise on a privacy page; it's private because there's no one else in the loop.

Plausible, Fathom, and Simple Analytics are lovely, and far more private than Google. But they're still a cloud you send data to. Self-hosting is the version where the data never leaves at all.

## Built to outlive its maker
Betting on a small tool shouldn't mean betting on the person behind it. The architecture makes us unnecessary:

- **MIT, no CLA.** There is no license to revoke and no relicense lever to pull ([LICENSE](LICENSE)). Fork it the day you stop liking us.
- **One static binary, no external services.** It calls no hosted API, has no license server, and never phones home. It does not know we exist.
- **Your data is open files on your own disk.** An append-only log that seals into columnar segments, with the format and its compatibility guarantees written down in [STABILITY.md](STABILITY.md) and [docs/design/storage.md](docs/design/storage.md).
- **Export any time.** `GET /v1/export` hands you everything as CSV or JSONL, and the JSONL round-trips straight back into `/v1/events`.
- **Works forever without us.** If this repo went dark tomorrow, your instance would not notice.

## Ask it in your editor (the whole point)
This is the code-aware surface: your coding agent has your codebase, your tracking plan, and smolanalytics over MCP, so it answers questions in your own terms. Ask "what's the MAU for the PQR page" and it knows PQR is the `/pqr` route from your code, then queries smolanalytics for it. (For quick data questions with no code lookup, the dashboard ask bar is faster and needs no setup. See "Two ways to ask" above.)

Connect once:

```sh
smolanalytics connect          # wires it into every coding assistant you have installed
```
That's it: restart the editor and ask *"where are users dropping off this week?"*. It
detects your installed assistants and merges the config in (keeping any MCP servers you
already have). Target one with `smolanalytics connect <name>`:

| Assistant | `connect <name>` | It configures |
|---|---|---|
| Claude Desktop | `claude` | `claude_desktop_config.json` |
| Claude Code | `claude-code` | runs `claude mcp add` for you |
| Cursor | `cursor` | `~/.cursor/mcp.json` |
| Windsurf | `windsurf` | `~/.codeium/windsurf/mcp_config.json` |
| VS Code (Copilot) | `vscode` | user `mcp.json` (`servers` key) |
| Cline | `cline` | `cline_mcp_settings.json` |

Any MCP client works: it's a standard stdio + Streamable-HTTP server.

**Building with Lovable, Bolt, v0, or Replit instead?** You don't need any of the above.
[Sign up](https://smolanalytics.com), create a project, and it hands you one prompt to
paste into your app builder. The builder's AI installs the snippet and wires your key
events itself. From then on the dashboard answers questions in plain English and the
morning brief lands by email: zero code, zero terminal. More at
[smolanalytics.com/for/lovable](https://smolanalytics.com/for/lovable).

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

(When a write key is set, add `"headers": { "Authorization": "Bearer YOUR_KEY" }` next to the url.)
</details>

Then just ask, in the same window you write code:
```
you ▸ how's activation, and is pro converting better than free?
ai  ▸ Activation is 62% (657 of 1,051 signups reach "activate").
      Pro converts 2.4× better end-to-end: 45% signup→checkout vs 19% on free.
      The leak is activate→checkout on free (only 31% continue). Want the paths after activate?
```
Your model gets **47 tools + 13 built-in prompts**. The editor runs the *whole thing*, not just queries:

- **Ask:** `whats_notable` (the *what-to-fix* verdict), `overview`, `list_events`, `funnel`, `retention`, `trends`, `breakdown`, `web_overview` (traffic at a glance), `lifecycle`, `stickiness`, `paths`, `groups` (B2B accounts), `recent_events`, `user_activity`, every one filterable by property (`plan=pro`, `source=hn`, …).
- **Do:** *"alert me if signups drop below 10 a day"* → `create_alert`; *"send alerts to Slack"* → `add_webhook`; *"track paying users as a group"* → `create_cohort`; *"pin that funnel to my dashboard"* → `save_report`, plus list/delete for each. Everything created in your editor appears on the dashboard instantly (same stores, one source of truth). A saved report keeps rendering on the dashboard every visit, so recurring metrics never need re-typing.
- **Run the instance:** rename the project, set the timezone and retention, create/revoke API keys, full settings parity, no browser.
- **Verify the instrumentation** (built for AI-assisted building): the agent that wires your tracking declares it with `set_tracking_plan`, then `instrumentation_health` checks reality against the plan: which events are flowing, which never arrived, which expected properties are missing. The loop closes: *build → instrument → verify → watch*, all in the editor.
- **Prompts:** 13 named jobs surfaced natively by MCP clients: `instrument-my-app` (full setup, end to end), `whats-broken-today` (the morning check), `weekly-review` and `monthly-report` (founder-grade recaps), `funnel-leak`, `launch-day`, `money-pages` (the SEO wins already in reach), and the rest. The full library, with what each reads and the shape of the answer: [docs/prompts.md](docs/prompts.md).

### Shipping with an agent? Make tracking automatic
If Claude Code or Cursor writes your features, it can write your instrumentation too:
one block in your repo's `CLAUDE.md` / `AGENTS.md` / `.cursorrules` tells your agent to
add `track()` for every feature it ships, keep `smolanalytics.plan.json` current, and
verify events actually flow with `smolanalytics plan check` after each deploy. The
copy-paste block and the full build → instrument → verify loop: [docs/agents.md](docs/agents.md).
Already on PostHog? `plan check --source=posthog` runs the same gate against your
existing PostHog project, no server, no migration ([docs/agents-ci.md](docs/agents-ci.md#already-on-posthog)).

## Send events (2 minutes, zero instrumentation)
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

…or POST directly from any language (single event or a batch):

```sh
curl -X POST https://YOUR_HOST/v1/events \
  -H "Authorization: Bearer YOUR_WRITE_KEY" \
  -d '{"name":"signup","distinct_id":"user_123","properties":{"plan":"pro"}}'
```

### From any platform: web, mobile, server
Ingestion is one HTTP endpoint, so **mobile apps, backends, and anything else send events the same way.** No heavy SDK required:

```swift
// iOS (Swift)
var req = URLRequest(url: URL(string: "\(host)/v1/events")!)
req.httpMethod = "POST"; req.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
req.setValue("application/json", forHTTPHeaderField: "Content-Type")
req.httpBody = try JSONSerialization.data(withJSONObject: ["name": "signup", "distinct_id": userId])
URLSession.shared.dataTask(with: req).resume()
```
```kotlin
// Android (Kotlin / OkHttp)
val body = """{"name":"signup","distinct_id":"$userId"}""".toRequestBody("application/json".toMediaType())
client.newCall(Request.Builder().url("$host/v1/events").addHeader("Authorization","Bearer $key").post(body).build()).enqueue(cb)
```
```js
// React Native / Node / any JS backend
fetch(`${host}/v1/events`, { method: "POST",
  headers: { "Content-Type": "application/json", Authorization: `Bearer ${key}` },
  body: JSON.stringify({ name: "purchase", distinct_id: userId, properties: { amount: 29 } }) });
```
```python
# Python backend
requests.post(f"{host}/v1/events", headers={"Authorization": f"Bearer {key}"},
              json={"name": "signup", "distinct_id": user_id, "properties": {"plan": "pro"}})
```
The browser SDK adds autocapture + batching on top; everywhere else, it's a 5-line POST. Same engine, same "ask in your editor," same verdict, whatever your product runs on.

Even easier: paste *this* into Cursor/Claude Code and let it instrument your app.
> "Add smolanalytics: load `https://YOUR_HOST/sdk.js`, init with my key, and `track()` the key moments (signup, activate, checkout) plus `identify()` on login."

**Framework guides** (copy-paste, two minutes each): [Next.js](docs/nextjs.md) · [React](docs/react.md) · [Vue](docs/vue.md) · [Backend](docs/backend.md) (Node/Python/Go/Ruby/PHP) · [Mobile](docs/mobile.md) (iOS/Android/RN). See [`examples/`](examples/) for a runnable page + curl script.

## How it compares
Honest version: we're **not** deeper than the big tools. The bet is a different shape:

| | smolanalytics | Plausible / Fathom | Mixpanel / Amplitude | PostHog |
|---|:---:|:---:|:---:|:---:|
| Funnels · retention · paths · cohorts | ✅ | ⚠️ Plausible: paid tier · Fathom: ❌ | ✅ | ✅ |
| Web analytics (pages · referrers · live) | ✅ | ✅ | ⚠️ | ✅ |
| Ask in plain English | ✅ **your AI, free** | ❌ | 💲 their AI | 💲 their AI + MCP |
| The AI's numbers match the dashboard | ✅ **always, same engine** | n/a | ⚠️ | ⚠️ their docs: *"may not match the UI"* |
| Self-host | ✅ one binary | ✅ | ❌ | ⚠️ Kafka + ClickHouse cluster |
| Own your data · export | ✅ | ✅ | ⚠️ | ✅ |
| Price | free / self-host | 💲 | 💲💲💲 | free tier + 💲 |

On that accuracy row: PostHog's MCP is real and good, but it answers by generating
HogQL, and [their own docs](https://posthog.com/tutorials/mcp-analytics) note results
"sometimes differ from PostHog's dashboard: sampling, timezone handling, default
filters, caching," with the advice to "verify in the UI" for numbers that matter. Ours
can't differ: the MCP tools call the exact same deterministic reports the dashboard
renders. There is no second path to disagree with.

## What we'll never add
The graveyard of analytics tools is "one tool that became nine." Session replay,
feature flags, A/B testing, surveys, data warehouses: other tools do those well, and
bundling them is how you end up needing a cluster and a pricing calculator. We stay
one binary that answers questions about your events, exactly. If you outgrow that,
you'll know, and your data exports cleanly.

Also on the never-list: **multi-node, clustering, HA.** Exactly one writer per
instance is *why* the storage engine needs no consensus protocol: crash recovery is
replaying one log, not electing a leader. The moment we add a second node we inherit
the distributed-systems tax that makes other tools a weekend to self-host. Need more
isolation? Run more instances (one per project), which is the whole cloud's design too.

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

**Safe by default:** `serve` binds `127.0.0.1:8080` (local only). To expose it, set `ADDR=0.0.0.0:8080`, and then a dashboard password is **required**: the server refuses to serve real data unauthenticated on a public interface (override with `SMOLANALYTICS_ALLOW_UNAUTHENTICATED=1` only on a trusted network). `demo` is exempt (throwaway data).

Config (all env): `ADDR` (default `127.0.0.1:8080`), `SMOLANALYTICS_DB` (event log path), `SMOLANALYTICS_WRITE_KEY` (require a key on ingestion + MCP), `SMOLANALYTICS_PASSWORD` (dashboard login, required to expose the server), `SMOLANALYTICS_RETAIN_DAYS` (drop events older than N days; default: keep forever), `SMOLANALYTICS_MAX_EVENTS` (keep only the newest N events resident, a memory guardrail so a flood degrades to compaction instead of an OOM). Health at `/healthz`, build at `/version`.

**The morning brief, self-hosted:** `smolanalytics brief` prints the pulse (visitors + events vs the prior week) and the "what to look at" findings from the same event log the server uses (set `SMOLANALYTICS_DB` to the same path). A running server also serves the same digest as JSON at `GET /v1/brief`, for your own delivery. Cron it and it arrives every morning:
```sh
# 8am daily, by email or straight into Slack
0 8 * * * SMOLANALYTICS_DB=/data/smolanalytics.data smolanalytics brief | mail -s "analytics brief" you@example.com
0 8 * * * SMOLANALYTICS_DB=/data/smolanalytics.data smolanalytics brief --webhook=https://hooks.slack.com/services/XXX/YYY/ZZZ
```

Running several products into one instance? The SDK stamps each event's `site`, and once more than one site reports the brief adds a per-product breakdown: ship monthly, one brief covers them all.

Manage everything from **Settings** (`/settings`): account + password, API keys, data retention, event taxonomy, exports, webhooks, threshold alerts, and an audit log.

**No lock-in:** export everything any time with `GET /v1/export?format=csv` or `?format=jsonl` (the JSONL round-trips straight back into `/v1/events`). It works on the way in too: `smolanalytics import` replays your history from PostHog, Umami or any events CSV with original timestamps intact. See [docs/migration.md](docs/migration.md).

**Backups:** your data is plain files, so a backup is a copy. Easiest recipe, a nightly cron of the export endpoint:
```sh
0 3 * * * curl -s -H "Authorization: Bearer $KEY" "https://YOUR_HOST/v1/export?format=jsonl" | gzip > /backups/events-$(date +\%F).jsonl.gz
```
A restore is one curl back into `/v1/events`, into this tool or away from it.

**If we disappear:** nothing happens to you. The MIT binary keeps running forever, your events live in plain files on *your* disk or *your* bucket, and exports are one curl. There is no phone-home, no license server, and no closed control plane your data depends on. The binary you run in dev is production: there's no separate "self-host mode" that's secretly the second-class version. Don't take our word for it: the on-disk format and its compatibility guarantees are written down in [STABILITY.md](STABILITY.md) and [docs/design/storage.md](docs/design/storage.md), so you can verify the exit paths before you send a single event.

## What's inside

How an event is stored (the whole write path, inside one binary):

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
- `internal/store`: the `Store` interface with three backends behind it: in-memory, the durable append-log (single box), and the columnar segment tier (`store/segment` + `store/blob`) that seals into compressed segments on local disk or S3/R2. Billions of events on flat memory, ~7 bytes/event.
- `internal/mcp`: the MCP server (stdio + Streamable HTTP), the "ask with your own AI" layer.
- `internal/api`: the single-binary HTTP server: ingestion, SDK, dashboard, Explore, settings, auth, webhooks, alerts, audit.
- `cmd/smolanalytics`: the binary (`serve`, `demo`, `mcp`, `connect`, `gsc`, `import`, `brief`, `plan`, `scrub`).

```sh
make demo    # seed + serve
make race    # tests with the race detector
```

## Don't want to run it? → smolanalytics Cloud
**Self-hosting is the free tier, unlimited, forever.** If you'd rather not run a server, or you're a **team**, the [hosted Cloud](https://smolanalytics.com) does the parts a single binary can't:

- **Zero ops:** we host an isolated instance per project; scale, backups, and uptime are ours.
- **Your whole team:** invites, roles, shared projects (the OSS is single-operator by design).
- **The brief, delivered:** your "what to fix" digest by email + Slack every morning, without you keeping a server up or wiring cron (self-host equivalent: `smolanalytics brief` + cron).
- **Scale + retention:** millions of events, longer history, no ops.

Cloud pricing is simple: a **14-day full-product trial** (every feature, no credit card), then from **$9/mo**. Overage never locks your dashboard. Same product, same "ask in your editor," same own-your-data, just managed. **[Start the trial →](https://smolanalytics.com)**

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
