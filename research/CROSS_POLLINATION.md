# Cross-Pollination: the mechanic that makes agentic E2E testing 10x

Research date: 2026-08-29. Method: primary sources (vendor docs, founder essays,
changelogs, post-mortems) read directly, cross-checked against our own architecture
(zero-dependency Node CLI, recordings as plain JSON committed to the customer repo,
verdicts posted to a Next.js control plane, no per-tenant infra for test execution).

Question being answered: **the Postman question.** Postman was not the first API tool.
curl, SoapUI, Paw all existed and did the same job. Postman won because it turned the
request into a *saveable, shareable, re-runnable artefact* — the collection — and then
made the collection the thing the team's work lived inside. The tool stopped being a
verb and became a place.

We are in the same position: agentic E2E testing is not a category we invented, and the
features are converging. The question is what our *artefact* is and whether using it
creates its own distribution.

STATUS: complete.

---

# Part I — the case studies

Each one answers three questions: the job that already existed, what was done
DIFFERENTLY, and the transplantable mechanic for us.

## 1. Postman — the collection, and the button that forked it

**Job that already existed.** Send an HTTP request and look at the response. curl (1997),
SoapUI (2005), Paw (2012), Fiddler, even a browser address bar all did this. Postman
launched in 2012 as a Chrome extension built by Abhinav Asthana as a side project while
he was at Yahoo Bangalore, for himself, because "he didn't find anything good enough".

**What it did differently.** It made the request *persistent and portable*. The
**collection** — a saved, named, ordered, re-runnable group of requests with variables —
turned a transient act into a file. Asthana's own framing in the First Round interview:
collections "became a very lightweight way for people to share what they were doing, and
established a workflow between groups of people". Then the crucial observation, from
Postman's own retelling: **Box and Microsoft started publishing collections publicly,
unprompted.** Postman did not build that; users did, and Postman then productised it —
team library (2016), public workspaces, and the **Run in Postman button**.

The Run in Postman button is the pure form of the mechanic and worth reading literally
(learning.postman.com, fetched 2026-08-29):
- The publisher generates an HTML or Markdown snippet and drops it "in your website or a
  README".
- A viewer clicks it and can "instantly fork your Postman Collection into their
  workspace" — i.e. **the click installs Postman into the viewer's workflow**.
- "The system automatically updates active buttons with collection changes" — the embed
  stays live without the publisher doing anything.
- Forks can "submit pull requests to the source collection" — the artefact gets
  git-shaped social mechanics.

So: every API company that wanted to be easy to adopt embedded a Postman ad in its own
docs, for its own reasons, and maintained it for free. Postman's distribution was other
people's README files. 50M+ public collections, 17M+ developers.

**Transplantable mechanic for us.** Two distinct things, and it is important not to
conflate them:
1. *The artefact must be a file the customer owns and can hand to someone.* We already
   have this: recordings are plain JSON in the customer's repo. We have the collection.
   We do not yet have the **fork**.
2. *The one-click "run this on your app" embed.* A `smoltest` suite is a folder of
   markdown sentences — it is more portable than a Postman collection, because it
   contains no selectors, no base URLs, no auth headers. A suite written against
   Vercel's commerce template runs against ANY Next.js commerce app. That is the
   property Postman's collections never had: ours are **app-agnostic by construction**.
   A "Run this suite on your app" button in someone's README is a one-line install of us.

**Honest caveat.** Postman's button worked because API publishers had a selfish reason to
publish (make my API easy to try). Who has the equivalent selfish reason to publish a
test suite? Answer, and it is the whole design of move #1 below: **template and
boilerplate authors** (Next.js starters, Supabase/Clerk/Stripe example apps, indie
boilerplate sellers like ShipFast) want to prove their template works. A suite that
proves it, on their README, is their marketing and our install.

## 2. Replay.io — the honest failure, read before anything else

**Job that already existed.** Debugging: console.log, breakpoints, the browser DevTools.
Replay recorded a browser session and let you scrub time with full DevTools at every
point — genuinely novel technology, deep systems work.

**What it did differently, and why that was not enough.** Their own post-mortem
("A new direction", replay.io/blog, read 2026-08-29) is the most useful document in this
entire research pass, and every sentence is a warning aimed directly at us:

- **"We started with a solution and have been searching for a problem."**
- Bug-report phase: users "worried replays could contain sensitive data and that you had
  to download a separate browser to record a replay" — *two adoption frictions killed a
  superior artefact.*
- The killer: **"most issues are reproducible once you can see the video and network
  requests. And once you can reproduce the problem, you don't need a time-travel
  debugger."** The cheap 80% artefact (a video + a HAR) ate the expensive 100% artefact.
- Test-suites pivot: they assumed devs wanted deep debugging of test failures. Reality:
  **"most users were okay fixing the test so that it would pass eventually."**
- Outcome: Test Suites discontinued 31 Aug 2024, first RIF, stopped scaling.

**Transplantable mechanic — inverted, as a constraint on our list.** Any move we make
that is *"here is a richer artefact for understanding a failure"* is competing with the
user's willingness to just fix the test and move on. Evidence depth is table stakes, not
a wedge. This directly demotes ideas like "time-travel our recordings", "full trace
viewer", "DOM snapshots at every step". **We must not spend our scarce weeks making the
failure artefact deeper. We must spend them making it travel.**

## 3. Chromatic / Percy — the PR ritual a non-engineer joins

**Job that already existed.** Look at the app before merging. Screenshots in a PR
description, "can you check staging?", pixel-diff scripts, BackstopJS.

**What they did differently.** They turned "look at it" into a **required status check
with named human approvers who are not engineers.** From Chromatic's own docs (fetched
2026-08-29): UI Review brings together "developers, designers, PMs, and stakeholders";
the CI build "generates a status check labeled UI Review"; the check "can be configured
as a mandatory requirement in GitHub, GitLab, or Bitbucket, preventing merges until
review requirements are satisfied"; reviewers are "emailed a link to the Review screen";
collaborators with repo permissions "can sign in to review immediately", and unlinked
projects use an invite code so people **without** a Git account can be added.

Note the compound effect. A merge is blocked until a designer clicks. So an engineer
*invites the designer*. The designer now has an account. The tool spread sideways into a
role that never installs dev tools, and its usage is enforced by the merge button.

**Transplantable mechanic for us.** Our verdict is currently engineer-to-engineer: a PR
comment about a test. The move is to make the verdict something a **non-engineer can
read, and occasionally must answer**. Two concrete forms:
- The recording's proof string ("the cart shows one line for that product at the price
  the product page listed") is written in English by construction. It is already a
  sentence a founder's non-technical cofounder can approve or reject. Nobody else in this
  category has an artefact that is *natively human-readable* — Playwright traces are not.
- "This test is now stale because the app changed" is exactly the Chromatic
  accept-the-new-baseline moment, and it is a decision a product person should make, not
  a build. A stale verdict that asks a human "is the new behaviour correct?" is the
  same ritual.

**Honest caveat for our buyer.** Chromatic's mechanic needs a second human. At a solo
builder there is no designer to invite — the ritual degrades to a nag. It compounds only
in the 5–50 band. Rank accordingly: this is a retention/expansion mechanic, not an
acquisition one.

## 4. Vercel preview deployments — the link everybody opens

**Job that already existed.** Deploying a branch somewhere for review. Heroku review
apps (2015) did this first and did it well.

**What Vercel did differently.** It made it *automatic, universal, and addressed*. From
the docs (fetched 2026-08-29): a preview is created on any push to a non-production
branch, on every PR on GitHub/GitLab/Bitbucket, and on a bare `vercel`; "each deployment
gets an automatically generated URL, and you'll typically see links appear in your Git
provider's PR comments"; there are two flavours, a **branch URL that always points at
the latest** and a **commit URL frozen to that exact deployment**.

The mechanic is not the environment. It is that **the PR grew a link that everyone
opens** — engineer, designer, founder, customer. The link is the shared referent for the
change. That is why Vercel, a hosting company, became a collaboration company.

**Transplantable mechanic for us.** We already sit on that link (`preview.mjs` discovers
the PR's own preview URL from the deployments API). The transplant is the *symmetry*:
every PR gets a deploy link, and it should get a **run link** — one URL that anybody can
open to see what an agent just did to that preview, in English, with the screenshot. Two
flavours, exactly like Vercel: a per-run permalink frozen to a SHA, and a per-test URL
that always shows the latest.

## 5. Playwright Trace Viewer — the artefact after a failure, with zero infra

**Job that already existed.** Understanding why a test failed. Cypress solved it with a
paid **Dashboard** — a hosted service holding your run history, videos, and screenshots.

**What Playwright did differently, and this is the underrated one.** `trace.playwright.dev`
is a **fully client-side viewer** (docs, fetched 2026-08-29): "Trace Viewer loads the
trace entirely in your browser and does not transmit any data externally." You drag a
`trace.zip` on to it, or — the important part — **you pass a URL**:
`https://trace.playwright.dev/?trace=https://example.com/trace.zip`.

So Microsoft shipped the artefact-viewing half of Cypress Dashboard as a **static page
with no backend, no accounts, no per-tenant storage, and no privacy story to defend** —
and it is free forever, which is a large part of why Cypress's paid Dashboard stopped
being a moat. Compare Cypress's model: your evidence lives on their servers, on their
plan, with their retention limits.

**Transplantable mechanic for us — and it is nearly free given our architecture.** Our
recordings are *plain JSON in the customer's repo*. A static viewer page on our own site
that takes `?recording=<url>` or a dropped file and renders the run as a readable story
(sentence → steps → proof → screenshot) costs a weekend, needs **zero per-tenant infra**,
holds no customer data, and makes every recording in every repo on GitHub openable by
anyone with the raw URL. It also turns every public repo that adopts us into an inbound
link. This is the cheapest 10x-adjacent thing on the list.

## 6. Sentry — the error carries the context, and the issue becomes the unit of work

**Job that already existed.** Knowing your app threw an exception. Log files, `tail -f`,
Airbrake/Bugsnag, an email from a user saying "it's broken".

**What it did differently.** Two moves, and the second is the one people forget.
1. *The artefact carries what you need to act.* Sentry attaches the stack trace, the
   release, the user, tags, breadcrumbs, and suspect commits to the event. You do not go
   looking for context; the context arrives with the alarm.
2. *Grouping created a durable object.* From Sentry's own docs (fetched 2026-08-29):
   "we group similar events into issues based on a fingerprint". That fingerprint is what
   turns ten thousand events into **one thing with a life** — states ("All Unresolved",
   "For Review", "Regressed", "Archived", "Escalating"), an assignee via "ownership rules
   to automatically assign issues to the right owners", and a resolution that can
   *regress* later.

The **Issue** is the invention. Not error collection — the persistent, assignable,
resolvable, regression-aware object. That is why Sentry sits in the workflow instead of
in a log drain.

**Transplantable mechanic for us.** Today our unit is a *run*: a verdict at a point in
time on a PR. That is an event, not an issue. The transplant is to fingerprint failures
across runs and PRs so that "checkout breaks when the coupon field is empty" becomes a
**durable object with a state machine**: first seen → suspected commit → resolved →
**regressed** (and regressed is the money word: "this broke again, three weeks after you
fixed it" is a sentence no test runner currently says). We are unusually well-placed:
a smoltest failure already carries the sentence, the step, the proof string, the
screenshot, the page text, and `suspect.mjs`'s named-evidence blame. We have Sentry's
context; we lack Sentry's *object*.

**Second, quieter lesson.** Sentry was open source and self-hostable for a decade and it
did not cannibalise the business, because the value was the hosted object graph and the
alerting, not the SDK. Our equivalent asymmetry: the CLI and the recordings are the
customer's; the durable failure-object across time is worth hosting.

## 7. Cypress Dashboard vs Playwright — the cautionary pair

**Job.** Where does a CI test failure go to be understood?

**Cypress's answer:** a hosted, paid **Dashboard**, priced per recorded test result,
holding your videos, screenshots and history. It worked commercially for a while and it
was the company's monetisation. **Playwright's answer** (§5): a free open format and a
**static, client-side viewer** at trace.playwright.dev with no server at all. Free won
the mindshare; Cypress's dashboard economics — and the "we bill per recorded result"
model — is one of the field's standing complaints (Checkly bills each retry as a check
run; 32% of negative reviews across this category are "the tool itself is flaky", so
being billed for the tool's own retries is a compounding grievance).

**Transplantable mechanic — mostly a prohibition.** Do not build a paid evidence
warehouse. Any product design where the customer pays us to *store* their run history is
competing with a free static file and a git repo. What can be charged for is the
*judgement* (the agent run), the *object across time* (§6), and the *social surface*
(§1, §11) — never the bytes.

## 8. Linear — opinionated speed as the product

**Job that already existed.** Issue tracking. Jira, Trello, GitHub Issues, Asana. A
solved, crowded, boring category — exactly ours.

**What it did differently.** It refused configurability. From linear.app/method
(fetched 2026-08-29), the whole thing is framed as "Practices for building… the
foundational ideas Linear is built on", with directives like **"Write issues not user
stories"** and **"Build in public"**. The product ships a *method*, not a toolbox: the
keyboard-first UI, the sub-100ms local-first sync, and the opinionated defaults are the
same argument expressed three ways — *we decided, so you don't have to*.

**Transplantable mechanic for us.** Our category's tools ask the buyer to make a hundred
decisions (which selectors, which fixtures, which retry policy, which reporter). We have
already taken the Linear posture in the runtime — five verdicts, one retry, `flaky` is
never a pass, `errored` is never your app, `continue-on-error` in week one on purpose.
The transplant is to **name it and ship it as a stated method**, because an opinion is
quotable and a toolbox is not. Concretely: a short, hard document — "how a small team
should test a web app in 2026" — with numbered rules we actually enforce in code. This
is a *content* asset that doubles as differentiation, and it is the input to the GEO
play (§9). It is also the cheapest thing on this list.

**Honest caveat.** Linear also had a founding team from Airbnb/Coinbase/Uber and a
design-led launch into a category where everyone already hated the incumbent. The
opinion worked because the product was faster in a way you felt in 200ms. Our felt
equivalent is "1.4s replay vs 8.0s agented" and "no test code to maintain" — we must
lead with the *felt* thing, not the doctrine.

## 9. Stripe — documentation as the acquisition channel

**Job that already existed.** API reference pages. Every payments company had them.

**What it did differently.** Stripe treated docs as a first-class product with engineers
assigned to it — three-column layout, live code panes keyed to your own test keys,
copy-pasteable snippets per language, and a build system of its own (Stripe published
"How Stripe builds interactive docs with Markdoc" on stripe.dev). The docs page for a
task *is* the fastest path to a working integration, so the docs page outranks blogs and
tutorials for the buy-intent query, and the developer arrives already inside the product.

**Transplantable mechanic for us — and it is sharper for us than for most.** The unit of
our product is *a sentence*. That means a documentation page can literally contain the
deliverable: "Test Stripe Checkout in a Next.js app" → the page's body is the four
sentences you paste into `tests/checkout.md` and the exact workflow YAML. Every such page
is (a) a true buy-intent SEO target, (b) a GEO/answer-engine target — when someone asks
Claude or ChatGPT "how do I e2e test my Clerk login", the answer we want returned is our
sentence, and (c) genuinely useful with no fabrication. There is a large, enumerable
space here that is *not* spam because each page contains a runnable artefact: one page
per (stack × flow) — Next.js/Clerk/Stripe/Supabase/Shopify × signup/login/checkout/
upgrade/password-reset/invite.

**Honest caveat.** SEO is slow and we have prior evidence in this org that the constraint
was authority and indexation, not content volume. So this is a compounding background
asset, never the front move — and its near-term value is GEO (LLM answers), which does
not need domain authority the same way.

## 10. Loom — the async artefact that replaces a meeting

**Job that already existed.** Screen recording. QuickTime, OBS, Camtasia, ScreenFlow —
all of which produced a *file* you then had to put somewhere.

**What it did differently.** It deleted every step after "stop". The recording ends and
the **shareable link is already on your clipboard**; the viewer needs no account, no
plugin and no download; and the creator sees who watched. The artefact is *born
shared* — the upload, the hosting, the permission and the link are the product, and the
recorder was the commodity part.

The distribution consequence is the point: **every Loom sent was an ad delivered by a
trusted colleague to exactly the audience most likely to need it**, and a meaningful
share of viewers were outside the sender's company. Usage was the growth loop.

**Transplantable mechanic for us.** Our runs currently end in a terminal (private) or a
PR comment (private repo, engineers only). A run should end the way a Loom ends: with a
link on the clipboard that anyone can open, that needs no account, and that shows the
sentence, the steps, the proof and the screenshot. Combine with §5 (client-side viewer)
and the hosting cost of this is close to zero.

## 11. Storybook — the catalogue became the team's shared vocabulary

**Job that already existed.** Looking at a component. A dev route, a kitchen-sink page,
a Figma file.

**What it did differently.** It made the catalogue **publishable and addressable**. From
Storybook's docs (fetched 2026-08-29): "Teams publish Storybook online to review and
collaborate on works in progress. That allows developers, designers, PMs, and other
stakeholders to check if the UI looks right without touching code or requiring a local
dev environment", and the canonical workflow is "pasting a link to the published
Storybook in a pull request or Slack". Chromatic then bolts that to CI with "a handy link
to your published Storybook in your PR checks".

The deeper effect: once every component has a name and a URL, the team starts *speaking
in those names*. The tool became the vocabulary, and vocabulary is the stickiest moat
there is — you cannot rip out the thing everyone's sentences are made of.

**Transplantable mechanic for us.** Our tests are already named in English
("A shopper can add an item to the cart"). Published and addressable, they become the
team's vocabulary for **what the product promises** — a browsable, always-verified
catalogue of "the things this app does". That artefact has a second identity that no
testing tool has claimed: it is a **living spec**. "Here is everything our app is
guaranteed to do, each line verified 40 minutes ago against production." A founder will
paste that at an investor, a customer, and a new hire. A Playwright suite can never be
that, because it is code.

## 12. SSL Labs, Codecov, Checkly — the three shapes of "using it advertises it"

Grouped because they are three variants of the same property, and the property is the one
the brief says to steal.

**Codecov — the badge.** From the docs (fetched 2026-08-29): a badge is an SVG at a
predictable URL, embedded as `[![badge-alt](badge-url)](link-to-codecov)`, and it "link[s]
directly to your repository's Codecov dashboard". The mechanic: **the customer puts our
logo at the top of their README, for their own reasons** (signalling quality), forever,
and every visitor to that README sees it and can click through to us. Cost to build:
an SVG endpoint. Reach: every reader of every adopting repo.

**SSL Labs — the public board.** ssllabs.com/ssltest shows "Recently Seen", "Recent
Best" and "Recent Worst" panels of sites other people just tested, and results are
**public by default** with an opt-out checkbox: "Do not show the results on the boards".
This is the property the brief points at with outbid.lol — *the act of using it puts you
on a board other people are already watching*, so usage is the advertising and the board
is the reason to come back. Note carefully the two design choices that make it survivable:
it is opt-**out** not opt-in (so the board is never empty), and the thing on the board is
a **grade**, not a private detail.

**Checkly — the public dashboard.** Checkly's dashboards exist to "instantly communicate
the health and performance of your checks" and to "create professional, branded status
displays for customers, internal teams, or specific stakeholders", with a live public
example at status.checkly-dashboards.com. The mechanic: the customer *wants* a public
page because it serves their trust story, and the page carries the vendor. This is the
badge, scaled up to a page, and it is the closest existing precedent in our own field.

**Transplantable synthesis.** We can do all three, and unusually cheaply, because our
verdict is already a small honest object: an SVG badge ("checkout · verified 3h ago"), a
public per-app page ("what this app is verified to do"), and a public board of recent
public runs. But see the adversarial section — one of these three is a trap.

---

# Part II — the ranked shortlist

Ranking rule, derived from the brief: **distribution per day of build, at N=1 customers.**
A mechanic that only works once we have volume is not a distribution mechanic, it is a
reward for having had one. Every move below is scored against our real architecture:
zero-dependency Node CLI, recordings as plain JSON the customer owns, verdicts posted to
an existing Next.js control plane, and no per-tenant infra needed to run a test.

Standing constraint from §2 (Replay): **none of these makes the failure artefact deeper.**
They all make an existing artefact travel.

---

## 1. `--share`: every run ends with a link anyone can open

**Mechanic.** A run can be published to a short URL that renders the sentence, the steps,
the verdict, the proof and the screenshot for anyone who opens it — no account, no repo
access, no install — and the URL is printed and copied at the end of the run, the way a
Loom link is.

**Why 10x, not 10%.** Right now our best artefact — a plain-English account of an agent
using someone's app — dies in a terminal or inside a private PR. Every competitor's
artefact (a Playwright trace, a video, a stack trace) *needs a developer to interpret it*.
Ours does not: "the cart shows one line for that product at $29" is readable by the
founder, the designer, the customer who reported the bug, and the investor. Making it
linkable converts every run from a private event into a message that a trusted person
sends to a specific person who has a reason to care — Loom's exact loop, with the
important difference that ours is generated by CI rather than by someone deciding to
record. The volume is already there; only the address is missing.

**Cost.** ~1.5 weeks. CLI: serialise the run bundle (steps, verdict, proof, one
screenshot, sanitised) and POST it — a few days, no new dependencies. Control plane: one
ingest route, blob storage, and a static-rendered public page — a few days in a Next.js
app that already receives verdicts. Follow Playwright's example and keep the renderer
dumb and client-side where possible; the viewer holds no logic worth defending.

**Honest risk.** *Leakage.* A shared run can contain the app's real page text, an email
address, an internal URL, a screenshot of a dashboard. This is precisely the friction that
killed Replay's bug-report phase ("replays could contain sensitive data"). Mitigation is
non-negotiable and must be the default: sharing is **opt-in per run** (`--share`), the
`smoltest`-prefixed identity is the only identity present by design, secrets/`Authorization`
are never in the bundle, the URL is unguessable, and there is a visible "delete this run"
that actually deletes. Second risk: it is a small new surface that must never be able to
change a verdict — same discipline as evidence today.

**Verification.** Instrument it with our own product. (a) share rate: % of runs invoked
with `--share` after 30 days; (b) **the number that matters — unique viewers per shared
run who are not the publisher**, because a link that only its author opens is a log file
with extra steps; target >1.5 median within 60 days; (c) referral sessions to the marketing
site from `/r/*` pages; (d) installs whose first-touch referrer is a share page.

---

## 2. The verified-promises page, and the badge that points at it

**Mechanic.** A public page per app listing, in English, every promise the suite verifies
and when each was last verified against the live app — plus an embeddable SVG badge
("checkout · verified 14m ago") that links to it.

**Why 10x, not 10%.** This is the only move on the list where **the customer publishes our
logo for their own selfish reason and keeps it there forever** (Codecov's badge, Checkly's
public status dashboard, Storybook's published catalogue, all at once). And it is a better
version of each of those, because a coverage percentage is a proxy nobody believes and a
status page only says "up". Ours says *"this app can actually be signed up for, paid, and
cancelled, and here is the proof from 14 minutes ago"* — which is exactly the claim an
indie SaaS founder is desperate to make to strangers on the internet. It also creates the
category's only **living spec**: a page the team, a new hire, an investor and a customer
all read, in the team's own words. Storybook's real moat was vocabulary; this is the same
moat in the domain of behaviour rather than pixels.

**Cost.** ~1 week on top of move #1, because it reuses the same ingest and the same public
renderer. An SVG route (`/badge/:app/:test.svg`, cached, no auth), a public app page
rendered from the latest verdict per test, and a snippet generator that emits the Markdown
and HTML — Codecov's exact shape.

**Honest risk.** *A badge is a claim, and a wrong claim is worse for us than for them.*
Our whole brand is verdict honesty. Rules that make it survivable, in the same spirit as
`flaky` never being a pass: the badge states a **timestamp, not a state of the world**; it
visibly ages ("verified 6 days ago" goes grey, then "not verified recently"); `flaky` and
`stale` render as themselves and never as green; and no badge ever says "all tests pass".
Second risk: a founder whose checkout badge goes red in public will rip the badge out
rather than fix the bug — so the default badge should be per-flow and opt-in per flow, and
the page should be shareable-unlisted before it is public.

**Verification.** (a) number of live badge SVG requests from third-party domains — the
purest possible measure of "using it advertises it"; (b) click-through rate from badge to
our page; (c) signups whose referrer is a customer's own domain; (d) badge retention: what
fraction of badges installed are still being requested 30 days later (this is the honest
test of whether the risk above bites).

---

## 3. Portable suites, and the "run this on your app" button

**Mechanic.** A suite is a folder of English sentences with no selectors, no base URL and
no fixtures, so it runs against *any* app of that shape. Publish suites at addresses,
add `npx smolanalytics test --suite gh:owner/repo` and a Run-in-Postman-shaped button that
template authors put in their README.

**Why 10x, not 10%.** This is the Postman collection, and our version has a property
Postman's never did: **because the test is a sentence, the artefact is app-agnostic by
construction.** A Playwright suite is welded to one DOM; "a shopper can add an item to the
cart and the cart shows one line at the listed price" is welded to nothing. That makes a
suite the first genuinely *reusable* asset in E2E testing, and reusable assets are what
create publishers. The publisher class with a selfish reason to publish is specific and
enumerable: **template and boilerplate authors** — Next.js commerce starters, Supabase and
Clerk example apps, the paid indie boilerplates. Their pitch is "this template works"; a
button proving it, live, is their marketing and our install, maintained by them, for free.
That is Postman's whole distribution model, on a smaller but reachable population.

**Cost.** ~1 week for the fetch-a-remote-suite path and the button/snippet generator (the
suite format already exists and already ships an example in `templates/`), plus ~half a
day each to seed the first 15–20 suites ourselves against real public apps, which is also
the best possible correctness testing we could do.

**Honest risk.** Two. (a) *The sentences may be less portable than the theory says* — a
suite written against one storefront may fail on another for legitimate reasons, and a
button whose first click fails is worse than no button. That is a testable claim and it
must be tested before it is marketed: take one suite, run it unmodified against ten public
apps of that shape, publish the real hit rate. (b) *Nobody may care.* Template authors are
a small population and unresponsive to anything that looks like outreach — which is fine,
because this move needs no outreach: it needs the button to exist and the suites to be
good enough that people find them.

**Verification.** (a) do our seeded suites actually pass unmodified on N public apps of
their shape — the go/no-go gate, run before any marketing; (b) number of third-party
repos containing the button or `--suite gh:`; (c) runs originating from a published suite;
(d) suites published by people who are not us.

---

## 4. The method: one hard, opinionated document

**Mechanic.** Publish the numbered rules we already enforce in code as a stated method for
how a small team should test a web app — flaky is never a pass, a tool fault is never your
bug, a new tool never reddens a build in week one, tests are sentences not selectors,
evidence can never change a verdict, the runner runs on your key.

**Why 10x, not 10%.** Linear sold an opinion and shipped a tool that obeyed it. Every
sentence above is *already true of our code and already unusual in the field* — Autonoma
has zero files matching "flake"; the field's #1 churn reason is the tool's own
unreliability being blamed on the app. An opinion is quotable, linkable and citable by
answer engines; a feature list is none of those. This is the cheapest asset on the list
and it is the input to every other one — the badge's honesty rules, the share page's
copy, the docs pages' framing, and the answer an LLM gives when asked "how should I test
my app" all derive from it.

**Cost.** 2–3 days. It is writing, and the source material is the codebase.

**Honest risk.** Doctrine without a felt product is a blog nobody reads. Linear's opinion
worked because you felt the speed in 200ms. Ours must lead with the felt thing — no test
code to maintain, 1.4s replay vs 8.0s agented — and put the doctrine underneath it, not
in front of it.

**Verification.** Citations, not pageviews: is the document quoted in threads we did not
start, and do answer engines return its rules when asked how to handle flaky E2E tests?

---

## 5. The Sentry object: a failure that persists, resolves, and regresses

**Mechanic.** Fingerprint failures across runs and PRs so that a recurring failure becomes
one durable object with a lifecycle — first seen, suspect commit, resolved, **regressed** —
instead of N unrelated red comments.

**Why 10x, not 10%.** This is the one move that is not distribution, and it earns its place
for two reasons. First, it is the answer to §7's prohibition: we must never charge for
storing bytes, so the hosted half has to be worth something on its own, and *the object
across time* is the only thing in this product that cannot live in a git repo. Second,
"**this broke again**, 19 days after you fixed it, and here is the commit" is a sentence no
test runner in this field currently says, and it is the sentence that makes someone keep
paying in month four. We already have every ingredient Sentry needed — the sentence, the
step, the proof, the screenshot, the page text, and `suspect.mjs`'s named-evidence blame.
We are missing only the fingerprint and the state machine.

**Cost.** 2–3 weeks, the largest on this list. Fingerprinting rules that survive a rewritten
sentence (the honest hard part), the state machine and regression detection in the control
plane, and the UI. Most of it is `runs.ts`-adjacent work in code that already exists.

**Honest risk.** Fingerprinting is where Sentry's own users complain loudest — bad grouping
is worse than no grouping, and our unit of identity (an English sentence a human edits) is
mushier than a stack trace. Mitigation: fingerprint on the *test's stable identity* (file +
heading, which we already use to name recordings) plus the failing step, not on prose;
and, in our house style, when we cannot tell, say so rather than guess.

**Verification.** (a) regression-detection precision, hand-audited on our own history
before it ships to anyone; (b) does a regression notice get a human response (a commit, a
comment) more often than an ordinary failure does; (c) retention of accounts that have seen
at least one regression notice versus those that have not.

---

## 6. Docs pages whose body is the runnable artefact (the GEO play)

**Mechanic.** One page per (stack × flow) — "test Stripe Checkout in Next.js", "test a
Clerk sign-up", "test a Supabase magic link" — whose content is the exact sentences to
paste into `tests/*.md` and the exact workflow YAML, verified by us against a real app of
that shape.

**Why 10x, not 10%.** Because our deliverable is a sentence, the documentation page can
*contain the whole product*, which is what made Stripe's docs an acquisition channel rather
than a reference. This is also the only move that survives the absence of an audience: an
answer engine asked "how do I e2e test my Clerk login" needs a source, and a page containing
a working, specific answer is the source it will use. It is not spam, because every page
carries a runnable artefact that we have actually run.

**Why it is ranked sixth and not first.** Slow, and this organisation has already measured
that its constraint was authority and indexation, not content volume. Its near-term value
is GEO, which does not depend on domain authority in the same way, and it compounds only
if moves 1–3 supply the inbound links.

**Cost.** ~1 week to build the page template and the generation-from-a-real-run pipeline
(each page should be produced *from* a passing run, so the content cannot drift into
fiction), then continuous.

**Honest risk.** Producing 60 thin pages that rank for nothing and read as slop — the exact
failure mode of programmatic SEO. The guard is a hard rule: **no page ships that we have
not run.** If we have not run the suite against a real app on that stack, the page does
not exist.

**Verification.** Not impressions. (a) do answer engines cite the page when asked the
question it answers, sampled monthly; (b) `npx smolanalytics` runs whose first touch was a
docs page.

---

## 7. The missing-test pull request

**Mechanic.** When a PR touches a flow nothing covers — which `audit` and `suspect` can
already reason about — the workflow opens a small follow-up PR adding the one sentence that
would cover it.

**Why 10x, not 10%.** Dependabot's growth mechanic was that **the artefact is the PR**: it
arrives in the place the work happens, it is trivially acceptable, and accepting it is
adoption. For us the accepted PR does something no competitor's does — it adds a
*sentence*, so the reviewer's cost of accepting is reading one line, not reviewing test
code. And it addresses the real reason E2E coverage stays thin at small teams: nobody has
time to write the test, not that nobody wants it.

**Cost.** ~1 week, and — importantly for our permissionless rule — **no GitHub App is
needed**. The workflow already runs in the customer's own repo with the free `GITHUB_TOKEN`;
adding `contents: write` in their own YAML is enough to push a branch and open a PR. No
install on other repositories, no vendor email, no partnership.

**Honest risk.** A bot that opens unwanted PRs is uninstalled faster than any other kind of
bot. Guards: at most one open suggestion PR at a time, never reopen a suggestion that was
closed, off by default with an explicit opt-in flag, and it must never touch anything except
add a file under `tests/`.

**Verification.** Acceptance rate of suggestion PRs (a healthy number is high — if under
~30% we are guessing, and the feature should be switched off rather than tuned), and
whether the tests it adds ever go on to catch a real failure.

---

## 8. The stale verdict as a human question

**Mechanic.** When a recording goes `stale` because the app changed, do not silently
re-record: ask a person "the app now does X instead of Y — is that intended?", in one
sentence, on the PR, in language a non-engineer can answer.

**Why it is here, and why it is last.** This is Chromatic's ritual — a merge-adjacent
moment that pulls a designer or PM into the tool and gives them an account. It is a real
expansion mechanic in the 5–50 band and it turns our weakest verdict into our most
distinctive one: every other tool treats "the app changed" as a maintenance chore, and
treating it as a **product decision to confirm** is the same move that made visual review
a ritual instead of a diff.

**Why last.** At a solo builder there is no second human, so it degrades to a nag — and
solo builders are half our buyer. It is retention and expansion, not acquisition, and it
must never become a merge blocker for a one-person team.

**Cost.** ~3–4 days: the verdict already exists; this is the wording, one interactive
element on the PR comment, and a default of never-blocking.

**Verification.** Whether a non-committing account ever answers one (the whole point), and
whether asking reduces the rate at which suites are abandoned after a redesign.

---

# Part III — adversarial: the two ideas that sound exciting and are traps

Both of these are the first things that fall out of the brief's own outbid.lol framing,
which is exactly why they need naming.

## Trap 1 — the public board of app test health

**The pitch, and it is seductive.** SSL Labs shows "Recently Seen / Recent Best / Recent
Worst", results public by default with an opt-out checkbox ("Do not show the results on
the boards"). Transplant it: a live board of apps and whether their checkout works. Usage
becomes the advertising, exactly the outbid.lol property. Founders check their rank.
Twitter screenshots it.

**Why it is a trap for us specifically.**
1. *The asymmetry with SSL Labs is fatal.* A TLS grade is about **configuration hygiene**
   and is already publicly observable by anyone with `openssl s_client` — publishing it
   reveals nothing the internet did not already know. "Acme's checkout is broken right
   now" is a **live commercial liability**, is not publicly observable, and we would be
   the ones publishing it. Opt-out-by-default there is not bold, it is a lawsuit and a
   reputation we cannot recover. Opt-in-by-default makes the board empty, and an empty
   board is worse than no board.
2. *It is volume-gated, and we have no volume.* outbid.lol worked because there was
   already an audience watching the board before the first purchase. A leaderboard is a
   reward for having distribution, not a way to get it. Building it at N≈0 produces a page
   with four rows on it, which actively signals that nobody uses us.
3. *The rank is not desirable.* Nobody wants to be #1 at "most tests passing"; the metric
   is not scarce, not competitive, and not flattering to lose at. The mechanics that make
   a board work (scarcity, rivalry, money on the line) are all absent.

**What to take from it instead.** The *per-app* version of the same property, which works
at N=1 and is opt-in and positive-only: the badge and the verified-promises page (move #2).
That keeps "using it advertises it" and drops the board.

## Trap 2 — the free hosted scanner: "paste your URL, we'll test it"

**The pitch, and it is even more seductive.** PageSpeed Insights for E2E. Type a URL, we
run a real agent against it, you get a public shareable report. Free-tool SEO, the best
demo imaginable, instant word of mouth, and it shows off the single most impressive thing
we do.

**Why it is a trap for us specifically.**
1. *It inverts our own economics, which are our best feature.* The entire reason our unit
   economics work is that **the run happens on the customer's machine, on the customer's
   model key** — "the model calls are billed to your account, not resold". A hosted
   scanner makes every stranger's curiosity a variable cost on our card. For a founder
   operating from roughly zero capital, a mechanic that succeeds by costing more money per
   unit of success is a losing bet on its best day, and a single HN front page is the
   worst possible outcome.
2. *It is an abuse surface with no cheap fix.* An agent driving a real browser at an
   arbitrary attacker-supplied URL is SSRF, an unpaid crawler, and a way to make our IPs
   attack someone. Rate limits, allowlists, domain verification and abuse review are weeks
   of work that produce zero product value — and domain verification, the only real fix,
   deletes the "paste a URL" magic that was the whole idea.
3. *Consent.* Our own product warns before touching a production-looking URL and asks a
   human. A hosted scanner that lets strangers point an agent at a third party's live
   checkout contradicts a safety rule we already ship and market.
4. *The demo is already free and already better.* `npx smolanalytics test --url … --test "…"`
   needs no account and runs in sixty seconds. The friction we would spend weeks removing
   is one command.

**What to take from it instead.** The *artefact* half without the *hosting* half: the run
still happens on their machine and their key, and only the finished, sanitised result gets
a public URL (move #1). Same shareability, none of the cost, none of the abuse surface.

**Honourable mention — a third trap, cheaper to name.** A marketplace or public workspace
for shared suites (Postman's end state). It is the right destination and the wrong
starting point: two-sided, volume-gated, and it presumes publishers exist. Move #3 is the
one-sided version — a button and a fetch path — which works with exactly one publisher,
namely us.

---

# Part IV — the synthesis

**The Postman answer for us, in one paragraph.** We are not going to win by being the best
agentic test runner, because that axis is converging and the losing side of it is well
funded. We win the way Postman won: **the artefact.** Ours is better than anything else in
the category for one structural reason nobody can copy quickly — *our test is a sentence
and our verdict is a sentence*, so our artefact is legible to people who cannot read code.
Every other tool in this field produces developer-only evidence. That means our artefact
can travel to audiences theirs cannot reach: the non-technical cofounder, the customer, the
investor, the reader of a README, the buyer of a boilerplate. So the whole strategy is:
**give the sentence an address, and let people who are not engineers open it.**

**Sequencing, given one builder and a distribution constraint.**
- Week 1: move #4 (the method — 2 days, and it is the copy input to everything else) and
  begin move #1.
- Weeks 2–3: move #1 (`--share`) shipped, with the leakage defaults right on day one.
- Week 4: move #2 (badge + verified-promises page) on top of #1's renderer.
- Weeks 5–6: move #3 (portable suites + button), gated on the honest hit-rate test —
  one suite, ten public apps of that shape, publish the real number.
- Then, and only if 1–3 show any pull: #7, #5, #6, #8.

**The kill gate.** If, sixty days after `--share` ships, the median shared run has fewer
than one non-author viewer, the artefact thesis is wrong for this product and none of #2,
#3 or #8 should be built. That is the same discipline Replay applied to itself too late:
"we started with a solution and have been searching for a problem."

---

# Sources

Primary sources fetched 2026-08-29 unless noted.

- Postman — [Run in Postman button docs](https://learning.postman.com/docs/publishing-your-api/run-in-postman/creating-run-button/); [How We Built Postman](https://blog.postman.com/how-we-built-postman-product-and-company/); [First Round: From Chrome extension to $5B platform](https://review.firstround.com/podcast/from-chrome-extension-to-5b-platform-postmans-journey-abhinav-asthana/); [About Postman](https://www.postman.com/company/about-postman/)
- Replay.io — [A new direction](https://www.replay.io/blog/a-new-direction) (the post-mortem quoted throughout); [HN: Replay.io is discontinuing Replay Test Suites](https://news.ycombinator.com/item?id=41066169)
- Chromatic — [UI Review docs](https://www.chromatic.com/docs/review/)
- Vercel — [Deployment environments / preview deployments](https://vercel.com/docs/deployments/environments)
- Playwright — [Trace Viewer docs](https://playwright.dev/docs/trace-viewer) (client-side viewer; `trace.playwright.dev/?trace=<url>`)
- Sentry — [Issues docs](https://docs.sentry.io/product/issues/); [event grouping](https://docs.sentry.io/concepts/data-management/event-grouping/)
- Linear — [The Linear Method](https://linear.app/method)
- Stripe — [How Stripe builds interactive docs with Markdoc](https://stripe.dev/blog/markdoc); [Stripe developer resources](https://docs.stripe.com/development)
- Loom — [Enable video links to copy into your clipboard](https://support.atlassian.com/loom/docs/enable-video-links-to-copy-into-your-clipboard); [Manage your notifications](https://support.loom.com/hc/en-us/articles/360002244757-How-to-manage-your-notifications) (anonymous viewers are reported as "someone" — i.e. no account required to watch)
- Storybook — [Publish Storybook](https://storybook.js.org/docs/sharing/publish-storybook)
- Codecov — [Status badges](https://docs.codecov.com/docs/status-badges)
- Checkly — [Dashboards](https://www.checklyhq.com/docs/dashboards/)
- Qualys SSL Labs — [SSL Server Test](https://www.ssllabs.com/ssltest/) (public boards, opt-out checkbox)
- QA Wolf pricing shape — [QASkills review 2026](https://qaskills.sh/blog/qa-wolf-ai-testing-guide-2026)

Internal, read 2026-08-29: `~/smolanalytics/cli/README.md`, `cli/lib/` (test, suite, safety,
suspect, preview), `cli/templates/github-action.yml`, `research/SCORECARD.md`,
`research/AUTONOMA_IN_MOTION.md`.
