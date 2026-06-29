# smolanalytics

[![ci](https://github.com/Arjun0606/smolanalytics/actions/workflows/ci.yml/badge.svg)](https://github.com/Arjun0606/smolanalytics/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-f5a623)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/Arjun0606/smolanalytics)](https://goreportcard.com/report/github.com/Arjun0606/smolanalytics)
[![release](https://img.shields.io/github/v/release/Arjun0606/smolanalytics?color=f5a623)](https://github.com/Arjun0606/smolanalytics/releases)

**Product analytics that lives in your editor. You don't build reports — you ask the AI you're already coding with. Free, self-hosted, one binary, your data.**

You ship a feature in Cursor or Claude Code, then ask *"did activation improve this week?"* right there. Your model answers from your real data over [MCP](https://modelcontextprotocol.io). We run no model, so the AI part costs you nothing.

<!-- demo: drop a screen-recording of "ask in Cursor → answer" here → docs/demo.gif -->

## Quickstart (30 seconds)

```sh
# Docker — populated demo dashboard at http://localhost:8080
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

Then open **http://localhost:8080** — a fully populated dashboard, and an ask bar at the top.

## Why this exists
Every analytics tool now has an AI assistant — but it's bolted *inside their app*, you pay for it, and you still leave your editor to use it. smolanalytics flips it: the analytics comes to where you already work, answered by the model you already pay for.

- **Ask in your editor, for free.** It's an MCP server — connect Claude / Cursor / Claude Code and ask in plain English. Your model does the reasoning, so there are no API keys and no metered AI credits. The dashboard has a built-in ask bar too, zero setup.
- **It can't make up numbers.** Every other tool's AI assistant admits it hallucinates. Ours can't — it calls exact, deterministic reports (not guessed SQL), so the answer is the real computed number or nothing.
- **Real product analytics.** Funnels, retention, trends, segmentation, lifecycle, stickiness, paths, cohorts, B2B accounts — every report filterable. The funnels Plausible makes you pay for and can't self-host.
- **One binary, not a cluster.** No Kafka/ClickHouse/Redis, no 12-hours-debugging-self-host. `docker run` and it's up. Your data never leaves your box and never trains anyone's model.
- **Beautiful by default.** Server-rendered, instant, opinionated — looks designed, not assembled.
- **Open source (MIT), genuinely self-hostable.** Own the whole thing — no paywalled features stripped from the self-hosted edition.

## Ask it in your editor (the whole point)
Connect once — the AI you already code with reads your real analytics and answers:

**Cursor / Claude Desktop** — add to your MCP config:
```json
{ "mcpServers": { "smolanalytics": { "url": "http://localhost:8080/mcp" } } }
```
**Claude Code** — one command:
```sh
claude mcp add --transport http smolanalytics http://localhost:8080/mcp
```
**Local stdio** (try it on demo data): `{ "command": "smolanalytics", "args": ["mcp"] }`

(When a key is set, add `"headers": { "Authorization": "Bearer YOUR_KEY" }` next to the url.)

Then just ask — in the same window you write code:
```
you ▸ how's activation, and is pro converting better than free?
ai  ▸ Activation is 62% (657 of 1,051 signups reach "activate").
      Pro converts 2.4× better end-to-end — 45% signup→checkout vs 19% on free.
      The leak is activate→checkout on free (only 31% continue). Want the paths after activate?
```
The 12 tools your model gets: `overview`, `list_events`, `funnel`, `retention`, `trends` (+ breakdown), `breakdown`, `lifecycle`, `stickiness`, `paths`, `groups` (B2B accounts), `recent_events`, `user_activity` — every one filterable by property (`plan=pro`, `source=hn`, …).

## Send events (2 minutes, zero instrumentation)
Drop the snippet in — it **autocaptures pageviews + clicks instantly**, so you get real data with no manual event tagging. Add `track()` for the key moments (signup, checkout) when you want funnels.

```html
<script src="https://YOUR_HOST/sdk.js"></script>
<script>
  smolanalytics.init("YOUR_WRITE_KEY", { host: "https://YOUR_HOST" });
  // that's it — pageviews + clicks are captured automatically.
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

Even easier: paste *this* into Cursor/Claude Code and let it instrument your app —
> "Add smolanalytics: load `https://YOUR_HOST/sdk.js`, init with my key, and `track()` the key moments (signup, activate, checkout) plus `identify()` on login."

See [`examples/`](examples/) for a runnable page + curl script.

## How it compares
Honest version — we're **not** deeper than the big tools (no session replay, flags, or experiments *yet*). The bet is a different shape:

| | smolanalytics | Plausible / Fathom | Mixpanel / Amplitude | PostHog |
|---|:---:|:---:|:---:|:---:|
| Funnels · retention · paths · cohorts | ✅ | ❌ | ✅ | ✅ |
| Ask in plain English | ✅ **your AI, free** | ❌ | 💲 their AI | 💲 their AI |
| Lives in your editor (MCP) | ✅ | ❌ | ❌ | ❌ |
| Self-host | ✅ one binary | ✅ | ❌ | ⚠️ Kafka + ClickHouse cluster |
| Own your data · export | ✅ | ✅ | ⚠️ | ✅ |
| Price | free / self-host | 💲 | 💲💲💲 | free tier + 💲 |

## Deploy it (production)
One static binary, no cgo, no cluster — it runs anywhere.

```sh
# Docker (persistent event log on a volume)
docker run -p 8080:8080 -v $PWD/data:/data \
  -e SMOLANALYTICS_WRITE_KEY=$(openssl rand -hex 16) \
  -e SMOLANALYTICS_PASSWORD=$(openssl rand -hex 12) \
  ghcr.io/arjun0606/smolanalytics

# Fly.io (persistent volume + health checks via fly.toml)
fly launch --copy-config && fly deploy
```

Config (all env): `ADDR` (default `:8080`), `SMOLANALYTICS_DB` (event log path), `SMOLANALYTICS_WRITE_KEY` (require a key on ingestion + MCP), `SMOLANALYTICS_PASSWORD` (protect the dashboard with a login). Health at `/healthz`, build at `/version`. The server warns loudly if it's exposed without a password.

Manage everything from **Settings** (`/settings`): account + password, API keys, data retention, event taxonomy, exports, webhooks, threshold alerts, and an audit log.

**No lock-in:** export everything any time — `GET /v1/export?format=csv` or `?format=jsonl` (the JSONL round-trips straight back into `/v1/events`).

## What's inside
- `internal/{funnel,retention,trends,paths,engagement,groups,cohort,query}` — the deterministic analytics engine (every report, fully tested).
- `internal/store` — the `Store` interface + in-memory and durable file (append-log) backends. A Postgres/columnar backend slots in behind the same interface.
- `internal/mcp` — the MCP server (stdio + Streamable HTTP): the "ask with your own AI" layer.
- `internal/api` — the single-binary HTTP server: ingestion, SDK, dashboard, Explore, settings, auth, webhooks, alerts, audit.
- `cmd/smolanalytics` — the binary (`serve`, `demo`, `mcp`).

```sh
make demo    # seed + serve
make race    # tests with the race detector
```

## Don't want to run it? → smolanalytics Cloud
Self-hosting is free forever. But if you'd rather not run a server — or you're a **team** — the [hosted Cloud](https://smolanalytics.com) does the parts a single binary can't:

- **Zero ops** — we host an isolated instance per project; scale, backups, and uptime are ours.
- **Your whole team** — invites, roles, shared projects (the OSS is single-operator by design).
- **The brief, delivered** — your "what to fix" digest by email + Slack every morning, reliably, without you keeping a server up or wiring webhooks.
- **Scale + retention** — millions of events, longer history, no ops.

Generous free tier (100k events, runs a real product), then $19/mo. Same product, same "ask in your editor," same own-your-data — just managed. **[Start free →](https://smolanalytics.com)**

## Contributing
PRs welcome — keep it small, correct, and dependency-free. See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: [SECURITY.md](SECURITY.md).

## License
[MIT](LICENSE). Use it, fork it, host it. A managed cloud (zero-setup hosting + per-tenant isolation) is coming for those who'd rather not run it themselves.
