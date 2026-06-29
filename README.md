# smolanalytics

**Product analytics that lives in your editor. You don't build reports — you ask the AI you're already coding with. Free, self-hosted, one binary, your data.**

You ship a feature in Cursor or Claude Code, then ask *"did activation improve this week?"* right there. Your model answers from your real data over MCP. We run no model, so it costs you nothing.

```sh
go run ./cmd/smolanalytics demo   # seeds a realistic dataset + opens a populated dashboard
```

Then open http://localhost:8080.

## Why this exists
Every analytics tool now has an AI assistant — but it's bolted *inside their app*, you pay for it, and you still leave your editor to use it. smolanalytics flips it: the analytics comes to where you already work, answered by the model you already pay for.

- **Ask in your editor, for free.** It's an MCP server — connect it to Claude / Cursor / Claude Code and ask in plain English. Your model does the reasoning, so there are no API keys and no per-question AI tax. The dashboard also answers common questions built-in, zero setup.
- **Real product analytics.** Funnels, retention, trends, segmentation, lifecycle, stickiness, paths, cohorts, B2B accounts — deterministic and fast, every report filterable.
- **One binary + your data.** No Kafka, no ClickHouse cluster, no SPA build step. `docker run` it anywhere; export any time. No per-event surprise bills.
- **Beautiful by default.** Server-rendered, instant, opinionated — looks designed, not assembled.
- **Open source.** Own the whole thing.

## Send events (2 minutes)
Drop the SDK in your app — it batches, persists an anonymous id, and flushes on unload:

```html
<script src="https://YOUR_HOST/sdk.js"></script>
<script>
  smolanalytics.init("YOUR_WRITE_KEY", { host: "https://YOUR_HOST" });
  smolanalytics.track("signup", { plan: "pro", source: "hacker news" });
  // later, when they log in:
  smolanalytics.identify("user_123", { email: "a@b.com" });
</script>
```

Or POST directly (any language) — single event or a batch:

```sh
curl -X POST https://YOUR_HOST/v1/events \
  -H "Authorization: Bearer YOUR_WRITE_KEY" \
  -d '{"name":"signup","distinct_id":"user_123","properties":{"plan":"pro"}}'
```

Set `SMOLANALYTICS_WRITE_KEY` to require the key (production); leave it unset for local dev. CORS is open so the browser SDK works from any origin.

## Ask it in your editor (the whole point)
smolanalytics is an MCP server, so the AI you already code with reads your real analytics and answers — no report-building, no separate tab, no AI bill. Connect once:

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
Your model picks the right reports and explains the answer. The 12 tools it has: `overview`, `list_events`, `funnel`, `retention`, `trends` (+ breakdown), `breakdown`, `lifecycle`, `stickiness`, `paths`, `groups` (B2B accounts), `recent_events`, `user_activity` — every one filterable by property (plan=pro, source=hn, …). The dashboard also has a built-in ask bar for common questions, zero setup.

## Deploy it (production)
One static binary, no cgo, no cluster — it runs anywhere.

```sh
# Docker (persistent event log on a volume)
docker build -t smolanalytics .
docker run -p 8080:8080 -v $PWD/data:/data \
  -e SMOLANALYTICS_WRITE_KEY=$(openssl rand -hex 16) smolanalytics

# Fly.io (one command, persistent volume + health checks via fly.toml)
fly launch --copy-config && fly deploy
fly secrets set SMOLANALYTICS_WRITE_KEY=$(openssl rand -hex 16)
```

Config (all env): `ADDR` (default `:8080`), `SMOLANALYTICS_DB` (event log path), `SMOLANALYTICS_WRITE_KEY` (require a key on ingestion), `SMOLANALYTICS_PASSWORD` (protect the dashboard with a login). Health at `/healthz`, build at `/version`.

Manage everything from **Settings** (`/settings`): project name, create/revoke API keys, the install snippet, data export, and a danger-zone reset. Set a password and the dashboard requires login; ingestion and MCP keep their own key auth so they never break.

**No lock-in:** export everything any time — `GET /v1/export?format=csv` or `?format=jsonl` (the JSONL round-trips straight back into `/v1/events`). Take your data to a warehouse or another tool whenever you want.

## What's here today
- `internal/funnel` — deterministic ordered conversion funnels (conversion windows, drop-off).
- `internal/retention` — cohort retention grids.
- `internal/trends` — daily time-series (count / unique users).
- `internal/query` — filter + breakdown (segmentation) over any property.
- `internal/store` — storage interface + in-memory backend (DuckDB backend next, same interface).
- `internal/mcp` — MCP server (stdio + Streamable HTTP) exposing the engine to your own AI.
- `internal/api` — single-binary HTTP server: event ingestion (`POST /v1/events`), the dashboard, and `POST /mcp`.
- `cmd/smolanalytics` — the binary (`demo`, `serve`, `mcp`).

Early and moving fast. Run `go test ./...` — the analytics engine is fully tested.
