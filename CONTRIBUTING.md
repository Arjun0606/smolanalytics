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
- `cmd/smolanalytics` — the CLI (`serve`, `demo`, `mcp`).
- `internal/api` — HTTP surface, the dashboard, settings, auth, webhooks, alerts.
- `internal/mcp` — the Model Context Protocol server (the "ask with your AI" layer).
- `internal/{funnel,retention,trends,paths,engagement,groups,cohort,query}` — the analytics engine.
- `internal/store` — the `Store` interface + the in-memory and file (append-log) backends.

## Good first issues
Look for the `good first issue` label. New report types, new MCP tools, and new store backends (e.g. a Postgres `Store`) are all welcome — they slot in behind existing interfaces without touching the rest.

## Reporting bugs / security
Open an issue, or for security see [SECURITY.md](SECURITY.md).
