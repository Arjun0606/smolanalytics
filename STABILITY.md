# Stability policy

Analytics is infrastructure: you wire it in once and it must keep working while you
ignore it. This is the contract we hold ourselves to — boring by design.

## Frozen surfaces (additive-only)

- **The event schema** — `name`, `distinct_id`, `timestamp`, `properties`. New
  optional fields may be added; existing ones never change meaning or disappear.
- **`POST /v1/events`** — anything that ingests today ingests forever.
- **`GET /v1/*` report endpoints** — response fields are additive-only; an existing
  field never changes type, meaning, or vanishes.
- **The MCP tool surface** — tools may gain optional parameters and new tools may
  appear; existing tool names, required parameters, and result fields stay.
- **On-disk formats** — the hot log and sealed segments written by any past release
  decode identically forever. Enforced by versioned fixtures in
  `internal/store/segment/testdata/` (new format ⇒ new fixture dir added; old ones
  are never touched) and a versioned manifest that fails loudly on a future version
  instead of misreading.

## Upgrades

Swap the binary. That's the whole procedure — no migrations to run, no coordinated
restarts, no cluster to drain. Downgrading within a manifest version is equally
uneventful.

## What "additive-only" means in practice

If a change would rename, remove, retype, or re-scope anything above, it doesn't
ship — it becomes a new field or a new endpoint instead. The
[agreement test](internal/api/agreement_test.go) additionally pins MCP and HTTP to
identical answers, so neither surface can drift from the other.

## Deprecations

None planned, and none will ever be silent: anything deprecated keeps working for
at least 12 months after being marked, with the marking visible in the changelog,
the docs, and a server log line.
