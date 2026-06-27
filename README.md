# smolanalytics

**Product analytics in a single Go binary. As simple as Plausible, as powerful as Mixpanel — and you just *ask* it in plain English.**

```sh
go run ./cmd/smolanalytics demo   # seeds a realistic dataset + opens a populated dashboard
```

Then open http://localhost:8080.

## Why
Simple analytics (Plausible, Fathom) can't do funnels or product analytics. Powerful analytics (Mixpanel, Amplitude, PostHog) are complex, expensive, and need a cluster to run. Nobody owns the middle: **full product-analytics depth, with a dead-simple experience, in one binary you actually own.**

- **One binary + your data.** No Kafka, no ClickHouse cluster, no SPA build step. Self-host it anywhere or use the cloud.
- **Real product analytics.** Funnels, cohort retention, trends, segmentation — deterministic and fast.
- **Ask with your OWN AI.** smolanalytics is an MCP server: connect it to your Claude / Cursor / Claude Code and ask *"why did checkout drop last week?"* in plain English. Your model reads the data through our tools — we never call a model ourselves, so there are no API keys and no inference cost on our side. The dashboard also answers common questions built-in, zero setup.
- **Beautiful by default.** Server-rendered, instant, opinionated — looks designed, not assembled.
- **Open source.** Own your data; no per-event tax.

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

## Ask it anything (MCP)
smolanalytics speaks the Model Context Protocol, so your own AI assistant reads your analytics directly:

- **Local (Claude Desktop / Cursor):** `smolanalytics mcp` runs an MCP server over stdio.
- **Remote:** point any Streamable-HTTP MCP client at `POST /mcp` on the running server — it shares the live data.

Tools exposed: `overview`, `list_events`, `funnel`, `retention`, `trends`, `breakdown`. Your model picks the right one and explains the answer.

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

Config (all env): `ADDR` (default `:8080`), `SMOLANALYTICS_DB` (event log path), `SMOLANALYTICS_WRITE_KEY` (require a key on ingestion). Health at `/healthz`, build at `/version`.

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
