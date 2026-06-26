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
- **Ask, don't build.** Type a question (*"why did checkout drop last week?"*) and get the answer + the chart, instead of clicking around for an afternoon.
- **Beautiful by default.** Server-rendered, instant, opinionated — looks designed, not assembled.
- **Open source.** Own your data; no per-event tax.

## What's here today
- `internal/funnel` — deterministic ordered conversion funnels (conversion windows, drop-off).
- `internal/retention` — cohort retention grids.
- `internal/trends` — daily time-series (count / unique users).
- `internal/store` — storage interface + in-memory backend (DuckDB backend next, same interface).
- `internal/api` — single-binary HTTP server: event ingestion (`POST /v1/events`) + the server-rendered dashboard.
- `cmd/smolanalytics` — the binary (`demo`, `serve`).

Early and moving fast. Run `go test ./...` — the analytics engine is fully tested.
