# the total feature list: smolanalytics vs PostHog vs Mixpanel

Built by enumerating what actually exists in the code — 85 MCP tools and 40 engine packages —
rather than from memory, then mapped against vendor docs verified 2026-08-03.

`SIDE_BY_SIDE.md` is the strategic read. This is the complete inventory.

**Legend:** ✅ included · 💰 costs extra · 🏢 Enterprise only · ⚠️ partial · ❌ absent

---

## 1. product analytics

| | us | PostHog | Mixpanel |
|---|---|---|---|
| funnels | ✅ | ✅ | ✅ |
| funnel breakdown by property | ✅ | ✅ | ✅ |
| funnel exclusion steps | ✅ | ✅ | ✅ |
| funnel conversion window | ✅ | ✅ | ✅ |
| strict vs any-order funnels | ✅ | ✅ | ✅ |
| retention (cohort triangle) | ✅ | ✅ | ✅ |
| rolling / bucketed retention | ✅ | ✅ | ✅ |
| paths / user flows | ✅ | ⚠️ advanced paths not in self-host | ✅ |
| trends over time | ✅ | ✅ | ✅ |
| trends with breakdown | ✅ | ✅ | ✅ |
| breakdown / segmentation | ✅ | ✅ | ✅ |
| lifecycle (new/returning/dormant/resurrected) | ✅ | ❌ not in self-host | ⚠️ |
| stickiness (DAU/WAU/MAU) | ✅ | ✅ | ✅ |
| cohorts | ✅ | ✅ | 💰 not on Free |
| **sequence cohorts** (did X then Y) | ✅ | ✅ | ✅ |
| group / account analytics (B2B) | ✅ **included** | 💰 | 💰 **+40% on the bill** |
| user activity timeline | ✅ | ✅ | ✅ |
| session inspector (event timeline) | ✅ | ✅ | ✅ |
| goals + goal reports | ✅ | ✅ | ✅ |
| saved reports | ✅ | ✅ | ⚠️ 5/seat on Free |
| **arbitrary SQL** | ✅ `run_sql` | ✅ HogQL | ✅ |
| correlation analysis | ⚠️ via `run_sql` | ❌ not in self-host | 🏢 Signal |
| **retroactive defined events** | ✅ `define_event` | ⚠️ | ⚠️ Custom Events |

**Retroactive defined events** deserve more credit than they get. Naming a new event *after the
fact* and having it apply to history is the direct answer to the single most-quoted complaint
about both incumbents — *"if the implementation is wrong, your data gets messy fast."* Everywhere
else, badly-named events stay bad forever.

---

## 2. experimentation

| | us | PostHog | Mixpanel |
|---|---|---|---|
| feature flags | ✅ | ✅ | ✅ |
| multivariate flags | ✅ | ✅ | ✅ |
| property targeting rules | ✅ | ✅ | ✅ |
| percentage rollouts | ✅ | ✅ | ✅ |
| **A/B experiments** | ✅ **included** | ✅ | 🏢 **Enterprise; 3/project otherwise** |
| deterministic bucketing | ✅ SHA-256, fixed space | ✅ SHA-1 | ✅ |
| **SRM detection** | ✅ `experiment_health`, p<0.001 | ✅ | ✅ |
| **SRM culprit segment** | ✅ names the skewed segment | ⚠️ | ⚠️ |
| flag → deploy marker | ✅ auto-recorded | ❌ | ❌ |
| bucket granularity | ⚠️ 1% floor | ✅ 100,000 | ✅ 10,000 |
| device-id bucketing across login | ❌ **next** | ✅ | ⚠️ |
| local/zero-latency evaluation | ❌ server-side | ✅ SSE | ⚠️ |
| bootstrap (no first-paint flicker) | ❌ | ✅ | ⚠️ |
| confidence intervals | ❌ boolean only | ✅ | ✅ |
| sequential / always-valid testing | ❌ | ✅ | ✅ |
| CUPED variance reduction | ❌ | ✅ | ✅ |
| sample-size calculator | ❌ | ✅ | ✅ |
| multiple-comparison correction | ❌ | ⚠️ | ✅ |

---

## 3. qualitative

| | us | PostHog | Mixpanel |
|---|---|---|---|
| surveys, multi-question | ✅ **native** | ✅ 1.5k free | ❌ **needs Hotjar** |
| survey targeting + results | ✅ | ✅ | ❌ |
| heatmaps | ✅ **standalone** | ✅ toolbar | ⚠️ **requires replay, web-only** |
| scroll-depth maps | ❌ | ✅ | ❌ |
| session replay (video) | ❌ **deliberate** | 💰 bill-shock line | 💰 |

---

## 4. web + acquisition

| | us | PostHog | Mixpanel |
|---|---|---|---|
| web analytics (visitors, pages, referrers) | ✅ | ✅ | ⚠️ |
| UTM / campaign tracking | ✅ | ✅ | ✅ |
| live-now visitors | ✅ | ✅ | ⚠️ |
| device / browser / OS breakdown | ✅ | ✅ | ✅ |
| **geo-IP country resolution, bundled** | ✅ 354k ranges, no vendor | ✅ | ✅ |
| **bot / crawler filtering** | ✅ `botua`, method publishable | ✅ | ⚠️ |
| **Google Search Console integration** | ✅ | ⚠️ | ❌ |
| **AI crawler tracking** (GPTBot, ClaudeBot…) | ✅ | ⚠️ | ❌ |
| **AI visibility / GEO** (do models cite you) | ✅ | ❌ | ❌ |
| **crawl-coverage gaps** | ✅ | ❌ | ❌ |

The last three exist nowhere else. Whether models can read and cite a site is a question every
founder now asks and no incumbent answers, because it needs both halves — what crawlers fetched
from your server *and* what models say — and only a tool sitting on your own traffic has both.

---

## 5. agent + LLM observability

| | us | PostHog | Mixpanel |
|---|---|---|---|
| agent conversation capture | ✅ | ✅ LLM analytics | ❌ |
| agent tool-call analytics | ✅ | ⚠️ | ❌ |
| agent error tracking | ✅ | ⚠️ | ❌ |
| conversation sampling + labelling | ✅ | ⚠️ | ❌ |

---

## 6. the agent / MCP surface

| | us | PostHog | Mixpanel |
|---|---|---|---|
| MCP server | ✅ **85 tools** | ✅ CLI-mode wrapper | ✅ ~50 tools |
| on by default for new accounts | ❌ | ❌ | ✅ since 2026-08-01 |
| agent writes instrumentation | ✅ | ✅ wizard | ✅ skill |
| **verifies events actually arrived** | ✅ **only one** | ❌ | ❌ |
| **instrumentation health / drift** | ✅ | ❌ | ❌ |
| **regenerate plan from code** | ✅ | ❌ | ❌ |
| **suggests a fix for bad instrumentation** | ✅ | ❌ | ❌ |
| tracking plan + drift gate | ✅ | ⚠️ | ✅ Lexicon |
| deploy markers + deploy impact | ✅ | ⚠️ | ❌ |
| agent-side cohort lifecycle | ✅ create/list/delete | ✅ | ⚠️ **create only** |
| **write SQL from the editor** | ✅ `run_sql` | ✅ | ⚠️ |

---

## 7. data in and out

| | us | PostHog | Mixpanel |
|---|---|---|---|
| import from Amplitude / Mixpanel / PostHog | ✅ | ⚠️ | ⚠️ |
| import from Umami / JSONL / CSV | ✅ | ❌ | ❌ |
| **one-click API-key import** | ❌ **gap** | ✅ | ✅ |
| CSV / JSONL export | ✅ | ✅ | ✅ |
| export links (shareable) | ✅ | ⚠️ | ⚠️ |
| webhooks | ✅ **unlimited** | ✅ | 💰 |
| alerts | ✅ **unlimited** | ⚠️ 10-20 metered | 🏢 |
| **anomaly-based alerts** | ❌ engine exists, not wired | ✅ | 🏢 |
| public / anonymous dashboards | ✅ | ✅ | ❌ **none** |
| data warehouse sync | ❌ | ✅ 💰 | ✅ 💰 |
| reverse ETL / CDP | ❌ | ✅ 💰 | ✅ 💰 |
| error tracking (Sentry-class) | ❌ | ✅ | ❌ |
| logs | ❌ | ✅ 💰 | ❌ |

---

## 8. SDKs and ingestion

| | us | PostHog | Mixpanel |
|---|---|---|---|
| JavaScript / web | ✅ | ✅ 75 KB gzipped | ✅ |
| Swift (SPM) | ✅ | ✅ | ✅ |
| Kotlin / Android | ✅ | ✅ | ✅ |
| React Native | ✅ | ✅ | ✅ |
| Flutter | ✅ | ✅ | ⚠️ |
| server-side SDKs | ⚠️ HTTP API | ✅ many | ✅ many |
| autocapture | ⚠️ | ✅ | ⚠️ **web-only** |
| cookieless / no consent banner | ✅ | ⚠️ | ⚠️ |

Mixpanel's autocapture is **web-only**, so mobile developers hand-instrument every event — at
their own published rate of ~30 minutes each. That is precisely where agent instrumentation is
worth the most and where the incumbent's easy path does not exist.

---

## 9. operations, trust, deployment

| | us | PostHog | Mixpanel |
|---|---|---|---|
| self-host the full product | ✅ **MIT, no carve-out** | ❌ **officially unsupported** | ❌ **none** |
| runtime | **one binary, zero deps** | 35 services, 4 vCPU/16GB | SaaS |
| cold start | seconds | **~12 min documented** | — |
| data retention control | ✅ **your disk** | 1yr free / 7yr paid | **cut 5yr → 2yr, 2025** |
| GDPR erasure (`delete_user_data`) | ✅ | ✅ | ✅ |
| audit log | ✅ | 🏢 | 🏢 |
| API keys + rotation | ✅ | ✅ | ✅ |
| seats | **none, ever** | 💰 above free | ✅ unlimited |
| SSO / SAML / RBAC | ❌ **deliberate** | 🏢 $250-750/mo | 🏢 |
| SOC 2 | ❌ | ✅ | ✅ |
| trains AI on your data | **never** | ⚠️ US cloud opted in | ❌ |
| known breach | none | none | **Nov 2025, OpenAI left** |

---

## 10. the honest scoreboard

**Only we have:** full self-host with no feature carve-out · instrumentation that verifies
itself and reports drift · AI-crawler tracking + GEO visibility + crawl-coverage gaps · flag
flips as automatic deploy markers · retroactive defined events · SRM with a named culprit
segment · unlimited alerts and webhooks · no seats.

**Genuinely behind:** error tracking · warehouse/CDP · session replay video · scroll maps ·
one-click API-key import · local flag evaluation and bootstrap · confidence intervals ·
sequential testing · CUPED · sample-size calculator · SOC 2 · **price below 2M events**.

**Where we are quietly ahead and never say it:** experiments included where Mixpanel needs
~$20k/year Enterprise · groups included where Mixpanel adds 40% · cohorts included where
Mixpanel excludes them from Free · surveys native where Mixpanel has none · public dashboards
where Mixpanel has no anonymous share link · unlimited alerts where PostHog meters them at 10-20.

### the four that would move purchase decisions most

Ranked by effect on someone deciding whether to pay, not by engineering interest:

1. **Price below 2M events.** Both incumbents give away 1M/month. This is the first thing a
   solo builder checks and the only row where we lose to *free*. Strategic, not technical.
2. **Empty states with a ghost preview.** Every funnel, retention and paths screen is blank on
   day one for every new user. Blank reads as broken; a preview of the filled report plus one
   CTA reads as a product. This is the highest-churn moment in the whole lifecycle.
3. **Collect-but-lock on overage.** Keep ingesting when the quota or trial ends and gate only
   *viewing*. It cannot break the customer's app, it creates upgrade pressure exactly when they
   care most, and their history has no gap on upgrade.
4. **Confidence intervals and raw counts** instead of a significant yes/no boolean. Showing the
   numerator and denominator next to every rate *is* the "computed, not guessed" claim made
   visible — and it is the cheapest trust we can buy.
