# Security Policy

## Reporting a vulnerability
Please **do not** open a public issue for security problems.

Email **karjunvarma2001@gmail.com** with details and steps to reproduce. You'll get
an acknowledgement within a few days and a fix or mitigation as quickly as we can.

## Scope notes
- smolanalytics is single-tenant and self-hosted. The dashboard is unauthenticated
  by default for local dev; set `SMOLANALYTICS_PASSWORD` (or create an in-app
  account) before exposing it. The server logs a warning when it's unprotected.
- Event ingestion and the MCP endpoint are protected by a write key when one is
  configured (`SMOLANALYTICS_WRITE_KEY` or a key created in Settings).
- The event log and settings files are written with `0600` permissions.
