# the scale ceiling, measured

Every number here came off a profiler or a load test on this machine. The first version of
this file reasoned from the architecture and got the cause wrong, which is recorded at the
bottom because the mistake is instructive.

## what is actually true

| | measured |
|---|---|
| ingest | **72,000 events/sec** sustained |
| disk | **21 bytes/event** sealed (flate + columnar) |
| memory, empty | **20 MB** |
| resident, 2M events held in memory | **1,109 MB** (554 bytes/event) |
| peak heap, one dashboard render, uncapped | **4,043 MB** |
| resident after that render + GC | **1,109 MB** — every byte of the spike was reclaimed |
| same render under a 400MB soft cap | **survives**, peak 631 MB, 16s instead of 2s |

Storage and ingest are genuinely good. 21 bytes/event means a 100M-events/month tenant needs
2.1 GB/month of disk, which is nothing.

## the two problems, which are not the same problem

**1. Allocation churn during a render.** This was the big one and it is mostly fixed.

Profiling the render, not reading it, found that **47% of everything the dashboard allocated
was `query.StampFirstTouch`**. `insight.segmentBlame` chained it across eight acquisition
properties, and stamping copies every event's property map — so eight chained calls meant
eight full copies of history, 1.6M map allocations for 200k events.

Two fixes, both in. Stamping takes a property *set* and does one pass, because the properties
are independent and batching is provably identical. And `BuildFirstTouch`/`Stamp` splits the
scan from the copy: finding a user's first touch has to look at every event (the country is on
the landing pageview), but `segmentBlame` never reads an event not named `from` or `to`. So it
indexes over everything and stamps only what it reads.

**200k events: 1,265 MB → 764 MB per render. 6.3 → 3.7 KB/event.**

The dashboard's own double load went too: it called `Range` and then `query.Apply` on the
result, holding two full slices. It scans once and filters inline now.

**2. Resident memory tracks total history.** This one is real and not yet fixed.

`store.Range(time.Time{}, time.Time{})` means every event ever, and the report builders
compute over all history on purpose — which is why the funnel reads 2,376 signups next to a
30-day KPI of 1,508. That is deliberate and correct. So resident memory is O(total events),
and it cannot be fixed by narrowing the read without silently changing every number on the
dashboard. In a product whose pitch is "computed, not guessed", quietly altering a number to
save memory is the worst available trade.

At 554 bytes/event resident, the practical ceiling is roughly **450k events on 256MB** and
**900k on 512MB**.

## what changed at the edge

The binary now sets a soft memory limit from the container's own cgroup (80%, both cgroup
versions, and it defers to an explicit `GOMEMLIMIT`). Go's collector sizes the heap against
what is live, not against what the box has, so with the default it would let the heap double
and the kernel would OOM-kill the process before the collector decided it was worth running.

Under a cap, the collector spends CPU to stay under it. Measured: a render that peaked at 4 GB
uncapped peaks at 631 MB under a 400 MB limit and takes 16s instead of 2s.

**That is the trade, and it is deliberate: a slow dashboard is a complaint, an OOM-killed
container is a customer watching their analytics vanish with no error anywhere.** It does not
raise the ceiling. It changes what hitting the ceiling looks like.

## the remaining fix: roll up at seal time

For problem 2. A segment is sealed once, is immutable, and already knows its own min/max
timestamp. So at seal time, also compute and store in the manifest:

- per-day counts per event name
- per-day counts per (property, value) for the indexed properties
- distinct-user sketch per day (HLL, ~1.5KB for 2% error, or an exact set while small)
- first/last seen per distinct_id, for retention and lifecycle

Then the dashboard answers from rollups and only touches raw events for what genuinely needs
them (session timelines, the event stream, path analysis). Memory becomes O(days × cardinality)
rather than O(events). Three years of daily rollups over ~50 event names and a few thousand
property values is single-digit MB.

**Order of work**
1. Rollup struct + compute at seal, written into the segment manifest. No read-path change, so
   nothing can break yet.
2. A parity test: every dashboard number from rollups must equal the number from raw events on
   the demo fixture. This is the gate — it matches exactly or the rollup is wrong.
3. Switch dashboard KPIs and trends to rollups behind a flag.
4. Funnel and retention, which need the per-user structures.
5. Delete the flag once parity holds for a week on real data.

## what this does NOT block

Everything at current customer scale. His own instance is ~1,100 events. A tenant doing tens of
thousands of events a month is nowhere near the ceiling. This is a growth blocker, not a launch
blocker — but it decides whether a $619/month customer is servable, and that customer is the
difference between a hobby and a business.

## the mistake, kept on purpose

The first version of this file blamed `store.Range` materializing all history, and proposed
rollups as the fix. Measured, the load step costs **32 MB** for 200k events — `Range` copies
80-byte event headers and the property maps already exist once in the store, so it is nearly
free. The render was **1,265 MB**. The diagnosis was pointed at 2.5% of the problem.

Rollups are still the right fix for resident memory. But the thing actually making the box fall
over was a nested loop nobody had profiled, and it was found in one `pprof` run after weeks of
the wrong answer sitting in this file. **Profile before optimizing, including when the
architecture argument sounds airtight.**
