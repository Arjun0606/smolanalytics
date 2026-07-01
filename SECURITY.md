# Security Policy

## Reporting a vulnerability
Please **do not** open a public issue for security problems.

Use GitHub's private reporting: **[Report a vulnerability](https://github.com/Arjun0606/smolanalytics/security/advisories/new)**
(the *Security* tab → *Report a vulnerability*). If you'd rather email, reach
`karjunvarma2001@gmail.com`. You'll get an acknowledgement within a few days and a fix or
mitigation as quickly as we can.

## Hardening notes for exposed deployments
smolanalytics is single-tenant and self-hosted. It's **safe by default** — `serve` binds
`127.0.0.1` (local only), and it **refuses to serve real data unauthenticated on a public
interface**. When you expose it:

- **A dashboard password is required to bind publicly.** Set `ADDR=0.0.0.0:8080` and the
  server won't start without `SMOLANALYTICS_PASSWORD` (or an in-app account) — no accidental
  open dashboard. Override with `SMOLANALYTICS_ALLOW_UNAUTHENTICATED=1` only on a trusted
  private network. (`demo` is exempt — its data is throwaway.)
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
