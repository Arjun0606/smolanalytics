# Contributing to smolanalytics

Thanks for helping out. The bar is simple: keep it **small, correct, and dependency-free**.

## Principles
- **One binary, zero dependencies.** The whole thing builds with the Go standard library. A PR that adds a third-party dependency needs a strong reason.
- **Deterministic engine.** The analytics packages (`funnel`, `retention`, `trends`, `paths`, `engagement`, `groups`, `cohort`, `query`) must be pure and reproducible — same events in, same numbers out. No clocks or randomness in the math.
- **Tests for behavior.** Every analytics change ships with a test that pins the numbers. Bugs ship with a regression test.

## Getting set up
```sh
git clone https://github.com/Arjun0606/smolanalytics
cd smolanalytics
make demo        # seeds data + serves http://localhost:8080
make test        # run the suite
```

## Before you open a PR
```sh
make fmt         # gofmt
make vet         # go vet
make race        # tests with the race detector
```
CI runs exactly these. If they're green locally, they're green on CI.

## Project layout
- `cmd/smolanalytics` — the CLI (`serve`, `demo`, `mcp`, `connect`).
- `internal/api` — HTTP surface, the dashboard, settings, auth, webhooks, alerts.
- `internal/mcp` — the Model Context Protocol server (the "ask with your AI" layer).
- `internal/{funnel,retention,trends,paths,engagement,groups,cohort,query}` — the analytics engine.
- `internal/store` — the `Store` interface + backends: in-memory, the durable append-log, and the columnar segment tier (`store/segment` + `store/blob`, local disk or S3/R2) for scale.

## Where to start
Honest version: this is a one-person repo, so there's no curated backlog of starter
issues yet. The clean extension seams — where a PR slots in behind an existing
interface without touching the rest — are:
- a new **report type** (see any package under `internal/`, e.g. `funnel`, `paths`)
- a new **MCP tool** (`internal/mcp/tools.go` + `actions.go` — schema + handler + test)
- a new **`Store` backend** (e.g. Postgres) behind `internal/store.Store`
- a new **assistant target** for `smolanalytics connect` (`cmd/smolanalytics/connect.go`)

Open a Discussion first if you're unsure the shape fits — saves you a rewrite.

## Reporting bugs / security
Open an issue, or for security see [SECURITY.md](SECURITY.md).
