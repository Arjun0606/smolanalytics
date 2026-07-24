# Changelog

All notable changes are documented here. This project follows [semantic versioning](https://semver.org).

## [Unreleased]
### Added
- Core analytics engine: funnels, retention, trends (+ breakdown), segmentation, lifecycle, stickiness, paths, cohorts, B2B groups — every report filterable by property.
- MCP server (stdio + Streamable HTTP) with 73 tools + 14 built-in prompts, including `whats_notable` (the "what to fix" verdict) — point your own Claude/Cursor at it and ask in plain English.
- `smolanalytics connect` — one command to wire the MCP server into Claude Desktop / Cursor.
- Proactive verdict: biggest funnel leak, week-over-week change, retention read, and "what changed in the last 24h" anomaly detection — on the dashboard, `/v1/notable`, and a daily brief.
- Single-binary server: event ingestion, drop-in JS SDK (autocapture), server-rendered dashboard with a built-in ask bar, live events feed, user profiles, Explore, saved reports.
- Operator surface: in-app account + auth, sectioned settings, API keys, data retention, event taxonomy, CSV/JSONL export, SSRF-guarded webhooks, threshold alerts, audit log.
- Storage: durable append-only event log (fsync, atomic batches) for a single box, plus a columnar segment tier (compressed segments on local disk or S3/R2) for billions of events on flat memory.
- `docker run` image, one-line install script, cross-platform release binaries.

## [0.9.11] - 2026-07-24
### Fixed
- `flag_impact` and `survey_results`: the `/v1` reads now apply the same default production scope (dev-env events excluded) as the MCP tools, so the two surfaces agree byte-for-byte again. Added agreement-test coverage for `flag_impact`, `survey_results`, `list_sessions`, and `session_timeline` (previously only `heatmap` was pinned) so this class of drift fails the build.

## [0.9.10] - 2026-07-24
### Added
- **Session inspector** — `list_sessions` + `session_timeline` (MCP) and `GET /v1/sessions` + `GET /v1/session`: an event-based journey replay (pages, clicks with positions, rage-clicks, ms timing), reconstructed from your event log. Not pixel-perfect DOM/video replay, by design.

## [0.9.9] - 2026-07-23
### Added
- **In-product surveys** — NPS / rating / choice / text, with URL + sampling targeting and a dependency-free SDK widget. `create_survey` / `list_surveys` / `set_survey_active` / `delete_survey` / `survey_results` (MCP) + `/v1/surveys/*` (public `active`, aggregate `results`).

## [0.9.8] - 2026-07-23
### Added
- **Click heatmaps** — `heatmap` (MCP) + `GET /v1/heatmap`: a click-density grid plus the top clicked elements per page and viewport, computed at query time from `$click` autocapture.

## [0.9.7] - 2026-07-23
### Added
- **A/B testing** as "measured flags" — `flag_impact` reports per-variant conversion on a goal event (counted only after first exposure), lift vs the control arm, and 95% two-proportion significance, computed from your events. Pinned MCP==API by the agreement test.

## [0.9.6] - 2026-07-23
### Added
- **Feature flags** — boolean + multivariate, property targeting + percentage rollout, deterministic (FNV) evaluation so the SDK and the agent always agree on a user's bucket. `create_flag` / `list_flags` / `set_flag_enabled` / `delete_flag` / `evaluate_flag` (MCP), public `GET /v1/flags/evaluate`, and `smol.flag()` in the browser SDK.
- **Sequenced behavioral cohorts** — `create_sequence_cohort`: cohort users by an ordered event sequence with per-step filters and time windows.
- **Native mobile SDKs published** — Swift (SPM), Kotlin/Android (JitPack), React Native (`smolanalytics-react-native` on npm), Flutter (`smolanalytics` on pub.dev): an offline-safe persisted queue, batching, retries, sessions, lifecycle events, and a cookieless anon id.

## [0.9.3] - 2026-07-20
### Added
- **Deploy tracking** — "which commit moved the metric": `record_deploy` / `list_deploys` / `delete_deploy` / `deploy_impact` (MCP), `POST /v1/deploys` + `GET /v1/deploys?event=`, and a one-line `smolanalytics deploy` CLI marker for CI. Before/after attribution per deploy, leading with any regression; pinned MCP==API by a CI test. Correlation, not proof — the copy always says so.

## [0.7.0] - 2026-07-04
### Added
- Portfolio brief over HTTP: `GET /v1/brief` returns the morning digest (pulse + "what to look at" findings, per-product breakdown once more than one site reports) as JSON — same key auth as the other `/v1` endpoints, `?days=` window.
- `plan check --source=posthog` — run the tracking-plan drift gate against an existing PostHog project over its query API; no smolanalytics server, no migration.
- Cloud: Solo plan — 14-day full-product trial, then from $9/mo at [smolanalytics-cloud.vercel.app](https://smolanalytics-cloud.vercel.app). Self-hosting stays the free tier.
