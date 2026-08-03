# displacing an incumbent — the researched case

Every incumbent started with no customers. The question is not whether it can be done, it is
which door is actually open to a product with no references. This is the researched answer,
not the encouraging one.

## the wedge is sovereignty, not price

The strongest finding, and it is structural rather than competitive.

Austria's DSB, France's CNIL and Italy's Garante have each ruled that sending EU visitor data
to US-hosted analytics breaches GDPR Chapter V transfer rules under Schrems II — **even with
Standard Contractual Clauses in place**. And the CLOUD Act means every US-incorporated vendor
is reachable by US government process regardless of where the servers sit.

That disqualifies, structurally and permanently:

- Amplitude (US), Mixpanel (US), Heap (US), Google Analytics (US)
- **PostHog Cloud (US)** — and PostHog's own docs say all paid-plan features are Cloud-only,
  so self-hosting them costs you the product

**CORRECTED 2026-08-03.** The original version of this file said the EU-safe survivors were
Matomo and Plausible and that both were web analytics with no funnels or retention. That is
false, and a prospect disproves it in ten minutes, so it is worth being exact:

- **Umami** — MIT, free, self-hostable — ships funnels, retention, journeys, cohorts, segments,
  attribution, goals, revenue, heatmaps **and session replay**. It is a real competitor.
- **Plausible CE** genuinely has no funnels. That narrower claim survives and is citable.
- **Matomo** has funnels and cohorts as paid plugins: **€275/month** for 4 users self-hosted.
  The honest framing is "costs €275/mo", not "can't do it" — a price argument we win at $49.
- **Swetrix** — EU-hosted, AGPL, funnels + replay + flags + A/B at **$19-39/mo**. We are not
  the cheapest EU-hosted option and must stop implying it.
- **OpenPanel**, **TelemetryDeck** (already ships an MCP), **Rybbit** are all live in this space.

So the door is narrower than first written. It is **not** "the only product analytics an EU
company can self-host". It is: self-hostable product analytics with **native mobile SDKs**, a
**B2B group model**, and an **MCP that verifies its own instrumentation** — the three things
the EU-safe field does not have. Smaller claim, but it survives contact.

There is also a qualification problem with the wedge itself. EU directories gate on **company
domicile**, not data residency, and the hosted cloud runs on Fly.io and Neon — both US-owned,
both reachable by the CLOUD Act. The argument cuts against us too. Either offer a
Hetzner/Scaleway target or lead EU prospects with self-hosting, where domicile is irrelevant.
Pick one before writing EU copy or the pitch collapses on the first technical question.

Price is a supporting argument, not the lead. Leading with cheap invites a race you lose to
whoever is next — and against Swetrix at $19 you lose it immediately.

## why teams actually switch (verified)

In order, from what teams say publicly:

1. **A bill that spiked without the business growing.** MTU and session pricing means a
   traffic surge or a bot wave costs money for users nobody wanted. "We did ROI calculations
   on every new event" is a real quote — that is a tool actively discouraging measurement.
2. **Identity is broken.** Inconsistent event names, user IDs that do not unify across
   devices, the same human appearing as several users.
3. **Instrumentation costs engineering weeks.**

Note what is NOT on the list: missing features. Nobody leaves Amplitude because it lacks a
report. Building more features does not win a displacement.

## what a buyer will actually ask, and where we stand

| they will ask | today |
|---|---|
| can you import our history? | **yes** — amplitude, mixpanel, posthog, umami, jsonl, csv mappers ship in the binary |
| can we run both in parallel and compare? | **yes** — second snippet, no coupling |
| can we get our data out? | **yes** — CSV/JSONL export, and the format is documented |
| is our data in the EU / on our infra? | **yes, completely** — MIT binary, your box, no vendor |
| do we need a consent banner? | **no** — cookieless |
| GDPR deletion / DSAR? | **yes** — delete_user_data |
| what happens at our volume? | **NO — see SCALE.md.** ~500k events resident is the ceiling. This is the blocker. |
| SOC 2? | **no** |
| who else uses you? | **nobody** |

Three honest noes. Two are fixable, one is only fixed by a first customer.

## the target profile

I cannot hand over a verified list of named companies with their traffic and stack — that
would be invention, and pitching a company on a wrong guess about their infrastructure is
worse than not pitching. What is verifiable is the profile and how to find real instances:

**Qualify on all four:**
1. **EU-based, or sells into the EU public sector / health / finance.** Sovereignty is a
   requirement, not a preference.
2. **Already self-hosts something.** If they run their own Postgres or Grafana, the "you run
   it" objection is already answered.
3. **Currently on GA4, Amplitude or Mixpanel.** Verifiable from the page source — the script
   tag is right there.
4. **Under ~500k events/month** until the rollup work lands, or they fail the trial.

**How to find real ones, all checkable:**
- BuiltWith / Wappalyzer: filter EU domains running GA4 or Amplitude
- Job posts naming "Amplitude" or "Mixpanel" alongside a GDPR or data-residency requirement
- The r/selfhosted and r/devops threads where someone says their legal team rejected GA4
- EU startup directories cross-referenced against script tags

That last one is the highest-signal: someone publicly saying "legal rejected GA4" has
self-identified as your buyer, and the reply is an answer rather than a pitch.

## the sequence

1. **Rollups (SCALE.md).** Nothing below matters until a trial survives real volume.
2. **One reference customer, any size, ideally EU.** "Nobody uses you" only ever dies once.
3. **A migration page** that says the quiet part: import your Amplitude export, run both for
   two weeks, compare the numbers, keep whichever you trust. Confidence is the offer.
4. **SOC 2 only when a real deal blocks on it.** Not before — it is months and thousands, and
   a company small enough to buy from a one-person product mostly will not ask.

## the honest read

The sovereignty pressure is real, verified and structural — no US competitor can follow into
it, and PostHog's own EU carve-out on AI training proves they know it. The web-analytics
category on european-alternatives.eu grew over 2,700% in unique visitors during 2025.

But "nobody is serving self-hostable product analytics for EU sovereignty" was wrong. Umami,
Swetrix, OpenPanel and TelemetryDeck are all in this space, and Swetrix undercuts us on price
while shipping session replay. The gap is narrower and more specific than this file first
claimed: mobile SDKs, B2B groups, and instrumentation that verifies itself.

It is also a 3-6 month enterprise motion with a compliance review in the middle, and it is not
the answer to needing $10k in 3 months. Run it as the product's long game. Fund the quarter
some other way.

The faster path, from the same research, is the **migration refugees**: June.so users after the
Amplitude acquisition, Highlight.io users being handed ~$75K LaunchDarkly contracts as of
2026-02-28, Aptabase users who hit a permanent architectural wall the moment they need
retention, and the PostHog users publicly leaving over the AI-training change. Those are people
already looking, in public, today. See COMPETITIVE.md §5 for the named threads.
