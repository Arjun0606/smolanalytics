# Security Policy

## Reporting a vulnerability
Please **do not** open a public issue for security problems.

Use GitHub's private reporting: **[Report a vulnerability](https://github.com/Arjun0606/smolanalytics/security/advisories/new)**
(the *Security* tab → *Report a vulnerability*). If you'd rather email, reach
`karjunvarma2001@gmail.com`. You'll get an acknowledgement within a few days and a fix or
mitigation as quickly as we can.

## Hardening notes for exposed deployments
smolanalytics is single-tenant and self-hosted. It's frictionless-by-default for local
dev; **before you put it on the public internet:**

- **Set a dashboard password.** With no `SMOLANALYTICS_PASSWORD` (or in-app account) the
  dashboard and its management endpoints are unauthenticated — fine on localhost, not on a
  public interface. The server logs a loud warning while it's unprotected.
- **Set a write key.** `SMOLANALYTICS_WRITE_KEY` (or a key created in Settings) gates event
  ingestion and the MCP endpoint so strangers can't write to your data.
- **Webhooks are SSRF-guarded.** Outbound webhook delivery refuses any URL that resolves to
  a loopback / private / link-local / cloud-metadata address (checked at dial time, so DNS
  rebinding and redirects into private space are covered too). If you *need* to hit an
  internal target, opt in with `SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS=1`.

## Design notes
- The event log, settings, and sidecar files are written with `0600` permissions.
- Session cookies are HMAC-signed with a per-instance secret (generated on first run) and
  set `HttpOnly` + `SameSite=Lax` + `Secure` (under TLS).
- Request bodies are capped (4 MB) and batches are bounded (10k events); no request input
  reaches a shell, and dashboard rendering escapes all event-supplied strings.
