# SMOLANALYTICS: THE PM STRATEGY

Decision doc. 2026-08-13.

---

## 1. THE HONEST DECOMPOSITION

**Your hypothesis did not survive. Say it out loud before anything else.**

You assumed: a PM's week is mostly "finding out," and judgment is the small remainder. Therefore automate the finding-out and the headcount falls.

The evidence says finding-out is roughly **8 to 12 percent of the week**, and it is the part PMs *want more of*, not less.

Here is the whole evidence base, graded honestly, because it is thinner than anyone admits:

**Nobody has ever measured a PM week.** No time diary, no telemetry study, nothing. Every number in circulation is self-report from a survey run by a company that sells product management training or product management software. That is the single most important caveat in this document and you should hold it the whole way through.

What the self-reports say, and they agree:

| Source | n | Finding |
|---|---|---|
| Product Focus 2026 | 677 | Single-select "biggest bucket": Inbound 56% / Strategic 25% / Outbound 19% |
| ProductPlan 2026 | ~250 | 72% spend a quarter or less of their time on strategy |
| Pragmatic (2019) | ~2,500 | 27% strategic, 73% tactical |

Note the trap: Product Focus's "56%" is not 56% of hours. It is the share of respondents naming Inbound as their single largest bucket. Do not quote it as a time share anywhere. Also note their own taxonomy puts *product discovery* inside Inbound and *product performance* inside Strategic, so "finding out" is smeared across both buckets and cannot be cleanly isolated by any instrument that exists.

The best single piece of evidence, and the one that actually kills the hypothesis, is the ICSE-SEIP '26 paper (885 Microsoft PMs, peer-reviewed, published task-mix, telemetry-validated on usage frequency only). **What PMs actually hand to AI:**

- drafting or refining documents: **74%**
- summarizing documents: **53%**
- brainstorming: 35%
- emails and external comms: 32%
- **user research or data analysis: 22%**
- prototyping/code: 12%

Writing beats analysis 3.4 to 1. Caveat that cuts the other way: the tool being measured is M365 Copilot, which lives in Word and Outlook and cannot reach an event log, so part of that ratio is tool reach, not job shape. Also the question capped at three picks and four of seven options were writing-flavoured. So call it "writing dominates, magnitude inflated by instrument design."

**The finding that should change your plan more than any other:** 81% of those PMs say AI saves them time. Only 44% say the effort of their job has fallen. Pragmatic's 2025 report names the mechanism: reclaimed time gets "absorbed by coordination and alignment."

**Time savings do not convert into headcount. They convert into a slightly less tired person.**

That is fatal to "automate the finding-out, remove the head." It is not fatal to the business, because of one gap in the data:

**Post-launch measurement is roughly 0 to 5 percent of the week, and mostly it simply does not happen.** Atlassian's 2026 research: nearly half of product teams lack time for data analysis at all. ProductPlan 2026: confidence in measuring business impact averages 3 out of 5. Product Focus: 34% of PMs have no clear primary metric.

Work that never happens cannot be "sped up," and therefore **cannot be absorbed by coordination.** It is the only category immune to the 81/44 finding.

And there is a hard prior on how much of it is waste. Kohavi, KDD 2013, Microsoft: **only one third of tested ideas improved the metric they were designed to improve.** Corroborating: Google ~10%, Netflix ~10%, Etsy "nearly everything fails." Pendo's telemetry across 615 subscriptions: 80% of features rarely or never used.

**Revised decomposition:**

| Slice | Share of week | Automatable? | Who owns it now |
|---|---|---|---|
| Delivery coordination, ticket triage, unblocking | 20-25% | Mechanically yes, politically no | PM, and nobody will let you |
| Stakeholder comms, status, "what's happening with X" | 15-20% | Yes, and largest automatable block | Commodity. Copilot/Notion/ChatPRD own it, free |
| Spec and PRD writing | 10-15% | Yes to draft, no to sign | Identity-loaded. PMs refuse. Commodity anyway |
| Data pulling, slicing, root-causing | 8-12% | Yes, and this is what you built | Analysts more than PMs |
| Discovery and user research | 8-12% | No | The part they want more of |
| Roadmap and prioritisation | 5-10% | No, 60%+ overridden by escalation | Political |
| Launch coordination | 5-10% | No | Political |
| **Post-launch measurement** | **0-5%** | **Yes** | **Nobody. This is the vacancy.** |

**Verdict: the hypothesis is wrong in a way that improves the plan.** You are not automating hours out of a person's week. You are occupying a job nobody is doing. That is a better business, because it does not require anyone to admit their existing staff are redundant, and it cannot be eaten by coordination.

One more thing you need to internalise before positioning. The closest measured analogue for "autonomous system emits an opinion at humans" is the CodeRabbit study: 31,073 review comments, 36.4% accepted, 56.3% rejected. Of the rejections, only about **19%** were fixable by computing the thing correctly. The other 81% were context, scope, intent and taste.

**Recompute-from-raw-log addresses about one rejection in five.** It is real, it is unmatched, and it is not sufficient. Stop treating provenance as the strategy. It is the hygiene layer that makes the strategy believable.

---

## 2. THE POSITIONING SENTENCE

Homepage, first line:

> **most of what you ship does nothing. this is the thing that tells you which.**

Second line:

> every ship gets a scorecard. what it was supposed to move, whether it moved, and the rows behind the number. nobody on your team has to remember to check.

The brave FAQ answer, Autonoma-shaped:

> **does this replace product managers?**
>
> it replaces the one you haven't hired yet, and honestly that's the good outcome. the job it does is the job nobody's doing. nobody checks whether last quarter's ships worked, because checking is boring and no one gets promoted for it. so a third of what your team builds sits there dead and everybody quietly moves on. that part is gone now. the arguing about what to build next is still yours.

Notes on why it is worded like that:

- Autonoma's real trick is not "yes we replace QA." It is the *second clause*: "no human QA team can multiplex across 7 features in parallel." That is a **span** argument, not a cost argument, and span is the only argument that survives contact with the 81/44 finding. Yours is the same shape: no human checks all of it, ever.
- It does not claim to save anyone time. Never say "get your week back." On this evidence you would be lying.
- It is aimed at the person deciding whether to open a req, not at a PM. A PM reading "we replace PMs" is your enemy. A founder reading it is your buyer.
- Keep the brave line in the FAQ where it gets screenshotted, and keep the body copy about capacity. Autonoma does exactly this and even they publish a "60 testers repurposed, not fired" case study alongside a "10% workforce reduction" one. Segmented pitch, not a hedge.

---

## 3. THE WEDGE: THE SHIP LEDGER

**Build this next. One thing. Nothing else until it ships.**

### What it is

Two halves, one loop.

**Half A, the ledger (forward).** When the GitHub App sees a merge and a deploy marker lands, the system auto-drafts a **claim** from the PR title, body and diff: which event or metric this is supposed to move, for whom, in which direction, and how long until there is enough traffic to answer (you already have power planning). The claim is written down, dated, and shown to a human once for a one-click accept or edit. Then it is locked.

At the due date the system **adjudicates without being asked**: moved / did not move / not enough traffic to tell. Cause attribution and the row-level receipt attach automatically. The verdict is permanent and public inside the org.

**Half B, the backtest (backward), which is how you sell it.** On connect, replay the Investigator and the kill list over the customer's **last 90 days of history**, and emit the scorecard they would have received, dated. Ships that did nothing, ranked by engineering weeks burned. Regressions found and how many days earlier. Then one question per finding: *knew / didn't know / that's wrong.*

Output surface: a stable URL per project plus one weekly email. That is it for v1.

### Why it wins

1. **It is the one thing no incumbent sells.** I swept Pendo Novus, Amplitude's five agents, Productboard Spark, PostHog Code, Enterpret, Dovetail, Reforge. Every single one is framed as finding the next opportunity, the next spec, the next fix. **Not one is framed as decommissioning.** That white space is not an accident: telling a company its work was wasted is a political act, and all of them sell to the PM whose work it was. You sell to the founder. That asymmetry is structural, not temporary.

2. **The demo cannot come back empty.** Kohavi's two-thirds failure rate means the backtest will find real waste at essentially any company you point it at. There is no other demo in analytics with that property.

3. **It trades on the thing only you have.** Adjudicating a question nobody asked, retroactively, over 90 days of history, requires the raw rows to still exist and to recompute. Amplitude and Mixpanel cannot backtest a question they were not configured to watch, because they rolled it up. Pendo cannot, because its whole pricing model is MAU aggregation. This is the first feature where "no sampling, no pre-aggregation" is a *capability* rather than a talking point.

4. **It is immune to the absorption problem.** It is not a faster version of something a human does. It is a job with a current occupancy of zero.

5. **Nobody argues with their own past.** The backtest makes a claim about events the buyer personally remembers. That replaces the reference customer you do not have, which your own DISPLACEMENT.md correctly named as the unfixable objection.

6. **The pieces exist.** `internal/investigate/{investigate,cause,killlist}.go`, `internal/brief`, `internal/deploys`, `internal/provenance`, the GitHub App, power planning, pre-registered locked plans (currently experiments-only). What is missing is the claim object, the adjudication cron, the replay driver and the scorecard surface. Weeks, not quarters, for one person.

### What it deliberately does not do

- **It does not block a merge.** A "product impact" status check is physically impossible: a merge check must resolve in minutes, product impact needs days of traffic. Its only two reachable states are `neutral` (which passes, so it gates nothing) and `pending` (which blocks forever and gets deleted in a week). Every vendor with gate mechanics in hand, Codecov, Chromatic, Meticulous, ships them non-blocking by default. The matching event for product impact is the **rollout window**, not the pull request.
- **It does not write PRDs, specs, roadmaps or prioritisation.** ChatPRD reached 100k+ PMs with a large founder audience and sits at six figures ARR. The judgment layer is unverifiable, therefore unprovable, therefore never commands a headcount-scale price. Explicitly out of scope forever.
- **It does not do feedback synthesis or voice-of-customer.** Viable dead, Zeda shutting down, Kraftful absorbed, Monterey absorbed, and the market price for the surviving product is $7.99/month with 131 integrations. Do not enter.
- **It does not auto-revert by default.** More on that below.
- **It does not need Slack, Notion, Jira, Gong, Reddit or Discord.**

---

## 4. BUILD ORDER

Everything after the wedge, in order, with honest effort. "Weeks" means your weeks, which are short.

| # | Item | Effort | Why here |
|---|---|---|---|
| 0 | **Ship Ledger + 90-day backtest** | 3-5 weeks | The wedge. Nothing before it. |
| 1 | **Kill list to the front door** | 3 days | It already exists and is buried in a tab, which by your own "shipped means reachable" rule means it does not exist. Make it a no-login page: point at 90 days, get back what you shipped that moved nothing, costed in engineer-weeks from one input. |
| 2 | **Reprice** | 2 days | See §5. A $49 product cannot carry a headcount claim. Do this the same week as the copy rewrite. |
| 3 | **Stripe restricted read key** | 3-5 days | Customer creates a scoped read-only key themselves in the Stripe dashboard. No approval, no cost, permissionless. Turns "checkout conversion fell 3.1%" into "$4,200/month, 62% on Android." The Investigator already claims cost-ranking and degrades to headcount without it, and 34% of your buyers have no metric defined at all. Highest quality-per-day on the list. |
| 4 | **Autonomy ladder, visible, default low** | 3 days | Read-only → advises → asks → acts, per capability, current rung printed on the scorecard. Auto-revert exists and is currently at the top of a ladder nobody climbs. Statsig ships auto-rollback; LaunchDarkly ships it gated behind Enterprise plus an add-on and OFF by default. So it is parity, not a differentiator, and it is the single most likely way a one-person vendor with no audit log loses its first real account permanently. Keep it, require a typed confirmation to arm, keep the receipt in the customer's event log, stop marketing it as novel. |
| 5 | **Rollups / resident-memory ceiling** | 2-4 weeks | SCALE.md is measured: 554 bytes/event resident, 2M events = 1,109 MB, Pro ships 256 MB. That is roughly 450k events resident. A 20-person startup can hit that in a quarter, and the backtest replaying 90 days is the heaviest read the engine performs. **Trigger, not a schedule: the first time a real trial OOMs, this jumps to #1.** Instrument for it now (one line: log peak heap per render with tenant and event count). |
| 6 | **Linear agent** | 2-3 weeks | The permissionless placement surface. `actor=app`, `app:assignable`, `app:mentionable`, self-serve OAuth, no Linear approval, and agents explicitly do not count as billable users. Verdicts land as issues with an assignee and a state instead of emails. PMs live in trackers (Jira 58% most-recommended, Amplitude dead last), not in analytics tools. But it is worthless until the ledger produces verdicts worth assigning, which is why it is here and not at #0. |
| 7 | **Buy-intent content, ongoing** | 1-2 posts/week | Autonoma has 175 GitHub stars and ~150 blog posts. The content engine is doing more acquisition than the repo. Two clusters: "open source alternative to [Amplitude/Mixpanel/PostHog/Pendo Novus]" and "[competitor] pricing" pages that do the arithmetic the vendor won't. Free, compounds, matches the framework already in your notes. |

**Explicit non-goals. Write these in the repo so you stop relitigating them.**

- **SSO / SAML / SCIM / audit log / SOC 2 Type II.** Cost: 1-2 months of engineering for SSO and SCIM, plus a 3-12 month observation window and $30-90k all in for Type II, plus a permanent security-questionnaire burden with no sales team to absorb it. Call it 2-4 quarters and cash you do not have. **Trigger: a named company with a signed LOI at ≥$15k ACV blocking on nothing else.** Until then, zero hours.
- **Jira / Atlassian Marketplace.** OAuth 3LO is self-serve but distribution goes through Forge/Connect review, and the Atlassian buyer overlaps the enterprise motion you cannot serve. Expansion, not entry.
- **Vercel Marketplace.** Public listing requires 500 active installations *and* an email to `integrations@vercel.com`. Chicken-and-egg plus a partnership-team email. Violates the permissionless rule twice. The Autonoma logos came from an Argentine founder network and a pre-existing Vercel relationship, not from the listing. Their own community livestream got 397 views.
- **Gong, Recall.ai, Reddit, Discord at scale, G2.** Each either needs somebody's approval or puts recurring per-hour COGS under a flat subscription.
- **Session replay as a product.** Three linked sessions inside a verdict, eventually. Not a library with search and filters.

---

## 5. WHO BUYS IT, WHAT THEY PAY, AND THE TENSION

### The tension, stated plainly and not resolved by wishing

A company with 3-5 PMs is 120-400 people. It has procurement, a security questionnaire, SSO as a hard gate, and usually a SOC 2 requirement. You have no SSO, no audit log, no SOC 2, no sales team, ~₹3,000, and a measured event ceiling one to two orders of magnitude below what that company generates in a month.

**You cannot sell to a company with five PMs in 2026.** Not with better positioning, not with a braver homepage. The gap is 2-4 quarters of non-product work plus cash. Anyone who tells you otherwise is not costing it.

One correction to your own brief while we are here: `seats: 0` in `lib/plans.ts` means *unlimited and unmetered*, not zero. Orgs, invites and owner/admin/member roles are already implemented. **Multi-user is not the gap. Compliance is.** That is good news: it means the gate is one purchase decision away rather than one architecture away, if you ever choose to open it.

### The resolution

**Sell against the marginal hire, not the existing team.**

Gartner's CFO survey (fielded Oct 2025): headcount growth expectations collapsing from 6% to 2%, 42% of CFOs expecting AI-driven reduction, mostly 1-5%, characterised as **AI replacing the next hire that would have been approved.** Tech budgets rising for 75%. That means the money already exists as an approved-but-unspent req. You are intercepting spend, not creating a line item.

That also happens to be the only claim your evidence supports. PM headcount is down 28% from the 2022 peak (Live Data Technologies), but the cuts land hardest on VP (-38%), Director (-35%) and Manager (-31%) and lightest on Senior ICs (-21%), while postings are up 12% YoY. The market is deleting the *coordination* layer, not the analysis layer. "Fire four of your five" is contradicted by the data. "You will not need to hire your next one" is not.

### The buyer

**10 to 40 people. Engineer-led. Zero or one PM. The founder is doing the PM job badly at 2am. Already using a coding agent. About to spend $170-200k fully loaded on their first or second product hire.**

That buyer has no procurement, no CPO to offend, no SSO requirement, one decision maker, and reads a pricing page instead of booking a call. It is one hop from your existing solo-dev ICP, not five. And it is exactly the segment Pendo Novus and PostHog are currently courting with free tiers, which is external confirmation the segment converts.

### The price

Kill Scale at $49. It cannot carry the claim; the price itself refutes it.

| Tier | Price | Who |
|---|---|---|
| Self-host | **free forever, no caps** | anyone. Autonoma's model, and you are already open source. |
| Cloud free | 1M events, no card | solo dev, the existing ICP, the funnel |
| Pro | **$29/mo** | indie, one product |
| **Team** | **$199/mo, unlimited seats, metered on ships adjudicated** | the buyer above |

$199 is **2% of one avoided hire** and 3-4% of a fractional PM retainer ($5-12k/mo is the market rate). That is a ratio a founder approves without a meeting. Nobody believes a $49 product replaced a $170k person.

Meter the *work done*, not the seats: ships adjudicated, investigations run, backtests. Autonoma does $100/150k credits with no minimum. Fin does $0.99/resolution. Per-seat fell from 21% to 15% of SaaS in a year. Unlimited seats is also table stakes now, since Amplitude and PostHog both ship it free.

**No enterprise tier. No "contact us." If someone asks for one, that is the LOI trigger, not a product decision.**

### What this means for your time

You do not have two quarters of runway and you do not have a sales team, so any plan requiring either is not a plan for you. Everything above is self-serve, permissionless, and needs nobody's approval: your own repo, your own binary, GitHub App you already have, Stripe keys the customer makes themselves, Linear's free agent API, and content. If a step in this plan requires emailing a partnership team or waiting on someone's review, it has been mis-specified.

Realistic revenue shape: this is not QA Wolf's $90k ACV, because that requires enterprise sales. At $199/mo you need **50 accounts for $10k MRR**. That is achievable from content plus the backtest demo, and it is the honest ceiling of a self-serve motion. Decide now whether that is the business you want, because the strategy is different if it is not, and the other strategy costs money you do not have.

---

## 6. WHY THIS FAILS

Three ways, in descending order of probability, each with the cheapest test.

### 1. The verdicts get ignored (most likely)

CodeRabbit's measured acceptance rate for autonomous opinions in production is 36.4%, and of the rejections, ~81% were context, intent or taste, not arithmetic. The analytics version: your scorecard says "this ship moved nothing" and the founder says "yes, we knew, it was a compliance thing" or "we shipped it for the enterprise deal, not for the metric." Correct, and ignored. Recompute-from-raw-rows does nothing for that.

Worse, there is a second-order version: telling a company its work was wasted is politically hostile even when the buyer is the founder, and especially when the wasted work is the founder's own.

**Cheapest test, and run it first: build only the backtest half, point it at 10 real products (your own, plus 9 you can get access to), and ask one question per finding: knew / didn't know / that's wrong.** If "didn't know" is under ~30%, the ledger is a fancy report and the strategy dies here. Days of work, and it is the same code path the wedge needs anyway, so it is not wasted either way. **Do this before building the forward ledger.**

### 2. The claim cannot be auto-derived

The whole design rests on the system writing the expected effect from the PR diff without a human. If it cannot, the human writes it, which is *more* PM work, not less, and the product becomes an annoying form. Product Focus: 34% of PMs have no clear primary metric at all. Many merges are refactors, infra, copy tweaks and compliance with no measurable intent whatsoever.

**Cheapest test: run the derivation over 200 merged PRs from five real repos (yours plus public ones) and count the share where a checkable claim comes out.** If it is under ~40%, the loop needs a human on every ship and the wedge has to be rescoped to *deploys that touch instrumented flows only*, which is smaller but still real. Two days.

### 3. The free incumbents eat it

Pendo Novus is shipped, free, no MAU cap, connects to your repo, auto-instruments by PR, root-causes and opens fix PRs, with 800+ teams and paid tiers arriving in exactly this half. Amplitude shipped five agents priced at **zero on every tier including Free**, with unlimited seats, absorbing a 200bps gross-margin hit because adoption outran their forecast. PostHog is building the same loop open-source and free at 1M events. All three are courting precisely your buyer.

None of them currently sells decommissioning. But a kill list is not hard to build once someone decides the politics are acceptable, and Pendo already owns the flag layer and the repo connection.

**Cheapest test: connect Novus to a real repo this week and see whether it survives contact.** Its only public quality evidence is Pendo's own "90%+ on PM-reviewed evals" in a post written by Pendo's Chief AI Officer, and a Product Hunt page whose "reviewer" is actually a maker, with zero independent reviews and 151 upvotes. If the instrumentation PRs are noise, the loop is not commoditised and you have more room than this document assumes. Then watch PostHog's changelog monthly for tracker write-back and Amplitude's Q3/Q4 for whether agents show up in NRR. One hour a month.

---

## THE ONE-LINE VERSION

Your hypothesis about the PM week was wrong, and the correction is good news: stop trying to make PMs faster (time savings get absorbed and never remove a head) and instead occupy the job with zero current occupancy, which is checking whether anything you shipped worked. Build the ship ledger with the 90-day backtest as its cold open, because Kohavi guarantees it finds waste at every company and because retroactively adjudicating an unasked question over raw history is the only thing your architecture can do that Amplitude's and Pendo's structurally cannot. Sell it at $199/month to the 10-40 person engineer-led company about to make its first product hire, not to the 300-person company with five PMs, because that company needs SOC 2 and SSO and two to four quarters you do not have. Run the backtest against ten real products before you build the forward half, and if fewer than three in ten findings come back "we didn't know," stop and go back to being the best analytics engine for solo builders.