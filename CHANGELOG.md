# Changelog

All notable changes are documented here. This project follows [semantic versioning](https://semver.org).

## [Unreleased]
### Added
- Core analytics engine: funnels, retention, trends (+ breakdown), segmentation, lifecycle, stickiness, paths, cohorts, B2B groups — every report filterable by property.
- MCP server (stdio + Streamable HTTP) with 13 tools including `whats_notable` (the "what to fix" verdict) — point your own Claude/Cursor at it and ask in plain English.
- `smolanalytics connect` — one command to wire the MCP server into Claude Desktop / Cursor.
- Proactive verdict: biggest funnel leak, week-over-week change, retention read, and "what changed in the last 24h" anomaly detection — on the dashboard, `/v1/notable`, and a daily brief.
- Single-binary server: event ingestion, drop-in JS SDK (autocapture), server-rendered dashboard with a built-in ask bar, live events feed, user profiles, Explore, saved reports.
- Operator surface: in-app account + auth, sectioned settings, API keys, data retention, event taxonomy, CSV/JSONL export, SSRF-guarded webhooks, threshold alerts, audit log.
- Storage: durable append-only event log (fsync, atomic batches) for a single box, plus a columnar segment tier (compressed segments on local disk or S3/R2) for billions of events on flat memory.
- `docker run` image, one-line install script, cross-platform release binaries.
