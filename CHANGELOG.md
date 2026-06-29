# Changelog

All notable changes are documented here. This project follows [semantic versioning](https://semver.org).

## [Unreleased]
### Added
- Core analytics engine: funnels, retention, trends (+ breakdown), segmentation, lifecycle, stickiness, paths, cohorts, B2B groups — every report filterable by property.
- MCP server (stdio + Streamable HTTP) with 12 tools — point your own Claude/Cursor at it and ask in plain English.
- Single-binary server: event ingestion, drop-in JS SDK, server-rendered dashboard with a built-in ask bar, live events feed, user profiles, Explore, saved reports.
- Operator surface: in-app account + auth, sectioned settings, API keys, data retention, event taxonomy, CSV/JSONL export, webhooks, threshold alerts, audit log.
- Durable append-only event log (fsync, atomic batches), in-memory store for the demo.
- `docker run` image, one-line install script, cross-platform release binaries.
