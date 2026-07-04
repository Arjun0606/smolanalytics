# Changelog

All notable changes are documented here. This project follows [semantic versioning](https://semver.org).

## [Unreleased]
### Added
- Core analytics engine: funnels, retention, trends (+ breakdown), segmentation, lifecycle, stickiness, paths, cohorts, B2B groups — every report filterable by property.
- MCP server (stdio + Streamable HTTP) with 47 tools including `whats_notable` (the "what to fix" verdict) — point your own Claude/Cursor at it and ask in plain English.
- `smolanalytics connect` — one command to wire the MCP server into Claude Desktop / Cursor.
- Proactive verdict: biggest funnel leak, week-over-week change, retention read, and "what changed in the last 24h" anomaly detection — on the dashboard, `/v1/notable`, and a daily brief.
- Single-binary server: event ingestion, drop-in JS SDK (autocapture), server-rendered dashboard with a built-in ask bar, live events feed, user profiles, Explore, saved reports.
- Operator surface: in-app account + auth, sectioned settings, API keys, data retention, event taxonomy, CSV/JSONL export, SSRF-guarded webhooks, threshold alerts, audit log.
- Storage: durable append-only event log (fsync, atomic batches) for a single box, plus a columnar segment tier (compressed segments on local disk or S3/R2) for billions of events on flat memory.
- `docker run` image, one-line install script, cross-platform release binaries.

## [0.7.0] - 2026-07-04
### Added
- Portfolio brief over HTTP: `GET /v1/brief` returns the morning digest (pulse + "what to look at" findings, per-product breakdown once more than one site reports) as JSON — same key auth as the other `/v1` endpoints, `?days=` window.
- `plan check --source=posthog` — run the tracking-plan drift gate against an existing PostHog project over its query API; no smolanalytics server, no migration.
- Cloud: Solo plan — 14-day full-product trial, then from $9/mo at [smolanalytics-cloud.vercel.app](https://smolanalytics-cloud.vercel.app). Self-hosting stays the free tier.
