# where we out-build them

Feature parity is unwinnable. Mixpanel shipped 30+ notable releases in seven months and PostHog
now hosts Streamlit apps and diffs screenshots. One person cannot out-ship either, and every
hour spent closing a gap they already own is an hour not spent on something they can't copy.

So the question is not *what are we missing*. It is: **what can they not follow us into.**

The test used throughout: would matching this damage their business model, or require something
they cannot get? If a competitor could ship it next quarter by deciding to, it is a lead, not a
moat — worth building, but not worth betting the product on.

---

## the four structural advantages

### 1. we can see the code that produced the event. they cannot.

This is the one that matters, and it is the least exploited thing we own.

PostHog and Mixpanel see events. They do not see your repository, and they cannot: a SaaS vendor
asking a company for repo access triggers a security review, a legal review, and a very
reasonable no. It is not a feature they have declined to build — it is a door that is closed to
them by what they are.

**It is already open to us.** The MCP server runs inside the editor, where the coding agent
already has the repo. No new permission, no new trust ask, no data leaving the machine. The
agent is the bridge, and nobody else has one.

Nothing about our current product uses this. `propose_instrumentation` writes tracking code and
`verify_instrumentation` checks the events arrived, but once written, the link is gone. An event
in the dashboard has no idea which line of code fired it.

**What it unlocks, none of which the incumbents can build:**

| question | today, everywhere | with code provenance |
|---|---|---|
| where does this event come from? | grep your repo and hope | the file and line that fires it |
| why did `signup` stop firing? | "it dropped 40%" | "the handler in `auth/signup.ts` changed in a1b2c3" |
| what are we NOT tracking? | unknowable from data alone | the user-facing actions in the code with no event |
| is the tracking plan real? | a doc that rots | derived from the code, checked against the data |
| did this deploy break tracking, or the product? | guesswork | the diff, next to the events that stopped |

That last row is the whole pitch. **"Your conversion dropped" is a symptom. "Your conversion
dropped and here is the commit" is an answer**, and it is only possible for a tool that can see
both halves.

**The claim:** *the only analytics that reads the code that produced your events.*

### 2. we can be the whole product on your own box. they structurally cannot.

PostHog's paid features are Cloud-only by stated policy, and self-hosting is officially
unsupported — their revenue depends on hosting. Mixpanel has no self-host at any price.

Both would have to damage their own model to match this. That is what makes it structural rather
than a gap they have not got to yet. It is also worth far more post-breach: Mixpanel had customer
identifiable data exported in November 2025 and OpenAI publicly terminated them over it. Nobody
can exfiltrate a vendor dataset that does not exist.

### 3. one binary, zero dependencies. thirty-five services cannot become one.

`go.mod` has no `require` block at all. PostHog needs ClickHouse, Kafka, Redis, Postgres, a
Django app, a plugin server and nginx — 4 vCPU / 16 GB minimum and a documented ~12 minute cold
start. That architecture cannot be collapsed; it is load-bearing for their scale.

This is not a talking point, it is a cost structure. It is why a flat price is possible for us
and usage pricing is forced on them.

### 4. deterministic answers, while both have committed to LLM analysis

PostHog AI meters at $0.01/credit and pauses at the cap. Mixpanel Agent is an LLM. Both now have
roadmaps, docs and pricing built around generated analysis.

Every report here is computed. The same question returns the same number every time, and the
number can be checked. Reversing that is a roadmap U-turn for both — softer than the first three,
but real.

---

## what to build, and in what order

Ranked by how much a company would prefer us *because of it*.

### phase 1 — event ↔ code provenance

The foundation. Everything below depends on it.

**`$source` on the event.** When the agent writes instrumentation, it also records where: file,
symbol, and the commit it was written against. Sent as event properties, so it needs no schema
change and flows through every existing report.

**`event_source(name)`** — an MCP tool answering "where does this event come from" with the file
and line. From inside the editor, that is a jump, not a search.

**Verified by construction.** `verify_instrumentation` already confirms events arrived. With
provenance it can confirm something stronger: *the call site still exists*. A tracking call
deleted in a refactor currently shows up as a mysterious drop weeks later.

### phase 2 — the diff is the answer

**`explain_change(event, since)`** — the event moved, and here are the commits touching the files
that fire it. We already have deploy markers and `deploy_impact`; the missing half is *which code
the event actually comes from*, which phase 1 supplies.

This is the single most valuable question in product analytics and no incumbent can answer it,
because answering it requires the repo.

### phase 3 — coverage, from the code

**`instrumentation_coverage()`** — read the repo's routes, handlers and form submissions, list
the user-facing actions with no event attached. Every other tool can only tell you about events
that exist; none can tell you what you forgot, because absence is invisible in the data.

This directly answers Mixpanel's own published number: 30 minutes per event, 10-30 engineer
hours, most customers done around **day 40**. Coverage plus generation is how that becomes an
afternoon, and their published figure is the benchmark to beat in public.

### phase 4 — the loop closes

Instrument → verify → deploy → detect drift → propose the fix → verify again. Every piece exists
today; nothing composes them. Composed, it is a product no incumbent can ship:

> Your agent instruments your app, proves the events arrive, notices when a deploy breaks one,
> tells you which commit did it, and opens the PR that fixes it.

---

## from dub.co, the mechanics worth taking

Their craft is in the money moments, not the feature list.

**Collect-but-lock.** Their two meters behave differently on purpose: exceeding the link limit
hard-blocks creation, exceeding the tracked-clicks limit keeps collecting and only locks
*viewing*. Copy it exactly. When a trial ends or a quota blows: **keep ingesting, never 429 the
SDK, never drop a row** — gate the dashboard and the MCP read tools. It cannot break the
customer's production app, so it is safe to be aggressive; it creates maximum upgrade pressure
exactly when they care most; and on upgrade their history is complete with no gap. A customer who
upgrades to find a hole in their data does not upgrade twice.

**A paywall that looks like a product state.** Theirs is the same reusable empty-state component
as the rest of the app, titled "Stats Locked", and it says the data is still being collected.
One `LockedState` component, leading with the reassurance: *"Your events are still being
recorded. Upgrade to view them."* A paywall that looks like an error makes people assume the tool
broke.

**Empty states that show the filled state.** Their empty-state component's API *forces* every
caller to include an animated preview of what the filled version looks like, plus a title, an
action and a docs link. Every funnel, retention, paths and cohort screen here is blank on day one
for every new user, and blank reads as broken. This is the highest-churn moment in the lifecycle
and it is a component, not a project.

**The changelog as the main indexed surface.** Dub has 94 changelog URLs — more than blog,
customers and integrations combined. One URL per shipped item, dated, bylined, one screenshot,
prev/next links. We ship constantly and none of it accrues indexable surface. It is the
highest-leverage SEO available because the content is a byproduct of work already done.

**`llms.txt` plus a `.md` twin of every docs page.** For a product whose pitch is "your coding
agent instruments this", an agent that lands on our docs and cannot enumerate them is a lost
install.

**One-click API-key import.** Ours are file-based, so the user must go and export first — and the
switch motion dies at exactly that step.

**Do not attempt** (capital or headcount we do not have): SOC 2, an enterprise sales motion,
payout and tax infrastructure, vanity domains, a paid docs platform, a founding designer.

---

## what this makes us

Not a cheaper Mixpanel. That comparison is lost before it starts — they give away 1M events a
month and we do not.

**The analytics that lives where the code lives.** It reads the repo to know what should be
tracked, writes the tracking, proves it arrived, and when a number moves it tells you which
commit moved it. It runs as one binary on your own box, so there is no vendor holding your data
and nothing to exfiltrate. Every number is computed, so it is the same on every run and you can
check it.

A company prefers that over Mixpanel not because it has more reports — it has fewer — but
because it answers the one question the reports cannot: **what changed, and where in the code.**

That is a product neither incumbent can build without becoming something they are not.
