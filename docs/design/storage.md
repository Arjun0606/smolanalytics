# storage: one writer, two tiers (2026-07)

**Invariant the whole design hangs on: the binary IS the entire product.** The cloud
is just us running it for you; nothing is stripped or cloud-only. Every decision
below exists to keep that true.

## The shape

```
POST /v1/events ──▶ hot log (append-only JSONL, fsync per batch)
                        │ seals every 50k events (SMOLANALYTICS_SEAL_EVENTS)
                        ▼
                  immutable columnar segment (.sms: length-prefixed columns,
                        │                      compressed, CRC32-checked)
                        ▼ optional
                  blob backend: local dir, or S3/R2/Tigris via stdlib SigV4
```

A manifest (JSON in the blob backend) lists live segments with count + time range.
Reads scan only segments whose time range overlaps the query, plus the hot log —
memory stays flat no matter how much history exists (~7 bytes/event at rest).

## Why one writer, no cluster

WAL durability across machines requires a consensus protocol — terms, quorums,
split-brain defense (see Neon's safekeeper protocol for what that costs a codebase).
We deliberately have exactly ONE writer per instance, so:
- durability = fsync before ACK (an accepted event is on disk, period)
- recovery = replay one log, tolerate a torn tail
- no coordination service, no leader election, nothing to misconfigure

Scale-out is "run more instances" (the cloud does one per project), never "add nodes."

## Crash-safety rules (all enforced in code, most tested)

1. **ACK after fsync**, never before (hot log).
2. **Immutable segments** — never modified in place; erasure (GDPR) rewrites a
   segment under a fresh key and swaps the manifest atomically.
3. **Monotonic segment keys** — a sequence counter restored from the manifest at
   open. Never derived from manifest length (that reused keys after retention
   pruning and could overwrite a live segment — found and fixed 2026-07).
4. **Manifest persists before old blobs are deleted** — a failure can orphan an
   unreferenced blob (harmless), never dangle a reference.
5. **Temp-file → fsync → rename → fsync dir** for every local blob write, same
   discipline as the hot log.
6. **Recovery is conservative** — a torn hot-log tail is dropped at the record
   boundary; a corrupt segment fails its CRC and is reported, not silently skipped.

## What we traded away, knowingly

- No concurrent writers → no horizontal write scaling. Fine: one product's events
  fit one box for far longer than anyone believes (~7 B/event ≈ 140M events/GB).
- No per-event deletes in segments → GDPR erasure rewrites whole segments. Rare
  operation, so we optimize for the read path instead.
- JSON for the hot log → larger than binary, but debuggable with `cat` and
  round-trips through the export API. Segments are where compactness matters.
