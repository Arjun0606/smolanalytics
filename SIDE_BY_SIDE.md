# feature-by-feature: smolanalytics vs PostHog vs Mixpanel

Verified 2026-08-03 against vendor docs and pricing pages. Every claim here is checkable, and
the ones that failed a second-pass refutation check were removed rather than softened.

**Legend:** ✅ included · 💰 costs extra · 🏢 Enterprise only · ⚠️ partial · ❌ absent

---

## the one-paragraph read

We are **ahead** on: self-hosting the whole product, experiments without an Enterprise gate,
surveys, public dashboards, groups at no surcharge, unlimited alerts, native mobile SDKs, and
instrumentation that verifies itself. We are **behind** on: error tracking,
warehouse sync, session replay, and price below 2M events. We are **level** on core reports and
on having an MCP at all — which stopped being a differentiator this week.

---

## 1. core reports

| | us | PostHog | Mixpanel |
|---|---|---|---|
| funnels (+ breakdown, exclusions, windows) | ✅ | ✅ | ✅ |
| retention | ✅ | ✅ | ✅ |
| paths / flows | ✅ | ⚠️ advanced paths **not in self-host** | ✅ |
| trends + breakdowns | ✅ | ✅ | ✅ |
| lifecycle | ✅ | ❌ **not in self-host** | ⚠️ |
| stickiness (DAU/WAU/MAU) | ✅ | ✅ | ✅ |
| cohorts | ✅ | ✅ | 💰 **not on Free** |
| sequence cohorts | ✅ | ✅ | ✅ |
| groups / B2B accounts | ✅ **included** | 💰 | 💰 **+40% on the bill** |
| correlation analysis | ❌ | ❌ not in self-host | 🏢 "Signal" |
| **SQL / arbitrary query** | ✅ `run_sql` **(shipped v0.22.0)** | ✅ HogQL + 33 MCP query tools | ✅ |

**Mixpanel gates cohorts on Free and charges +40% for groups.** Both are included here. Neither
fact is on our pricing page.

**SHIPPED (v0.22.0):** `run_sql` — read-only SQL over the event stream, streaming so memory is
O(groups) not O(events) (measured: 5 KB retained after 20k events, 9 KB after 400k). Closes
correlation analysis, custom metrics and every "can it answer X" objection at once, because the
agent writes the SQL. Hand-written rather than embedding SQLite, which keeps the zero-dependency
binary and avoids materializing rows into a table.

---

## 2. A/B testing and feature flags

The sharpest wedge in the whole comparison, and the one we have never used.

| | us | PostHog | Mixpanel |
|---|---|---|---|
| feature flags | ✅ | ✅ 1M req/mo free | ✅ |
| experiments | ✅ **included** | ✅ | 🏢 **Enterprise add-on, billed per Monthly Experiment User** |
| experiments without the add-on | — | — | **3 per project. Total.** |
| deterministic bucketing | ✅ *(SHA-256, fixed space — fixed today)* | ✅ SHA-1 | ✅ |
| ≥10k bucket granularity | ⚠️ 1% floor | ✅ 100,000 | ✅ 10,000 |
| local/zero-latency evaluation | ❌ server-side | ✅ SSE + polling | ⚠️ |
| bootstrap (no flicker on first paint) | ❌ | ✅ | ⚠️ |
| device-id bucketing across login | ❌ | ✅ documented default | ⚠️ |
| SRM detection | ✅ `experiment_health` **(shipped v0.23.0)** | ✅ | ✅ |
| sequential / always-valid testing | ❌ | ✅ | ✅ |
| CUPED variance reduction | ❌ | ✅ | ✅ |
| sample-size / MDE calculator | ❌ | ✅ | ✅ |
| multiple-comparison correction | ❌ | ⚠️ | ✅ |

**Mixpanel's experiments are Enterprise-only, and Enterprise starts around $20,000/year.**
Without it you get three experiments per project, ever. Ours are included at $49. That is one
comparison page: *"A/B testing: included here, $20k/year there,"* quoting their docs.

### two bugs found and fixed today

Researching how the five major platforms bucket users, then checking ours, found two. Neither
errored. Both silently produced confident, wrong results.

1. **Any two concurrent experiments were perfectly confounded.** FNV-1a is `h = (h ^ byte) *
   prime` with an odd prime, and multiplying by an odd number cannot change the low bit — so
   the low bit was just the XOR-parity of the input bytes, and `% 2` discarded the salt almost
   entirely. **Measured phi between two independent 50/50 experiments: +1.0000.** Every user in
   arm A of one experiment was in arm A of the other. GrowthBook shipped this exact
   construction, measured the bias, and retired it.

2. **Editing weights mid-experiment reshuffled everyone.** `hash % sum(weights)` made the
   bucket space a function of the weights, so 1:1 → 2:1 moved the modulus from 2 to 3.
   **5,041 of 10,000 users changed arm** where ~1,667 should have, and **1,674 LEFT the variant
   that had just been widened** — impossible under positional assignment. Two weight sets that
   both sum to 100 hide this completely, which is how it survived.

Now SHA-256 into a fixed [0,1) space with cumulative ranges. phi measures **-0.0052**.

### getting better, in build order

Items 1-3 are about a week and take experiments from *silently wrong* to *trustworthy*.

1. ~~**SRM detection.**~~ **SHIPPED (v0.23.0)** as `experiment_health`. Chi-square at p < 0.001,
   and when it fires it names the segment most responsible — *"broken, and it's iOS: 400 control
   vs 40 test."* The chi-square tail needed a hand-written incomplete gamma function (the stdlib
   has none), validated against published critical values at 15 points across 5 degrees of
   freedom.
2. **Device-id bucketing.** Anonymous → identified changes `distinct_id` → changes the hash →
   changes the variant. PostHog calls this data corruption in their own docs. Persist an
   `sa_bucket_id` on first visit and bucket on that. ~15 lines of SDK. Do **not** build
   DB-pinned experience continuity — it breaks local evaluation.
3. **Confidence intervals + raw numerator/denominator**, replacing today's significant-yes/no
   boolean. Showing the raw counts *is* "computed, not guessed" made visible.
4. **`experiment_sample_size` as an MCP tool.** `N = 16 × variance / d²`. Four lines. The moat
   is the delivery: asking Claude Code *"how long do I need to run this?"* and getting a number
   computed from **your actual current exposure rate**. No competitor can do that in-editor.
5. **Sequential testing (mSPRT).** Solo builders peek constantly — it is the defining behaviour
   of the segment — and a fixed-horizon z-test is simply wrong under peeking. Statsig publishes
   the formula. ~30 lines. Then *"peek as often as you like"* is honest.
6. **CUPED.** Halves the traffic an experiment needs. The only hard prerequisite is per-user
   pre-period history, which we already store — most tools have to build that pipeline first.
   Ship Statsig's four safety gates with it, not after.
7. **Benjamini-Hochberg.** ~20 lines, matters the moment there's a second goal metric.
8. **Make bad states impossible**, not documented: once an experiment records its first
   exposure, freeze the variant list (weights stay editable). Cheaper than the support burden.

**Licensing:** gbstats is plain MIT — safe to read and port. Unleash **SDKs** are Apache-2.0.
The Unleash **server** is AGPL — do not read it. Formulas published in Statsig/PostHog docs
aren't copyrightable and can be implemented from scratch freely.

---

## 3. qualitative and session tools

| | us | PostHog | Mixpanel |
|---|---|---|---|
| surveys | ✅ **native, multi-question** | ✅ 1.5k free | ❌ **none — needs Hotjar** |
| heatmaps | ✅ **standalone** | ✅ toolbar | ⚠️ **requires session replay, web-only, 2 types** |
| session inspector (event timeline) | ✅ | ✅ | ✅ |
| session replay (video) | ❌ **deliberate** | 💰 $0.005/recording | 💰 |
| error tracking | ❌ | ✅ Sentry-class | ❌ |

**Do not build video replay.** It is PostHog's biggest bill-shock line (200-300k replays runs
$1,000-2,000/mo) and it would wreck the per-tenant cost model. Make it a stated position:
*"a session inspector — the full event timeline — not video. Costs you nothing extra, and it's
what you actually read when debugging a funnel."*

**Getting better:** Mixpanel's heatmaps need replay switched on and are web-only. Ours have no
replay dependency, so no video capture, no replay quota, no privacy exposure — say that.
On errors, don't chase Sentry. Ship an `error` event type with a stack-trace string surfaced in
the session inspector, so *"user hit an error, then dropped out of the funnel"* is answerable.
A week, not a quarter.

---

## 4. agent / MCP surface

| | us | PostHog | Mixpanel |
|---|---|---|---|
| MCP server | ✅ 85 tools | ✅ (CLI-mode wrapper) | ✅ 50+ tools |
| on by default for new accounts | ❌ | ❌ | ✅ **since 2026-08-01** |
| agent writes instrumentation | ✅ | ✅ `@posthog/wizard`, 22+ frameworks | ✅ Implementation Skill |
| **verifies the events arrived** | ✅ **only one** | ❌ | ❌ |
| **ties instrumentation to a deploy** | ✅ | ❌ | ❌ |
| **detects drift when code changes** | ✅ | ❌ | ❌ |
| agent-side cohort lifecycle | ✅ create/list/delete | ✅ | ⚠️ **create only — no edit/target** |

**"We have an MCP" is dead as a claim.** Mixpanel's has more surface area and is on by default
for every new signup as of this week. Any copy saying "the only analytics tool with MCP" must
come down.

**The claim that survives** is the loop nobody else closes:

> `propose_instrumentation` → the agent writes it → **`verify_instrumentation`** confirms events
> actually arrived → `record_deploy` + `deploy_impact` tie it to a release →
> `regenerate_plan_from_code` catches drift when the code moves.

Everyone writes the tracking. **Nobody proves it worked.** That is the sentence.

### the best number in this entire document

**Mixpanel's own published estimate: ~30 minutes per event, 10-30 engineer hours for a 20-60
event product, and most customers finish around day 40.**

The incumbent is admitting the job costs weeks. Time our agentic onboarding end to end — repo →
PR → first verified event — and publish the measured number against theirs. **If 40 events can
be instrumented in under an hour, that comparison *is* the product.** Nothing else in this
document comes close to it as marketing.

And their second-most-quoted complaint is *"if the implementation is wrong, your data gets
messy fast."* That is the ICP describing our feature out loud. So the onboarding demo should
show the agent **catching a bad implementation** — `verify_instrumentation` and
`instrumentation_health` as the visible payoff, not `propose_instrumentation`.

---

## 5. deployment, data ownership, trust

| | us | PostHog | Mixpanel |
|---|---|---|---|
| self-host the full product | ✅ **MIT, no carve-out** | ❌ **officially unsupported** | ❌ **none at all** |
| runtime | one Go binary | 35 services, 4 vCPU/16GB min | SaaS only |
| cold start | seconds | **~12 min documented** | — |
| data retention | **your disk, your call** | 1yr free / 7yr paid | **cut 5yr → 2yr in Sept 2025** |
| cookieless / no consent banner | ✅ | ⚠️ | ⚠️ |
| trains AI on your data | **never** | ⚠️ US cloud **opt-out by default** | ❌ |
| known breach of customer data | none | none | **Nov 2025 — OpenAI terminated them** |
| seats | **none, ever** | per-seat above free | unlimited |
| public/anonymous dashboards | ✅ | ✅ | ❌ **no public share link** |

Three facts here are worth pages of copy:

- **Self-hosted PostHog is officially unsupported**, and self-hosting drops groups, lifecycle,
  correlation, advanced paths, subscriptions, extended retention and multiple alerts. HN
  threads on their own FOSS repo report it outright broken.
- **PostHog will train its own models on customer data** (announced 2026-05-27). US cloud users
  opted in by default; EU cloud and BAA/MSA customers opted out. At least six identifiable HN
  users said publicly they're leaving.
- **Mixpanel was breached in Nov 2025** — a phished employee, customer identifiable info
  exported, and OpenAI publicly terminated their use of it.

**Getting better:** write *"your analytics vendor is your attack surface"* — no gloating, just
the facts, the OpenAI link, and the ending: **self-hosting means there is no vendor dataset to
exfiltrate.** Also surface `set_retention` prominently in settings with *"your data, your disk,
your retention"* next to it.

We already have "no seats, ever" and never say it. One line on the pricing page removes a
purchase objection for free.

---

## 6. price

| monthly events | us | PostHog | Mixpanel Growth |
|---|---|---|---|
| 100k | $49 | **$0** | **$0** |
| 1M | $49 | **$0** | **$0** |
| 2M | $54 | $50 | $280 |
| 10M | **$149** | ~$325 | **~$2,520** |
| 20M | $149 + overage | ~$600 | **~$5,320** → then Enterprise (~$20k/yr) |

**We lose below ~2M events and win decisively above it.** PostHog gives 1M events/month free
*per product*; Mixpanel gives 1M free with unlimited seats. A solo builder under 1M pays them
nothing and us $49.

This is the single biggest strategic problem in the document, and it deserves a real decision
rather than a calculator that hides it. Three options:

1. **A small permanent free tier** (10k events, one project, community support) purely as the
   top of the agentic-onboarding funnel. The acquisition cost of a solo builder is currently a
   trial they never finish.
2. **Don't compete at the bottom.** Say plainly: *"under 1M events and happy on Cloud? PostHog's
   free tier is genuinely good. Buy this for the binary, your data, and instrumentation that
   verifies itself."* Honesty converts better than a rigged comparison.
3. **Reprice the entry tier** to land under the point where free tiers stop mattering.

Whichever is chosen: **don't ship a pricing calculator until we win in the ICP's band.** And
when publishing any cost comparison, count events **the way they count them** — Mixpanel
doesn't bill `$identify`, `$merge`, `$opt_in` or profile updates — or the math gets called
rigged and the whole page loses credibility.

### the pricing facts worth quoting

- Mixpanel Growth is **$0.28 per 1,000 events** after the first 1M, growing linearly with no
  ceiling. Group Analytics adds **+40%** and Data Pipelines **+20%** on top, neither priced on
  the pricing page.
- **Mixpanel Enterprise ≈ $20,000/year.** Our Scale is $1,788/year — an **11× gap**, statable
  with a docs citation.
- Real: OpenPanel's founder left Mixpanel at **$300-400/mo on 250-500k events** at 10k MAU.
  Another user reports **~$300/mo for a React Native app with 7-10k monthly users**. Both map to
  $49 flat here. (Re-read the HN thread before quoting the second one verbatim.)
- Warehouse "Mirror" mode bills **every row change — inserts, updates and deletes** — as an
  event. A backfill multiplies the bill with no new user activity. That is the blog post
  *"why usage billing on events is a trap."*

---

## 7. where to spend the next month

**Efficiency wins — most value per hour, in order**

1. ~~`run_sql`~~ **shipped v0.22.0** — the largest capability gap in the matrix, closed.
2. ~~SRM detection~~ **shipped v0.23.0** as `experiment_health`, culprit segment included.
3. **Device-id bucketing.** ~15 lines of SDK, prevents the next silent corruption. **Next up.**
4. **Collect-but-lock on overage** (from dub.co). When quota or trial ends: keep ingesting,
   never 429 the SDK, never drop a row — gate *viewing*. Can't break the customer's app,
   maximum upgrade pressure exactly when they care, and no gap in history on upgrade.
5. **Time the agentic onboarding and publish it** against Mixpanel's own 10-30 hours / day 40.
6. **Reposition on the verify loop**, and delete every "only tool with MCP" claim today.

**Do not build** — all confirmed in-changelog at the incumbents, all out of ICP for one person:
video session replay, Sentry-class error tracking, warehouse sync, logs, SSO/SAML/RBAC/audit
logs, an editor, or a second product line. Mixpanel shipped 30+ notable releases in seven
months. Feature-breadth parity is unwinnable and attempting it is how the quarter gets lost.

### claims checked against the code, not the docs

A false claim on a comparison page is worse than a missing feature, so these were verified
before anyone writes them down:

| claim | verdict |
|---|---|
| heatmaps do scroll depth | **NO.** `internal/heatmap` handles click/coordinate density; scroll appears only as a Y-coordinate cap. Do not claim it over Mixpanel |
| `whats_notable` does real anomaly detection | **YES.** `insight.anomalies()` compares the last 24h against a trailing-week baseline — genuinely baseline-vs-expected, not a static threshold. Mixpanel gates this behind Enterprise, so the row is honest |
| alerts are anomaly-based | **NO.** `alert.Alert` carries a `Threshold float64` — static only. The anomaly engine exists in `insight` but alerts don't use it. **Wiring `anomalies()` into alerting is a small, high-value build** and would make "anomaly detection included on Pro, Enterprise-only there" true for alerts as well as the dashboard |
| agent-side cohort **edit** exists | **NO.** create / list / delete / create_sequence exist; there is no update. Mixpanel's MCP can't edit cohorts either, so adding one small tool wins the row outright |

Still unverified and needing a run rather than a grep: that `create_share_link` renders a real
no-login page, and that `install.sh` works on a clean machine. **The self-host path is the one
structural advantage that cannot be copied — a broken install wastes it.**
