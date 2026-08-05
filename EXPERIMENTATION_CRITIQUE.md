Verified a sample of the repo claims before critiquing: `internal/query/sampler.go` is indeed untracked (`git ls-files --error-unmatch` fails), `event.Event` has no ingest timestamp (`/Users/arjun/smolanalytics/internal/event/event.go:11-17`), `Measure` does call `time.Now()` inside the pure path and picks control via `sort.Strings(variants)[0]` (`internal/flag/measure.go:64-120`), and `api.go:373-378` has no `/health` route. The verified-defect cells are real. That is the strongest part of both documents.

**What's good, briefly:** Matrix §8 (the unflattering summary) and §9's "Must be true first" gating are rare discipline — §9 refuses to let a claim exist before the product earns it. Build-spec §0 ("do NOT rebuild") is the most useful page for an implementer in either doc. Items 1, 2, 3, 9, 10 are unarguable: all S, all fixing verified wrong answers, no new math. Now the flaws.

---

## 1. Capability areas a serious comparison must include and this one omits

**a) Ad-blocker survivability / first-party ingest.** The single biggest omission. PostHog ships reverse-proxy docs and a managed proxy on paid tiers *because* `posthog.com` is on every blocklist; Mixpanel's own FAQ (which the matrix quotes in the agreement row) blames ad blockers for discrepancies. A self-hosted binary on your own domain is structurally immune. The matrix cites the competitor's excuse and never converts it into the obvious advantage. There is no proxy row, no first-party-domain row, no measured blocked-traffic delta.

**b) Consent / privacy operating mode.** `sdk.js:19,143,632` implements a cookieless mode with an explicit "no banner needed" comment. It appears nowhere in the matrix. Also unscored: DNT/GPC handling, IP anonymization, PII scrubbing on properties, data residency. §6 reduces the entire privacy axis to one row saying "no SOC 2" — in a document whose §9 leans on privacy as a pillar.

**c) Projects / environments.** `set_project` exists as an MCP tool and `internal/settings/settings.go` references projects, yet §6 says "single-tenant, single-user" and §4 repeatedly reasons about "dev-env pageviews" and "production scope." Environments are a scored capability at both incumbents (PostHog withholds multi-env flags from OSS — mentioned once in a list, never rowed).

**d) Query performance, concurrency, and scale ceiling.** There is a caching row but no latency row, no dataset ceiling, no memory profile. §9.6 admits the number doesn't exist ("publish a measured events/month ceiling… and hold both") while the matrix still scores PostHog's query-perf mega-issue as their weakness. "Every request materialises all history — unbounded per-request memory" is a scored liability buried inside an advantage cell.

**e) Backup, restore, upgrade, and crash recovery.** For a product whose pitch is "one binary, one file," the first ops question is "what happens when the box dies," and the second is "what happens when I upgrade." Neither is a row. Single-writer means a data-loss window exists and its size is unstated.

**f) Revenue analytics / attribution.** PostHog shipped a revenue product (Stripe ingestion) and ingests ad spend. The matrix mentions "no revenue/LTV/MRR modelling" in a §8 bullet and never rows it, so a reader comparing on money-metrics finds nothing.

**g) Time-to-first-event / onboarding DX.** For the stated ICP (solo builders) this is more predictive of adoption than any row in the document, and it's absent from both docs.

**h) Mobile beyond SDK existence** — app-version breakdowns, session stitching across cold starts, crash-free sessions. Compressed to "manual instrumentation only."

---

## 2. Claims asserted without evidence, or likely wrong

**The load-bearing ones:**

- **§9.1 "PostHog cannot say this — it has four open bugs."** Open GitHub issues are proof someone filed something, not proof a claim is false. Four issues in a repo of tens of thousands is thin, and they get closed — at which point the headline claim reads as stale FUD. Worse: §4 and §8 list *seven verified* self-disagreements in smolanalytics (sampler divergence, XAU, timezone, session 404, two week definitions, funnel tie order, ingest idempotency). On the evidence in this document, smolanalytics currently has more determinism defects than PostHog. Neither doc says that sentence out loud, and it's the most important sentence in either.

- **§9.9 "No vendor in the bundle publishes measured false-positive rate or CI coverage."** Absence of evidence stated as evidence of absence, across an industry, and it is the entire basis for build-spec item 15 — the doc's self-declared "highest-leverage marketing artifact." Statsig has published simulation and methodology work publicly. Scope this to "we could not find" or the artifact's premise is a claim you can be corrected on in public.

- **The agreement row and the "verified live disagreements" row are in the same table, four rows apart, and contradict each other.** "CI-enforced byte-for-byte" and "the flagship claim failing on its own instance" are both scored as facts. That isn't nuance, it's an unreconciled contradiction the reader is left to resolve.

**Pricing (all quoted to the cent, none dated):**

- **"5M anonymous ≈ $152.90/mo"** and **"$0.28 per 1,000 events"** — point estimates on tiered curves, presented as prices. Anything to the cent needs a per-cell retrieval date or it becomes a liability the day it changes.
- **"Vendr median ACV $38,717"** is flagged *likely* in a parenthetical and then sits in a pricing table reading as fact, next to self-serve list prices. Enterprise ACV is not what your ICP would pay; the contrast is inflated.
- **No vendor cell in the entire matrix carries an "as of" date**, in a market the document itself says moved in the last 12 months (PostHog Code May 2026, Mixpanel Onboarding Agent May 2026, Experimentation 2.0 Jun 2026). This makes the matrix unmaintainable and guarantees it gets quoted after it's false.

**Likely wrong or overstated:**

- **"Lifecycle: Mixpanel ❌ (no equivalent named report)"** — absence of a *name* is not absence of the capability; Retention→Frequency plus their Users reports cover the same question. Scoring "ahead of both" off a naming difference.
- **"Heatmaps ahead of Mixpanel (no replay prerequisite)"** — the buyer experiences "Mixpanel's heatmap links to a session I can watch." You've scored the consequence of a missing feature as an advantage.
- **"Retention: arguably ahead on honesty (null vs fake 0%)"** — no evidence given that either incumbent renders fake 0% for unobservable periods. Asserted.
- **"Alerts: ahead on count and price"** — unlimited alerts of the one shape that doesn't matter is not "ahead."
- **"No formula/derived metrics anywhere in smolanalytics"** contradicts §4, which gives `run_sql` aggregates, GROUP BY and HAVING. Ratios across series are computable today; "nowhere" is wrong.
- **Mixpanel's Nov 2025 breach as a comparison row** is not a capability, and the security row's own caveat ("an argument, not an attestation") applies with more force to a competitor's incident than to your own architecture.

**In the build spec:**

- **§4: "under continuous peeking the true FPR is 20–25%."** Depends entirely on peek cadence and horizon — the matrix itself gives 10% at 5–7 looks. The spec then hardcodes this into a *test assertion* (`assert fixed FPR > 0.15`). Encoding a marketing number as a test is backwards; change the peek schedule and you'll "fix" the test by moving the threshold.
- **§6: `TestSampleSizeMatchesLehrRule` within 2%.** Lehr's 16/Δ² is itself a ~2-3% approximation. You're asserting agreement between two approximations at a tolerance tighter than one of them. Flaky on day one.
- **§12: `TestCUPEDReducesVarianceOnCorrelatedCovariate` ≈36% ± 3pp at ρ=0.6.** That's just 1−ρ² restated — it tests the identity, not the implementation, and the ±3pp tolerance is meaningless without a stated n.

---

## 3. Underspecified items (an implementer still has to make a significant unstated call)

**Item 3 — the biggest hole in the spec.** "Resolve `days` → absolute instants at the API/MCP boundary" leaves undefined: resolved against which clock, in which timezone, and what `to` is when a caller passes `days=7` at 14:23. If `to = now`, every recomputation yields a different receipt and item 14 is dead on arrival. The spec demands `TestReceiptIsClockIndependent` while leaving undefined the only input that makes it clock-dependent. Needs a sealed-window rule (e.g. `to = floor(now, hour)` or `to = watermark`) stated explicitly.

**Item 14 — three unstated decisions.** (a) `InputDigest = sha256 of sorted event IDs in scope` is an O(n) hash of every event on every report, on a product whose named liability is "every request materialises all history." No budget, no incremental/Merkle-over-segments alternative. (b) No policy for events *removed* behind a receipt: GDPR erasure and `RETAIN_DAYS` prune are both shipped features, so an old receipt becomes permanently unverifiable and `verify` prints a scary diff for a compliance action the user deliberately took. (c) Does an amendment (item 5) invalidate prior receipts? Unspecified interaction with the item it depends on.

**Item 5 — canonical JSON is hand-waved.** "Keys sorted, use a small canonical-encode helper." Undefined: float canonicalization (0.05 vs 5e-2), `time.Time` serialization precision (a value read from disk and one just constructed differ at nanosecond precision — this will bite), omitempty on zero-valued optional pointers, and struct-field vs map-key ordering. `TestPlanHashIsStableAcrossMarshalling` as written will pass and production will drift.

**Item 4 — `n_tune = 5000` has no basis.** `k(N)` is minimized near N\*, so a wrong default systematically widens everyone's intervals. Item 6 computes `n_planned`, which is the obvious value for N\*, and the spec never connects them. Also unstated: behavior when N far exceeds N\* (k grows again), whether the always-valid p bisection is monotone in α over `[1e-12, 1]` (assumed, unproven — non-monotonicity gives deterministic garbage, which is worse than nondeterministic), and the UX when a previously-significant result becomes non-significant as data arrives. Users will file that as a bug.

**Item 1 introduces the exact defect it exists to fix.** `/health` is unscoped (to match MCP), `/measure` is scoped via `query.Apply`. The banner and the table it sits above are computed on different event sets, on the same screen. The spec calls both scoping choices "right" and never notices they now collide.

**Item 7 — δ=0 on `$exception`.** A non-inferiority test at margin zero is a one-sided superiority test; it can essentially never return PASS at finite n. The suggested default guardrail is permanently INCONCLUSIVE.

**Item 9 — `sessionStorage` is per-tab.** Two tabs, two exposures, and the copy still says "per visitor per session." You've moved the lie, not removed it. Needs localStorage plus a session-id stamp, or the copy changes.

**Item 11 — is BH applied to the always-valid p-values from item 4?** Unstated, and the FDR guarantee under continuous monitoring is conditional. Also unstated whether the family accumulates across peeks (it doesn't, and that should be said in the payload).

**Item 12 does not compose with item 4.** The confidence sequence is derived for a difference of proportions; CUPED changes the variance input. The spec treats them as orthogonal configurations. They are not, and nothing says which wins.

**Item 13 check 5 — `eligible_users` is undefined and largely unknowable.** Eligibility depends on targeting rules *and* whether the code path ran. The headline widget ("only 9% exposed, you need 123× the sample") is computed against an undefined denominator.

**Item 16 — which is the cluster, `bucket_id` or `DistinctID`?** The spec says randomisation unit = `bucket_id`, but `Measure` keys everything on `DistinctID` and §0 says `bucket_id` is device-scoped and deliberately survives `identify()`. That is the entire estimand and it is unresolved.

**Item 17 — folding the layer into the salt re-randomises every already-exposed user.** The spec makes `Layer` part of the immutable plan (correct) but never states the footgun or forbids assigning a layer to a running flag. It also breaks item 8's conformance fixture, which pins `salt = "variant:"+key`.

**Effort estimates.** Item 15 at "M" for 16,000 simulated experiments through the real (unoptimized, pure) `Measure` path is optimistic by a wide margin — which is why it's gated behind `-tags slow`, which means it won't run, which means the published table goes stale. And the cross-cutting "thread `*time.Location` through every bucket function" is listed as **S** in a footnote table: it touches trends, retention, engagement, ask, funnel, paths, sessions, web, every agreement test's expected values, and it silently changes numbers users have already seen. It is the largest single item in the document.

---

## 4. The single highest-leverage thing missing

**A "show me the rows behind this number" drill — event-level provenance from any reported figure back to the exact events that produced it.**

The matrix names it as a requirement in §9.4 ("a 'rows behind this number' drill … must be callable") and the build spec never turns it into an item. It is the only thing in scope that makes *every existing report* more trustworthy instead of adding a new one; it is nearly free for an engine that already recomputes from raw and holds no rollups; it is structurally impossible for PostHog and Mixpanel at their scale; and it is exactly what an agent needs when the user asks "why is this number 3,431?" Receipts prove two people got the same number. Provenance is the only feature that lets one person find out why.

**The larger missing thing, if scope is negotiable:** neither document contains a kill gate for the experimentation bet. There is no stated traffic threshold below which this entire spec is dead weight, and no count of how many current users clear it. The ICP is solo builders and indie devs. A solo builder at 500 MAU cannot complete a fixed-horizon test on a 5% lift, let alone need CUPED, layers, or ratio metrics with delta-method variance. Meanwhile the matrix's own ranked list of what a user hits first is: one dashboard, no replay, no error tracking, no email — and experimentation is **#7**. This spec is nineteen items deep on the seventh-ranked gap, with no argument for why.

---

## 5. Traps

**Item 14 (receipts) is the most likely to make the product worse.** Unbudgeted O(n) hashing on every request; goes red on GDPR erasure and retention pruning, both shipped features; and with `MaxEventTS` + `InputDigest` in the hash, a receipt from ten minutes ago never verifies on an instance receiving live traffic — i.e. always. The demo is "paste it in, it matches." The reality is "it never matches, here's a reason," which teaches users the numbers are unstable. That is the precise inverse of the intent. Not shippable without a sealed-window concept that isn't in the spec.

**Item 15 (A/A harness) publishes your own worst number.** The headline contrast row is `fixed (peeked ×20): FPR 0.241` — for a mode *you ship*. Anyone quoting the table quotes that. And it's gated behind `-tags slow`, so it will silently stop running and the committed JSON becomes a stale trust artifact — the worst possible failure mode for a trust artifact. If the fixed mode is that bad, delete it rather than documenting how bad it is.

**Items 12, 16, 19 are statistical machinery for traffic the ICP does not have.** Item 19's empirical-Bayes shrinkage needs the org's own historical effect distribution — the spec's own example says "across your last 11 experiments." A solo builder has zero. That code path can never fire for the target user. M/L effort each, zero reachable value.

**Item 17 (layers + holdouts)** matters when you run many concurrent experiments. The ICP runs zero to one. It also introduces a silent re-randomisation footgun and breaks the item-8 fixture. High effort, negative trust risk.

**Item 5 (plan lock) directly contradicts your best differentiator.** `define_event` — retroactive event definition, which the matrix calls "genuinely unique" and the answer to the #1 churn cause at both incumbents — exists to say "your taxonomy is fixable after the fact." Item 5 says "your goal event is frozen at start, changing it means you chose your result after seeing the data." Both ship, on the same screen, for the same user. Neither document notices. For a solo builder this reads as the tool refusing to let them fix a typo.

**Item 8's public conformance fixture is a one-way door.** 500 pinned pairs published as a spec means you can never change the hash, including to fix a bias found later. `HashVersion` lives on the *experiment*, so there is no migration story for the fixture itself or for flags created before versioning existed. LaunchDarkly's cited case is the argument *against* publishing without that story, not for publishing.

**Net:** ship items 1, 2, 3, 9, 10 and the sampler/XAU/tie-break cross-cutting fixes. That's roughly a week, it converts seven verified wrong answers into right ones, and it's the only part of this spec that the ICP will ever touch. Everything from item 11 down is a different product for a different customer, and the document never argues for that customer.