# the scale ceiling, measured

Written after a load test, not from the architecture doc. Every number here came off a real
run on the same backend a Fly tenant uses (`scale backend — hot log + columnar segments`,
confirmed in the server log).

## what is actually true

| | measured |
|---|---|
| ingest | **72,000 events/sec** sustained, 2M events in 28s |
| disk | **21 bytes/event** sealed (flate + columnar) |
| memory, empty | **20 MB** |
| memory, 2M events, at rest | **1,883 MB** |
| memory, after ONE funnel query | **3,856 MB** |
| query latency at 2M events | funnel 6.0s · retention 5.8s · breakdown 6.4s · paths 5.9s |

Storage and ingest are genuinely good. 21 bytes/event means a 100M-events/month tenant needs
2.1 GB/month of disk, which is nothing. **Memory is the entire ceiling.**

Practical limit today: **~250k events on a 256MB box, ~500k on 512MB.** Pro's quota is 2M
events/month and Scale's is 5M. The quotas are throughput; the ceiling is total resident
history, and they are about 10x apart.

## why

Not the storage layer. `segment.Scan` streams properly and skips segments outside the time
range — that part is right.

The read path is the problem, in two compounding ways:

1. **`store.Range(time.Time{}, time.Time{})`** — zero bounds means every event ever. It is
   called on the dashboard, ask, explore and cohort paths. `Range` materialises: it appends
   every scanned event into one slice.
2. **The report builders compute over all history on purpose.** `funnel.ComputeOpts(evs, ...)`
   takes no window — which is why the funnel reads 2,376 signups next to a 30-day KPI of
   1,508. That is deliberate and correct behaviour.

So resident memory tracks TOTAL HISTORY, not the query window, and it cannot be fixed by
narrowing the read without silently changing every number on the dashboard. That is why this
file exists instead of a quick patch: in a product whose pitch is "computed, not guessed",
quietly altering a number to save memory is the worst available trade.

## the fix: roll up at seal time

The standard columnar answer, and it fits what is already here.

A segment is sealed once, is immutable, and already knows its own min/max timestamp. So at
seal time, also compute and store in the manifest:

- per-day counts per event name
- per-day counts per (property, value) for the indexed properties
- distinct-user sketch per day (HLL, ~1.5KB for 2% error, or an exact set while small)
- first/last seen per distinct_id, for retention and lifecycle

Then the dashboard answers from rollups and only touches raw events for the things that
genuinely need them (session timelines, the event stream, path analysis). Memory becomes
O(days × cardinality) rather than O(events).

Sizing check: 3 years of daily rollups over ~50 event names and a few thousand property
values is single-digit MB. That is the whole point.

**Order of work**
1. Rollup struct + compute at seal, written into the segment manifest. No read-path change,
   so nothing can break yet.
2. A parity test: every dashboard number computed from rollups must equal the number computed
   from raw events, on the demo fixture. This is the gate — it either matches exactly or the
   rollup is wrong.
3. Switch the dashboard KPIs and trends to rollups behind a flag.
4. Funnel and retention, which need the per-user structures.
5. Delete the flag once parity holds for a week on real data.

Until step 5, the honest position is the one in lib/plans.ts: do not sell a high-volume
tenant onto this.

## what this does NOT block

Everything at current customer scale. His own instance is ~1,100 events. A tenant doing tens
of thousands of events a month is nowhere near the ceiling. This is a growth blocker, not a
launch blocker — but it is the thing that decides whether a $619/month customer is servable,
and that customer is the difference between a hobby and a business.
