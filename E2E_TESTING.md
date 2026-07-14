# How to end-to-end test smolanalytics

Two levels: a 5-minute smoke you can do anytime, and the full journey that catches the
bugs only realistic data reveals (the channel-attribution bug in this repo hid for
weeks because synthetic test data had clean `source` props; a real app's autocapture
carries `referrer`, not `source`).

## Level 1 — 5-minute smoke (no setup)

The live demo instance is a full testbed.

1. Open <https://smolanalytics-demo.fly.dev> — click the ask bar, ask "what should I
   fix?", "where do people drop off?", "which channel converts best?". Read the answers
   critically: is the number right, is it the metric you asked for, is anything
   collapsed or missing?
2. Scroll the deck: every report card (pages, referrers, geo, devices, funnel,
   retention, live, events) should show real, non-empty data that reconciles.
3. Connect it to your own editor: copy the command from the dashboard's "connect your
   agent" sheet into Claude Code / Cursor, then ask it your numbers over MCP. That is
   the whole product loop.

## Level 2 — the full journey (~15 min, catches the real bugs)

This is the only way to catch bugs that need realistic data. Do it on every release
that touches the engine, the SDK, or instrumentation.

1. **Run a fresh instance:** `docker run -p 8080:8080 ghcr.io/arjun0606/smolanalytics`
   (or `go run ./cmd/smolanalytics serve`). This is a brand-new user's project — check
   the first-run dashboard guides you to instrument (not a sad blank screen).
2. **Build/point a throwaway app at it** (a small Next.js app with signup → a core
   action → checkout). Follow `smolanalytics.com/install.md` literally as the agent
   would: add the base script, connect MCP, run `propose_instrumentation`, apply the
   `track()` calls, `set_tracking_plan`, `verify_instrumentation` (expect WIRED).
3. **Generate realistic traffic** — the key word is *realistic*: many visitors, MIXED
   referrers (reddit, HN, google, direct — some with no referrer), a real funnel where
   most people drop, custom events that DON'T carry every property (autocapture
   pageviews carry `referrer`; your signup event carries neither `source` nor
   `device`). Clean uniform synthetic data hides real bugs — vary it.
4. **`verify_instrumentation` again** → expect FIRING.
5. **Ask the founder questions** and recompute the answer by hand against the raw
   `/v1/export`: "how many signups this week?", "where do people drop off?", "which
   channel converts best?", "how's the reddit traffic doing?". Every number must be
   right AND answer the question asked (not a collapsed/substituted metric).
6. **Load the dashboard** as that founder — does every card show the real segments
   (all the referrers, not just "direct"), does the funnel match, is anything empty or
   misleading?

The gold standard for any report claim: pull the raw events from `/v1/export` and
recompute the number independently. If the endpoint disagrees with your recompute,
that's the bug — trust the recompute.

## Level 3 — the claim-vs-code gate (before shipping ANY marketing copy)

Every feature bullet on a marketing page (pricing, /features, /for, /vs, docs) and
every MCP tool description MUST point to a real function. A claim with no code behind
it is a launch-blocker — the product's whole pitch is "provably correct, no
fabrication," so a fabricated *feature claim* contradicts the brand.

The check (run it whenever pricing/features copy changes):

1. List every concrete capability the copy claims.
2. For each, grep BOTH repos for the implementation — `~/smolanalytics/internal` and
   `~/smolanalytics/internal` (engine) AND `~/smolanalytics-cloud/app` + `lib` (cloud).
   Search the engine too: features like the audit log live in `internal/audit/`, not
   the cloud repo.
3. If there's copy but no code → the claim is false; fix the copy or build the feature.
4. Watch for *mis-gating*: a feature that's real but universal (audit log, exports, the
   verdict, ask-in-editor) must NOT be listed as a paid-tier differentiator — every
   plan and the free self-host get it. Per-plan gating lives in `lib/provision.ts`
   (only ram/disk/retain differ per plan today), so most "features" are universal.
5. Keep ONE source of truth for pricing copy. `components/PricingCards.tsx` is what
   renders; do not keep a second `feats` list in `lib/plans.ts` that can diverge (that
   divergence is exactly how a wrong claim shipped once — the fix went to the unrendered
   list).

This gate would have caught both the "opens a PR" (no such feature) and the
Business-exclusive "audit log" (real but universal) claims at write-time.
