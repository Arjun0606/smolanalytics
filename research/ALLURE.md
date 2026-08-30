# ALLURE — what makes a developer tool wanted, as opposed to merely better

Date: 2026-08-30. Companion to `SCORECARD.md` (stale — corrected in §0 below), `RED_TEAM.md`,
`CROSS_POLLINATION.md`, `FIELD_AGENT_FIRST.md`, `GRAVEYARD_AND_BUYER.md`,
`FIELD_INCUMBENTS_AND_FREE.md`, `AUTONOMA_TEARDOWN.md`, `AUTONOMA_IN_MOTION.md`,
`WHITESPACE_DB_AND_AI.md`. Built on them. Where it contradicts one, it says so out loud.

**Two passes.** §0–§6 are the first pass. **§7** (the share-link mechanic, tested against primary
sources — it amends the §4 ranking) and **§8** (an independent re-verification of every load-bearing
claim and quote, with corrections) are a second pass by a second reader, 2026-08-30. Where the two
disagree, the disagreement is stated in place rather than resolved silently.

**Evidence labels.** MEASURED = I fetched the page or ran/read the code today. CLAIMED = the
vendor's own marketing. REPORTED = a third party said it. INFERRED = my read, and it is marked as
mine. Anything I could not verify is called unverified in place and is never promoted to a fact
further down.

**The question this file answers, and the one it refuses.** `SCORECARD.md` asks "are we better than
Autonoma," and `RED_TEAM.md` already established that the answer barely matters — eight companies in
this category died while being perfectly good software. This file asks the question that killed
them: *why would anybody want it?* Being ahead on a spreadsheet is not a reason. Nobody has ever
told a colleague about a comparison table.

---

## §0 — GROUND TRUTH: what we actually shipped, verified against the code today

`SCORECARD.md` predates all of this. Every row below was read in `/Users/arjun/smolanalytics/cli`
today (2026-08-30), package version `0.16.1` (`cli/package.json`, MEASURED — npm's `dist-tags.latest`
is still `0.16.0` today, MEASURED via `registry.npmjs.org/smolanalytics`, so 0.16.1 is unpublished as
of this writing).

| shipped thing | file | verified | note |
|---|---|---|---|
| Parallel suite execution | `lib/pool.mjs` (405 lines) | MEASURED — `--workers` wired in `bin/smolanalytics.mjs:71`, refused-not-defaulted on a bad value (`parseWorkers`), one browser + a context per worker, results returned in suite order, one-login-first serialisation | **Correction to the brief:** the code's own measured table says **39.2s serial → 4.9s at 8 workers** (`pool.mjs:60–66`), and 5.36–5.47s at the auto default. The brief's "4.6s" does not appear anywhere in `lib/`; the only `4.6s` in the tree is an example summary string in `lib/watch.mjs:544`. Quote 39.2 → 4.9. |
| Cross-browser | `lib/engines.mjs` (172 lines) | MEASURED — `ENGINES = ["chromium","firefox","webkit"]`, `--browser` refused on typo, engine stamped into the recording, a cross-engine replay is **a note, never a verdict** | The honesty rule here is the interesting part, not the feature |
| File uploads, fabricated fixtures | `lib/upload.mjs` (502), `lib/uploadsafe.mjs` (121) | MEASURED — file generated from the input's own `accept` attribute (PNG / PDF / CSV / txt), deterministic bytes, recording stores `{kind:"upload", role, name}` and **no path and no bytes** | The recording stays portable because the fixture is rebuilt at replay time |
| `--seed <url>` | `lib/seed.mjs` (366), `lib/seedguard.mjs` (145) | MEASURED — wired at `bin/smolanalytics.mjs:67`; POSTs the run identity to the customer's endpoint before the run, flat JSON keys become `{{placeholders}}` | Closes behind-list #3 in `SCORECARD.md` |
| False-green render guard | `lib/render.mjs` (685) | MEASURED — **verdict-affecting**: a would-be PASS over a blank / unstyled / crashed page becomes `failed`; a `failed` is never softened; `stale`/`errored`/`flaky` are never checked; `--no-render-check` opts out | Closes behind-list #1, the biggest hole in the scorecard |
| Authenticated runs | `lib/auth.mjs` (549) | MEASURED — `--login "<sentence>"` / `--auth-file`, one login per suite, session file reused, credentials redacted (`redact`, `loginCredentials` imported by `share.mjs`) | Closes RED_TEAM §4 gap #2 |
| Token metering, `--max-calls` | `lib/cost.mjs` (145) | MEASURED — counts come from the API's own `usage` block, never estimated; **dollars only when a price is supplied** via `SMOLANALYTICS_PRICE_IN/OUT`; the cap is on calls not dollars; hitting it exits 2, never a verdict | This is a desire artefact, see §4 candidate C2 |
| `--share` | `lib/share.mjs` (937) + cloud `app/s/[id]/page.tsx` (386), `opengraph-image.tsx` (120), `shot/route.ts`, `app/api/share/route.ts` (168) | MEASURED end to end — opt-in, cannot change a verdict or exit code, three masking passes, five statuses never blurred, OG card carries **the verdict and the sentence**, `noindex` but deliberately *not* robots-Disallowed so Slackbot/Twitterbot can still unfurl | Live route confirmed: `smolanalytics.com` returns 200, `/r/{code}` 307s (MEASURED via curl today) |
| `--since <ref>` | `lib/select.mjs` (405) | MEASURED — refuses without `--suite`; every unknown runs the test (no recording → run it; unreadable → run it; no git / no merge base / empty diff → run everything **and say why**; module throws → run everything) | The asymmetry is stated in the file and is the reason to trust it |

Two things in `SCORECARD.md` that are now wrong and should not be quoted again: rows 9 (visual
blindness) and 10's cousin — the false-green hole is closed by `lib/render.mjs`, and the
authenticated-flow and parallelism gaps named in `RED_TEAM.md` §4 are closed. The mobile gap (row
10) and the AI-app assertion gap (behind-list #2) are **still open** — nothing in `lib/` matches
mobile/appium/ios/android, and the proof is still exact page text.

---

## §1 — The thesis

Every tool in §2 won its category. None of them won it with a feature matrix. Each won it with a
single **artefact that a developer could hold, and hand to somebody else.**

The pattern, stated before the evidence so it can be checked against it:

> Desire is created when a developer, using their own code, gets back a concrete object they did
> not have before — a URL, a number, a page, a filmstrip — in under a minute, and the telling of it
> contains an instruction the listener can execute in a minute too.

Two corollaries the case studies force, both of which contradict how this category markets itself:

- **The moment is not the launch post.** Raycast's Show HN got 18 points. Linear's got 3. Prisma
  Studio's got 5. (All MEASURED, HN Algolia, today.) Tailwind v2 got 927 and Supabase got 1,120 —
  and both of those were *second* acts, launched to an audience the artefact had already built.
- **Anything standing between a stranger and the artefact deletes it.** Warp had a genuinely novel
  artefact and put a login in front of it for two years. §2.11 is what that cost.

---

## §2 — THE CASE STUDIES

For each: the exact artefact, the moment somebody told somebody else, and the smallest version that
existed at launch.

### 2.1 Vercel — the preview URL

**The artefact.** A URL that points at this exact commit, forever, that a person who cannot run your
repo can open.

**Primary source (MEASURED, `https://vercel.com/docs/deployments/environments`, seen 2026-08-30):**
> "By default, Vercel creates a preview deployment when you: Push a commit to a branch that is
> **not** your production branch… Each deployment gets an automatically generated URL, and you'll
> typically see links appear in your Git provider's PR comments or in the Vercel Dashboard."

And the detail that makes it an artefact rather than a staging box:
> "**Branch-specific URL** – Always points to the latest changes on that branch · **Commit-specific
> URL** – Points to the exact deployment of that commit"

**The moment.** Not the deploy. The moment a designer or a founder clicks the link in the PR and
sees the change *without installing anything*. The tool's output escaped the engineer.

**The smallest version at launch** (MEASURED, HN 11440224, `Now: realtime Node.js deployments`,
2016-04-06, 288 pts): one command, `now`, in a directory, printing one immutable URL. No git
integration, no PR comment, no dashboard. A commenter in that thread describing it, correctly,
without the vendor's help:
> "[alanh] They seem to be one-time deployments that never go away."

Note what launched *with* it and is rarely remembered: appending `/_src` to any deployment URL
showed the source. Rauchg in-thread: *"All `/_src` links will link to a secure portion of the site
with login + 2FA."* The launch shipped one artefact plus one way to look inside it.

**INFERRED, and it is the lesson for us:** the PR comment came later and is what turned the artefact
into distribution. The artefact was already good; the *address in a place other people already look*
is what made it spread.

### 2.2 Linear — speed as the entire product

**The artefact.** A keystroke with no spinner after it.

**Primary source (MEASURED, HN 33199304, `Linear.app`, 2022-10-14, 212 pts). Users, unprompted:**
> "[erex78] there isn't a tool I use that feels faster - it's like they know what I'm about to click
> on before I do."
> "[dmix] Probably one the fastest web app around, given how it preloads all JSON data from all
> sublinks on the page, so it loads immediately when you [click]"
> "[julianlam] It's a ticketing system whose killer feature is that practically everything is a
> keyboard shortcut."

The best evidence in the thread is accidental. Linear was under a DDoS that day and had redirected
its homepage to a Figma file, and a user still wrote:
> "[ghayes] even under attack, Linear is faster than JIRA."

**The moment.** Pressing `C`, typing, pressing `Cmd+Enter`, and the issue exists before your hands
leave the keyboard — set against Jira, where the same user measured *"30+ second page loads"*
(metadat, same thread).

**The smallest version at launch:** an issue list and a keyboard shortcut, behind a waitlist. Their
2019 HN post (`Linear: Fast Issue Tracking`, 2019-04-19) scored **3 points** (MEASURED). The desire
was not created by that post; it was created by one user showing another the keyboard.

**The lesson we can steal:** speed only becomes allure when it is *contrasted with the thing the
buyer suffers today*. "Fast" is nothing. "Faster than the 30-second page load you had this morning"
is everything.

### 2.3 Bun — the benchmark

**The artefact.** A table of ratios the reader can reproduce on their own repo in one command.

**Primary source (MEASURED, `https://bun.sh/blog/bun-v1.0`, seen 2026-08-30).** The install is one
line — `curl -fsSL https://bun.sh/install | bash` — and the numbers include:

| claim | number |
|---|---|
| `bun run` vs `npm run` script start | **7ms vs 176ms** |
| test runner vs Jest (Zod suite) | 13× |
| test runner vs Vitest | 8× |
| `expect().toEqual()` vs Jest | 100× |
| startup vs Node | 4× |
| bundler vs Webpack | 220× |

**The moment.** Not reading the table. Running `bun install` on *your own* `package.json` and
watching it finish before you look away. The 7ms/176ms row is the one that travelled, because
`npm run` is a thing every reader does forty times a day and has never once thought about.

**The smallest version at launch:** an install script and one drop-in command that beat a tool the
reader already used. (Note the Bun 1.0 HN thread scored 150 points — MEASURED, id 37422106 — and one
of the top comments is *"given that it's written in Zig… I'd trust it only as far as I can throw a
floppy."* The benchmark still won. Skepticism at launch is not a signal.)

### 2.4 Vite — the dev server

**The artefact.** The terminal line that says the server is ready, and the number on it.

**Primary source (MEASURED, HN 31967420, `Vite – Next Generation Front End Tooling`, 2022-07-03, 483
pts).** The single most useful sentence in the thread is a user's own measurement, volunteered:
> "[Escapado] Dev Server cold starts went from **34s to 0.3s** and build times from **1m20s to 9s**
> so I won't complain!"

That is a developer telling other developers, with numbers they took themselves, on their own
codebase. No Vite marketing was involved.

**The moment.** `npm run dev` returning before your hand leaves the keyboard, on a project you
already had.

**The smallest version at launch:** an unbundled dev server serving native ES modules — one command,
existing project, no config. The migration cost was the whole pitch: *"It was surprisingly little
effort"* (same comment).

### 2.5 Tailwind — the first hour

**The artefact.** A component you built without naming anything and without leaving the file.

**Primary source (MEASURED, HN 25140604, `TailwindCSS v2.0`, 2020-11-18, 927 pts):**
> "[whalesalad] I've been writing CSS since the beginning of the web2.0 days and so it was really
> hard for me to adjust to the Tailwind approach… after working almost exclusively with it I am a
> big believer. **I find myself wanting all the utilities almost immediately as soon as I am back on
> any other project/codebase.**"

That last clause is the definition of allure: the tool made its own absence painful.

The thread also contains the most honest description of the adoption curve anyone has written, from
a skeptic (systemvoltage): phases 1–3 are pain with plain CSS, phase 4 is *"Tailwind arrives. OMG.
This is awesome! Never have to leave the context,"* phase 5 is *"HTML looks completely unreadable,"*
phase 6 is wanting 2005 back. **Allure and objection coexisted in the same user.** Tailwind won
anyway because phase 4 happened in hour one and phase 5 happened in month three.

**The smallest version at launch:** a CSS file you drop into a `<link>` tag. Tailwind began as the
byproduct of a side project (`adamwathan.me/tailwindcss-from-side-project-byproduct-to-multi-mullion-dollar-business/`, 308 pts, MEASURED via HN, not fetched — treat the post's contents as unverified here).

### 2.6 Playwright Trace Viewer — the artefact after the failure

**This is the closest structural analogue to our share page in the entire industry, and it is worth
reading twice.**

**The artefact.** A zip file, and a web page that opens it.

**Primary source (MEASURED, `https://playwright.dev/docs/trace-viewer`, seen 2026-08-30):**
> "You can open a saved trace using either the Playwright CLI or in the browser at
> **trace.playwright.dev**."
> "trace.playwright.dev is a statically hosted variant of the Trace Viewer. You can upload a trace
> file using **drag and drop** or via the `Select file` button."
> "Trace Viewer **loads the trace entirely in your browser and does not transmit any data
> externally**."

The trace contains action details, DOM snapshots, screenshots, console logs, network requests and
errors.

**The moment.** A CI failure you cannot reproduce locally. You download one artifact, drag it onto a
web page, and time-travel through the DOM as it was at the moment of the failure. No account, no
upload, no server, no vendor.

**The smallest version at launch:** `--trace on` writes a zip; `npx playwright show-trace trace.zip`
opens it locally. The hosted drag-and-drop page came after.

**The tension this creates for us, stated honestly:** Playwright's design decision — *nothing leaves
the machine* — is the opposite of ours. Our share page posts a bundle to a server. Microsoft can
afford the local-only version because they are not trying to acquire customers; the trace viewer is
a *retention* artefact for a free tool. We are using the same shape of artefact for a different job,
and the three-pass masking in `lib/share.mjs` exists precisely because that job is riskier. Anybody
who compares the two will notice. The answer is not to hide it; it is that our page is openable *by
someone who has not installed Playwright, does not have the zip, and cannot read a filmstrip* —
which is a thing trace.playwright.dev cannot do and does not want to do.

### 2.7 Sentry — the first error

**The artefact.** An error you did not know about, arriving with its stack, its user and its release.

**Primary source (MEASURED, `https://docs.sentry.io/platforms/javascript/`, seen 2026-08-30).** The
verify step, still in the docs a decade and a half later:
> "To verify that Sentry captures errors and creates issues in your Sentry project, add a button
> that throws an error when clicked."
> ```html
> <button onclick="triggerError()">Break the World</button>
> ```

**The moment.** Two moments, and the order matters. First, the manufactured one: you click *Break
the World* and the issue appears in the dashboard in seconds — the tool proves itself on demand, on
your app, in your browser. Second, the real one: an hour later an error you never triggered arrives,
from a user you have never met.

**The smallest version at launch** (MEASURED, `https://cra.mr/sentry-from-the-beginning/`, David
Cramer, seen 2026-08-30): `django-db-log`, 2008, written to answer one question asked in IRC —
*"how would you record errors to a dashboard?"* Errors written to a dashboard instead of a log file.
That is the entire product. Cramer on the commercial start: *"We launched it and that same day we
had our first paying customer. Seven dollars baby!"*

**The lesson we can steal, and it is the biggest one in this file:** Sentry ships a *deliberate
self-inflicted failure* as step 4 of onboarding. The tool is not asked to be trusted; it is asked to
be proven, immediately, by breaking something on purpose. We have the exact equivalent and do not
use it (§4, candidate C4).

### 2.8 Stripe — the docs

**The artefact.** A `curl` command that works when pasted, because it already contains your key.

**Primary source (MEASURED, `https://docs.stripe.com/api`, seen 2026-08-30):**
> "The Stripe API differs for every account as we release new versions and tailor functionality.
> **Log in to see docs with your test key and data.**"

And today, a second front door on the same page:
> "Read this page in your terminal: install the Stripe CLI (v1.43.3+) and run `stripe docs`."

**The moment.** You paste the example, it returns a real object from *your* account, and the
distance between reading and having-integrated collapses to zero.

**The smallest version at launch:** a two-column reference where the right column is a runnable
request. It was so obviously the artefact that PayPal was accused of cloning it outright
(TechCrunch, 2013-03-08, via HN 5347093 — REPORTED, headline only, not fetched).

### 2.9 Supabase — the dashboard

**The artefact.** A table you can see, that already has an API.

**Primary source (MEASURED, HN 23319901, `Supabase (YC S20) – An open source Firebase alternative`,
2020-05-27, **1,120 points**).** The most instructive thing in the thread is not a claim — it is what
a co-founder posted:
> "[awalias] here's something small I [supabase co-founder] built with supabase as an example:
> Realtime Collaborative Task Lists https://todo-zeta.now.sh/"
> "[awalias] here's a list just for fun/chaos: https://todo-zeta.now.sh/?uuid=7e5217bf-…"

A live, shared, multiplayer artefact — posted *inside the launch thread*, on a URL anyone could open
and mess with together. Hundreds of readers experienced the product's core claim (realtime Postgres)
without signing up for anything.

**The moment.** You create a table in a GUI and a REST endpoint and a realtime subscription already
exist. Nothing was generated; it was always there.

**The smallest version at launch:** Postgres + PostgREST + an Elixir realtime server + a table
editor, hosted free, in alpha, with the co-founder answering every comment. The dashboard itself was
not open-sourced until 18 months later (`Supabase open sourced their dashboard`, HN 29401589,
2021-12-01, 331 pts, MEASURED).

### 2.10 Prisma Studio / Drizzle Studio — one command, your own data

**The artefact.** A GUI showing *your* rows, opened by a command in a repo you already have.

**Primary sources (both MEASURED, seen 2026-08-30):**
- `https://www.prisma.io/studio` — `npx prisma studio`. "A visual browser and editor for the data in
  your Prisma project."
- `https://orm.drizzle.team/drizzle-studio/overview` — `drizzle-kit studio`. No account. "Free
  forever." And the sentence that is the whole design: it **"grabs everything it needs from your
  `drizzle.config.ts` file."**

**The moment.** Zero configuration, because the configuration was already on disk. You did not tell
it your connection string; it read the file you already wrote.

**The smallest version at launch:** exactly that. One command, existing config, a browser tab.

**The lesson we can steal:** the highest-allure onboarding is not "short." It is *derived*. Anything
we ask the user to type that we could have read off their repo is a tax on the moment. (We already
do this in one place — `lib/preview.mjs` discovers the PR's preview URL from the deployments API
instead of asking. It should be the rule, not an exception.)

### 2.11 Warp — the anti-case-study

**Warp is in this list because it is the only one that had a real artefact and destroyed the
mechanic itself.** Warp's blocks (command + output as one selectable, shareable unit) are a genuinely
new terminal primitive. It put an account wall in front of them.

**Primary sources (MEASURED, HN):**
- id 33960005, 2022-12-12: *"[hintymad] Warp requires a sign-up before I can try the terminal. This
  can be a big no no for many users, or at least for me."*
- id 42247583, `Warp terminal – no more login required`, 2024-11-26, 73 pts / 107 comments — the
  post announcing the wall's removal. Top comments, in order:
  > "[user432678] Too late."
  > "[jakebasile] …being asked to sign in. **I have never uninstalled a program faster in my life.**"
  > "[InfiniteVortex] The fact that they even required a login in the first place is crazy for a
  > terminal."
  > "[20wenty] The damage is done. You can't overthrow the king… **if you have to sign up to get
  > past the castle gates.** And in the two years it took to remove the signup, the townsfolk have
  > realized their kings are good enough for what they want to do anyway."

**The lesson.** Two years of an account wall converted a novel artefact into a permanent reputation.
The people in that thread were not describing a feature gap; they were describing a grudge. Our
`npx` + no-account + `--share`-is-openable-by-strangers posture is not a nice-to-have, it is the
thing that has to survive every future product decision. (`RED_TEAM.md` §2.2 is right that
npx-no-account is not a *differentiator* — Shortest gave it away. This is a different claim: it is
not a differentiator, it is a **precondition**. Removing it is one of the few unforced ways to kill
this outright.)

### 2.12 Raycast — the mechanic, not the launch

**The artefact at launch:** a launcher. **The artefact that actually built the company:** somebody
else's extension, with their name on it.

**Primary sources (MEASURED):**
- HN 24932600, `Show HN: Raycast (YC W20) – Spotlight for Developers`, 2020-10-29: **18 points.** The
  launch, by any HN measure, did not land.
- HN 29395720, `Show HN: Extend Raycast with React and TypeScript`, 2021-11-30 — the store, thirteen
  months later.
- `https://www.raycast.com/store`, seen 2026-08-30: the store paginates to **page 322**; the top
  extension shown ("Kill Process") reads **684,457** installs, second ("Color Picker") **525,802**.

**The moment.** A developer writes a fifty-line extension in an afternoon, publishes it, and other
developers install it. Using the product produced a public artefact carrying the author's name — and
every one of those artefacts is a landing page for Raycast that Raycast did not write.

**The lesson we can steal, and it is the one that matches our binding constraint exactly:** the
distribution mechanic was not the launch and was not outreach. It was *a thing users made, in
public, by using the product.*

### 2.13 The pattern, extracted

Every case above shares five properties. This list is the scoring rubric for §4.

1. **The artefact is an object, not a capability.** A URL. A number. A zip. A dashboard row. A
   button labelled *Break the World*. You can point at it.
2. **It appears on the user's own material within a minute.** Bun on your `package.json`. Vite on
   your project. Drizzle Studio on your config. Stripe's docs with your key. Never a sandbox, never
   a demo account.
3. **The telling is executable.** "Run `bun install` on your repo." "Drag the zip onto this page."
   "Type `npx prisma studio`." A recommendation the listener can act on in sixty seconds spreads;
   one that requires a signup, a call or a migration does not.
4. **It is contrasted against a specific present pain.** 176ms → 7ms. 34s → 0.3s. 30-second Jira
   page loads. "Errors in a dashboard instead of a log file." Absolute goodness is not persuasive;
   a delta against today is.
5. **The ones that became *distribution* rather than merely *delight* produced an artefact that
   survives leaving the team** — the preview URL a designer opens, the Sentry issue link, the
   Raycast extension in a public store, the Supabase demo URL pasted into a launch thread. The ones
   that stayed inside the team (Linear's speed, Warp's blocks, Vite's dev server) grew by word of
   mouth, which is slower and needs the audience to already exist.

**And two anti-properties:**

6. **A wall in front of the artefact deletes it** (Warp, §2.11), and the reputational damage
   outlives the wall by years.
7. **Launch-day score is not the signal.** Raycast 18, Linear 3, Prisma Studio 5. If our launch post
   underperforms, that is information about the post, not about the product — and the plan must not
   depend on a post landing.

---

## §3 — OUR FOUR ARTEFACTS, SCORED AGAINST THE RUBRIC

Before ranking candidates, an honest audit of what we already hold. The rubric is §2.13's five
properties.

| our artefact | is it an object? | on their own material? | is the telling executable? | contrasted against a present pain? | survives leaving the team? |
|---|---|---|---|---|---|
| **the SENTENCE** (`tests/*.md`, one line of English) | yes — a line in a file in their repo | yes | yes: "write the sentence, run it" | yes: against a 40-line spec with selectors that break | **yes — anybody can read it, including a founder** |
| **the VERDICT** (5 statuses, `flaky` never a pass) | no — it is a property | yes | no | yes: against a red build you cannot explain | only inside a page or a PR comment |
| **the RECORDING** (plain JSON, replays with 0 model calls) | yes, but an invisible one | yes | no — nobody has ever forwarded a JSON file | yes, economically | no. Its *consequence* travels; the file does not |
| **the SHARE PAGE** (`--share` → a URL, unfurls with the verdict and the sentence) | yes | yes | **not yet — see §4 C1** | yes | **yes, and it is the only one of the four that can** |

Read across the bottom two rows and the conclusion is forced: **the sentence is our most legible
artefact and the share page is our only travelling one, and today they are not wired to each
other.** MEASURED, `app/s/[id]/page.tsx` today: the share page's footer contains the string
`npx smolanalytics test --share` as *prose in a sentence about provenance*. There is no copyable
command, no "run this on your own app", nothing a reader can execute. The page is a beautiful
account of somebody else's run and a dead end.

That single gap is the highest-leverage thing in this document.

---

## §4 — OUR MOMENT, AND THE CANDIDATES RANKED

### 4.0 The moment, in one sentence

> **You type one English sentence about your own app; forty seconds later you hold a URL that a
> person who has installed nothing can open, and on it is your sentence, a real browser's account of
> trying to do it, and whether it worked.**

The structural analogue is Vercel's preview URL (§2.1), not Playwright's trace (§2.6). Vercel's
artefact escaped the engineer — a designer could open it. Playwright's did not and was never meant
to; a trace needs a developer. **Our sentence is the thing that makes our verdict escape the
engineer**, and that is the only property we hold that nobody else in `FIELD_AGENT_FIRST.md` holds,
because everyone else's artefact is code, a video, or a hosted scenario in their dashboard.

**The smallest version of it** — mirroring §2's discipline, where every winner launched one artefact
and not a suite — is *already built*, minus one paragraph of HTML. That is the whole finding.

### 4.1 The ranking

Metric: **desire created per week of work**, where desire = (probability an engineer tells one more
person) × (whether that telling can reach outside their team), and where the constraint is explicit:
*a mechanic where using the product creates its own distribution beats one needing outreach.*

| # | candidate | work | self-distributing? | desire/week |
|---|---|---|---|---|
| 1 | **The share page becomes runnable** | 2–4 days | **yes, strongly** | **highest** |
| 2 | **The receipt: `no model calls` as the headline number** | 3–5 days | partly (screenshot) | high |
| 3 | **`--break`: our *Break the World* button** | 3–5 days | weakly (it is a demo, not an incident) | high |
| 4 | **The suite page as a list of verified promises** | ~1 week | yes | high |
| 5 | **The English-diff pull request** | 1.5–2.5 weeks | yes, and it is permanent | medium-high |
| 6 | **Zero-argument `npx smolanalytics test`** | 1–2 weeks | no | medium-high |
| 7 | **The sentence gallery (`/tests/…` pages)** | 3–5 days | no (needs traffic we lack) | medium, but cheap |
| 8 | **`--since` receipt on the PR comment** | days (mostly shipped) | no | low desire, high retention |
| 9 | **MCP / in-editor** | 1–2 weeks | no | low on *this* metric |

> **Amended by §7.5 (read it before acting on this table).** A second pass tested the share-link
> mechanic against primary sources and found one condition this ranking never scored: *is there a
> reason to send the page **outside** the sender's team?* C4 is the only candidate that creates one,
> which moves **C4 from rank 4 to rank 2**. C1 stays rank 1 — it is the precondition — and §7.6 adds
> one editable field to its spec at no extra cost.

---

### C1 — The share page becomes runnable *(2–4 days · rank 1)*

**The change.** Every share page gains one copyable block, above the fold, under the sentence:

```
npx smolanalytics test --url https://your-app.com --test "the cart shows one line for that product at the price the product page listed"
```

— with the sentence taken verbatim from the page being read, and a copy button. Plus one line of
prose: *"That is the whole test. Change the URL to your own app and run it."*

**Why it is rank 1.** It converts the artefact we already publish from a *report* into an *entry
point*, at a cost of days. It is Stripe's move (§2.8 — the docs contain a request that runs) and
Postman's fork button (`CROSS_POLLINATION.md` §1). It satisfies rubric property 3 — *the telling is
executable* — which is the property separating tools that spread from tools that are merely liked,
and it is the only property of the five our share page currently fails.

**C1b, same sprint, hours not days.** When a run fails and `--share` was not passed, print one line:

```
→ someone needs to see this and cannot run it? re-run with --share for a link they can open.
```

This does **not** touch `lib/share.mjs` rule 1 (OPT-IN, ALWAYS — nothing leaves the machine unless
someone typed `--share`). That rule is correct and traces to Replay.io's own post-mortem, quoted in
the file's header. What C1b does is offer the share at the moment of maximum motivation: a failure
somebody has to explain to another human. Publishing on failure by default would be a policy change
and should be refused.

**How we know if it worked.** Server-side, and it needs no tracking of readers: *unique openers per
share*, and *shares published per installing account*. A share opened once (by its publisher) means
`--share` is a nicer terminal output. A share opened five times means the artefact left the team.
That is the number that tells us whether any of this is real, and today we do not have it.
An optional referral nonce in the copied command would close the loop from read→run, but it must be
visible in the string the user copies and named in the docs; a hidden one contradicts everything
else about how this product behaves.

**Adversarial, because this is rank 1 and deserves the hardest look.** The obvious counter is that
share links get pasted into a *private* Slack, to three people who already work with the person who
installed it — which makes `--share` a retention feature wearing an acquisition costume. That
objection is correct about `--share` *as it exists today*, and it is exactly what C1 fixes: the
third colleague who opens the page is not a new customer, but they are a new *installer* the moment
the page hands them a runnable line, and they are the person most likely to have their own app and
their own frustration. It is also the reason C1 is worth days and not weeks: the loop is
plausible, not proven, and the cheapest version that can be measured beats the elaborate one.

---

### C2 — The receipt: make `no model calls` the headline *(3–5 days · rank 2)*

**What exists (MEASURED today).** `lib/cost.mjs` counts from the API's own `usage` block and never
estimates; `lib/test.mjs:1187` prints `PASS — replayed N steps in X.Xs, no model calls.`;
`lib/suite.mjs:726,1067` say `N of M ran from a recording, with no model calls.` The artefact is
built. It is not the headline anywhere.

**The change.** State it as a **delta on the user's own run**, the way Bun stated 176ms → 7ms:

```
first run    4 model calls · 21,430 in / 1,205 out
every run after   0
```

**Why this is the right number and the parallelism number is not.** Rubric property 4: the contrast
must be against *a present pain the reader feels*. Our buyer's live fear, per `GRAVEYARD_AND_BUYER.md`
and the entire pricing half of `FIELD_AGENT_FIRST.md`, is not "my tests are slow" — it is *"I am
handing my Anthropic key to a loop that runs on every pull request and I do not know if that is $5
or $500."* `lib/cost.mjs`'s own header says exactly this, and `--max-calls` is the answer to it. A
run that reports zero, from a tool that could have reported anything, is the most persuasive
sentence we can print, and it prints itself.

**The reproducibility test it passes and the benchmark fails:** a stranger can verify it with one
`npx` command run twice. That is Bun's actual mechanic (§2.3) — not the size of the ratio, but that
the reader can produce it themselves in a minute on their own material.

---

### C3 — `--break`: our *Break the World* button *(3–5 days · rank 3)*

**The move, taken directly from Sentry (§2.7).** Sentry's onboarding does not ask to be trusted; it
tells you to add `<button onclick="triggerError()">Break the World</button>` and click it. The tool
proves itself, on your app, on demand, by breaking something on purpose.

**Ours, and it is one nobody else in this field can run.** We own `lib/render.mjs` — the guard that
turns a would-be PASS over a blank / unstyled / crashed page into a `failed`. So:

```
npx smolanalytics test --url https://your-app.com --test "..." --break css
```

runs the user's own passing test against their own app with the stylesheets blocked, and shows the
guard catching a page whose `innerText` still contains every string a text-based assertion would
have matched. The output is a share page reading, in effect: *green under the rules every other
runner uses; red under ours, and here is the screenshot.*

**Why it is high-desire.** Churn reason #1 in this category is "the tool is less trustworthy than
the app" (`GRAVEYARD_AND_BUYER.md`). A tool that answers that with an *experiment on your own app*
rather than a claim is doing something no comparison table can do. And `lib/render.mjs`'s header
already documents the three exact cases (CSS 404, empty root with an off-screen node — MEASURED at
32 characters of innerText, 0 painted — and a Next.js error overlay in a shadow root), so the
demonstration is grounded in a measurement we already took.

**Why it is rank 3 and not rank 1.** A manufactured failure is a *demo*, and demos travel worse than
incidents. Nobody forwards a link that says "I broke my own CSS on purpose." It is the best first-
hour experience we can build; it is not, by itself, a distribution mechanic. It must also be
labelled on the page as deliberately manufactured — a share page showing a red verdict caused by us
would otherwise be the single most dishonest artefact this product could emit.

---

### C4 — The suite page as a list of verified promises *(~1 week · rank 4)*

Largely built: a suite run already produces **one** page carrying up to 200 tests with per-test
statuses (`lib/share.mjs`, `MAX_TESTS = 200`, "one link per run, never per test" — MEASURED). The
work is presentational, and the presentation is the product: the page should read as **a list of
things this app promises, each with a tick and a date**, not as a test report.

That is the artefact a founder screenshots and a candidate reads. It is `CROSS_POLLINATION.md` §2's
"verified-promises page" and it is the same object Supabase put in its own launch thread (§2.9): a
live URL a stranger can open that demonstrates the core claim without signing up.

**The adversarial note:** `CROSS_POLLINATION.md` Trap 1 correctly kills the *public board of app
test health* — a directory we host, ranking other people's apps. This is not that. This is one
customer's own page, published by them, deletable by them with the token they already hold
(MEASURED, share page footer). The difference between the good version and the trap is who chooses
to publish and who can delete.

---

### C5 — The pull request whose entire diff is English *(1.5–2.5 weeks · rank 5)*

**What exists.** `lib/suggest.mjs` (674 lines) crawls the app with a real browser and writes
`tests/*.md` in the exact format `test --suite` runs, with the anti-hallucination rule that a
proposal whose quote appears on no visited page is dropped out loud (MEASURED). It does not open a
pull request.

**The artefact.** A diff a human reads end to end, containing **eight sentences of English and zero
lines of code**, plus one share link in the body showing all eight run green against the PR's own
preview URL (which `lib/preview.mjs` already discovers without being told). On a public repo it is
permanent and strangers see it. This is the Raycast-store shape (§2.12): *a thing users make, in
public, by using the product* — the one mechanic in §2 that was neither a launch nor outreach.

**Two adversarial notes that change the design.**
1. Our buyer's repos are mostly private, so the public surface is far thinner than Raycast's store.
   What survives is the *screenshot* of that diff, which is the most tweetable image this product
   can produce — and it is produced by a customer, not by us.
2. Opening the PR ourselves needs a token or an App, and `SCORECARD.md` row 13 says the no-App
   posture is the row that decides whether evaluation happens at all. **So do not open the PR.**
   Write the files, print the `gh pr create` line with a body already composed, and let the user (or
   the coding agent already sitting in their repo) run it. Cheaper, and it keeps the posture.

---

### C6 — Zero-argument `npx smolanalytics test` *(1–2 weeks · rank 6)*

The Drizzle Studio move (§2.10): *"grabs everything it needs from your `drizzle.config.ts` file."*
The highest-allure onboarding is not short, it is **derived** — anything we ask the user to type
that we could have read off their repo is a tax on the moment.

We already do this in one place: `lib/preview.mjs` finds the PR's preview URL from the deployments
API the job's `GITHUB_TOKEN` can already read, and **never guesses**, with no fallback to production
(MEASURED). Extending the same discipline to a local invocation — read `tests/` if it exists, read
the dev URL out of the project's own config — would make `npx smolanalytics test`, with no flags at
all, do something correct.

**The refusal that has to come with it:** `preview.mjs`'s rule is the right one. A wrong guess on
somebody's first command is fatal, so the failure mode must be *"I found two candidate URLs, which
one?"* and never a silent pick. Ranked 6 because it is a week or two for an improvement to a first
run that is already sixty seconds — real, but it is polishing a moment that already works rather
than building one that does not exist.

---

### C7–C9, briefly

- **The sentence gallery** (3–5 days): pages whose body is a runnable sentence — *"the 30 sentences
  every SaaS should pass"*, each with a copy button and an `npx` line. `CROSS_POLLINATION.md` §6.
  Cheap and compounding, but it is content, content needs traffic, and traffic is the binding
  constraint. Build it *after* C1, so share pages can link into it.
- **`--since` receipt on the PR comment** (days, mostly shipped): "47 of 50 skipped, and here is the
  string that connected each of the 3 we ran." Engineers love a tool that shows its work. It
  delights an existing user and creates no new one — high retention value, near-zero desire value.
- **MCP / in-editor** (1–2 weeks): a capability, not an artefact. There is nothing to point at and
  nothing to forward. It may well be right for other reasons; on *this* metric it ranks last.

---

## §5 — ADVERSARIAL: the two that sound exciting and would not move anybody

Both of these are things a founder in this position wants to build, both are real, and both are
dead ends for desire. They are named here so that wanting them later is a decision rather than a
drift.

### TRAP 1 — the parallelism benchmark: *"50 end-to-end tests in 4.9 seconds"*

**Why it is seductive.** It is literally the Bun move (§2.3). The number is real, it is ours, it was
measured properly (`lib/pool.mjs` header — one browser + a context per worker, three repeat runs at
the default, 50/50 passed each time), and it is the kind of thing that gets a screenshot.

**Three reasons it moves nobody, each fatal alone.**

1. **The audience cannot reproduce it.** Bun's 176ms → 7ms worked because every reader has a
   `package.json` and runs `npm run` forty times a day; the reader *becomes* the benchmark in one
   command. Nobody on earth has a fifty-test smolanalytics suite. Our number is a private fact about
   a fixture — and `pool.mjs`'s own header says so: *"on this machine (8 cores, 17.2GB) against a
   50-flow fixture."* A benchmark a reader cannot re-run is a claim, and this category is already
   drowning in claims (`FIELD_AGENT_FIRST.md` pattern 3).
2. **It invites the comparison we lose.** 4.9s is *our parallel mode against our serial mode*. The
   comparison every reader silently makes is against Playwright, and fifty Playwright specs on eight
   workers will beat us — theirs was always deterministic code, ours is a replay plus a browser walk.
   Publishing the number recruits the wrong yardstick.
3. **Speed is not a churn axis here.** `GRAVEYARD_AND_BUYER.md`'s tally across 38 negative reviews is
   flake (32%), coverage walls (29%), meters, and black-box verdicts. *Slow* does not appear. We
   would be optimising the message for the one axis nobody cancels over.

**What to do instead.** Keep **39.2s → 4.9s** as a docs fact so a CI-minded engineer can tick the
box, quoted with the machine and the fixture attached. Never the headline. Never a launch.

### TRAP 2 — the agent-driving-the-browser demo (the video, the GIF, the hero animation)

**Why it is seductive.** It is the visually impressive half, it is the part that is genuinely new,
and it is the thing anybody who builds this wants to show a stranger.

**Four reasons it moves nobody.**

1. **It is the category's wallpaper.** `FIELD_AGENT_FIRST.md` documents Autonoma, Momentic,
   TestDriver, Stably, Spur, Donobu, Midscene, Shortest, Hercules and auto-playwright all leading
   with exactly this artefact. A viewer who has seen two has seen ours. Indistinguishability is the
   precise opposite of allure.
2. **It fails rubric property 2 outright.** Every winner in §2 got its moment *on the user's own
   material* — your `package.json`, your config file, your API key, your app. A demo video is a
   stranger's app doing a stranger's thing, and no amount of production value fixes that.
3. **It advertises the expensive half and teaches the wrong price.** The agent loop is the slow,
   paid, once-per-test path; our actual economic argument is the replay, which is visually boring —
   a terminal line reading `no model calls`. A video that stars the agent trains the market to price
   us as an agent product, which is the meter that killed Octomind ($89/mo for **20 AI test
   creations**, `RED_TEAM.md` §2.1).
4. **Eight companies shipped that video and are dead, dormant, absorbed or pivoted.** It is the most
   thoroughly falsified marketing artefact in this entire research directory.

**What to do instead.** If a moving image must exist, **film the second run** — the one where the
recording replays, nothing thinks, it costs nothing and it is over in 1.4 seconds. That is the
asset no competitor can film, because their runtime *is* the model.

### Honourable mention — *"we found a bug in a famous open-source app"*

Tempting, and it is outreach wearing a lab coat: one-shot, non-compounding, stale the moment the
maintainer fixes it, and it puts us in the position of publicly grading our buyer's peers. Worse, it
fails the incident test from `GRAVEYARD_AND_BUYER.md` §4: purchase in this category is triggered by
*the reader's own* incident. Somebody else's bug is not one.

---

## §6 — SYNTHESIS

### What the case studies say we should do, in order

1. **C1 — wire the sentence to the share page (2–4 days).** We already publish the only artefact in
   this field that survives being forwarded to a person who cannot read code. It currently ends in a
   dead end. Give it a runnable line and a copy button, and add the one-line prompt on an unshared
   failure (C1b). This is the entire §2 pattern applied to something already built.
2. **C2 — make `no model calls` the headline number (3–5 days),** stated as a delta on the reader's
   own run. Our Bun number is the one about their bill, not the one about our clock.
3. **C3 — `--break` (3–5 days).** Sentry's *Break the World* is the most under-copied idea in
   developer tools, and we hold the one guard (`lib/render.mjs`) that makes it mean something in this
   category.
4. **C4 — the suite page as a list of verified promises (~1 week).**
5. Then re-measure before spending the 1.5–2.5 weeks on C5.

**Amended by §7.5.** On the evidence in §7, the order should be **C1 → C4 → C2 → C3**. C4 is the
only item on this list that gives the page a reason to be sent to somebody who is not on the team,
and without that, C1 makes a runnable page that only teammates ever run. C2 and C3 are excellent
first-hour experiences and neither one travels.

Everything above is under two weeks combined for items 1–3, and none of it requires outreach, a
launch post landing, or an audience we do not have.

### The one number that decides whether any of this is true

`RED_TEAM.md` §2.3 is right that npm downloads measure curiosity: `auto-playwright` pulls 13,207/wk
into a repo untouched for thirteen months. Ours today is **1,191 downloads in the week to
2026-08-28** (MEASURED, `api.npmjs.org`), and it means nothing on its own.

The metric this document proposes, which we cannot currently compute, is:

> **unique openers per published share.**

One opener is the publisher: `--share` is a nicer terminal output. Five openers means the artefact
left the team, which is the only mechanism in this plan that does not require outreach. It is
measurable server-side, requires no tracking of readers, and it either validates the whole §4
ranking within a month of C1 shipping or kills it cheaply.

### The one sentence to hold on to

Every tool in §2 won because a developer could hold something and hand it to somebody else. We have
the rarest version of that in this entire category — **a test that a non-engineer can read** — and
we are currently printing it into a terminal.

---

## §7 — THE SHARE-LINK LOOP, TESTED AGAINST PRIMARY SOURCES

**Why this section exists.** §4 ranks C1 first, and C1 rests on one unproven belief: *that a page
published by a user acquires another user.* The whole document's constraint — "a mechanic where
USING the product creates its own distribution beats one needing outreach" — stands or falls on it.
So this section studies the mechanic itself rather than the moment, using tools whose artefact is,
like ours, **a URL a stranger opens**. It ends by correcting §4's ranking.

### 7.1 Excalidraw — the link that needs no account (the positive form of the Warp lesson)

Hacker News, **`Why is Excalidraw so good?`, 2021-11-04, 738 points** (MEASURED, HN 29109995, id and
score read from the Algolia API today). Note what that thread is: not a launch, not an
announcement — *a developer writing an essay about why he wants a tool*, upvoted 738 times. It is
the closest thing in the industry to a primary source on allure itself.

**The essay (MEASURED, `https://offbyone.us/posts/why-is-excalidraw-so-good/`, fetched
2026-08-30).** Its answers, verbatim:

> "There's no onboarding. No signup. No confirmation email. No OAuth. You're just in the product."
> "It's not overwhelming. There are 9 tools. The icons tell you what they do."
> "**Just send a link to your coworker. They don't have to sign up.** They just click the link, and
> collaborate."
> "Don't have to worry that a SaaS company will spam them for the rest of their life."

Two comments in the thread matter more than the essay.

- **The skeptic concedes exactly one thing.** `[systemvoltage]` — the same commenter who wrote the
  six-phase Tailwind adoption arc in §2.5 — dismisses the whole premise: *"This just reads like a
  hipster fetish over aesthetics, not substance… I much prefer Monodraw."* And then: *"That said,
  the app itself has great UX, **especially onboarding. Just straight to drawing mode.**"* (MEASURED,
  HN 29109995.) A hostile reader who rejects the aesthetics still hands over the frictionlessness.
  That is what a durable property looks like: it survives the person who dislikes your product.
- **The frictionlessness reads as a *restoration*, not an innovation.** `[kazinator]`, quoting the
  essay's no-signup line: *"You mean like pretty much every single darned productivity program
  before year 200X? **It's sad that this even has to be an issue.**"* (MEASURED, same thread.)

**INFERRED, and it is the sharpest thing in this section:** no-account is not a feature you get
credit for. It is a tax you avoid paying. Warp (§2.11) shows the penalty for levying it — a
two-year grudge — and Excalidraw shows the reward for not: a mild, universal *"finally"*. Nobody
will tell a friend "and you don't even need an account." They will simply forward the link, which is
the only behaviour we need.

### 7.2 Chromatic — the counter-case, and it is in our own category

The closest commercial analogue to our share page is not a terminal or a whiteboard. It is
Chromatic: a testing company whose central artefact is **a URL a designer or PM opens to look at a
build they cannot run**.

**Primary source (MEASURED, `https://www.chromatic.com/docs/publish/`, fetched 2026-08-30):**
> Chromatic creates a permalink per branch: `https://<branch>--<appid>.chromatic.com`
> "**Published Storybooks are private by default with access restricted to logged in
> collaborators.**" Visibility "can be set to public if desired."

**What this does to our thinking, in both directions, and both are uncomfortable.**

1. **Our open link is a genuine position, not a shared default.** The most successful company
   shipping this exact artefact ships it *closed* and makes you opt in to open. Our
   `/s/[id]` is open with no opt-in to close (MEASURED today, see §8), which is a real difference —
   and a real exposure, which is why `lib/share.mjs`'s three masking passes are load-bearing rather
   than decorative.
2. **And: gating the link did not stop Chromatic from being a business.** So an open link is not a
   survival requirement. It is an *acquisition bet*, and it must be judged as one — by the
   unique-openers-per-share number §6 proposes, and by nothing else. If that number comes back at
   1.0, the honest read is that we have built Chromatic's feature with more risk and no benefit, and
   the correct response is to make private-by-default an option rather than to write more copy.

### 7.3 ngrok — what actually travels is the URL's *function*

**Primary source (MEASURED, HN 14278703, `Ngrok: Secure tunnels to localhost`, 2017-05-06, 468
points; the creator answering in-thread: `[inconshreveable] Hiya there folks - I'm the creator of
ngrok, happy to answer any questions`).**

The user testimony in that thread never mentions sharing with a human:
> "[yeldarb] Happy paying ngrok user here. Love it for developing anything using **webhooks** and
> also hybrid mobile apps (I have my app pull the JS from the Dev box I'm working on via ngrok
> without having to rebuild the app or deploy the code anywhere)."

And the dissent, which is the useful half:
> "[packetized] Literally the most terrifying service for any security-minded operations-focused
> person… I've had some real horrific moments when users told me that they installed it to allow
> access to their (private) repos for testing."

**The lesson, INFERRED:** ngrok makes a public URL and has almost no viral loop, because the thing
on the other end of its URL is *a machine* (Stripe's webhook sender), not a person who might want
ngrok. A public URL is not a distribution mechanic by itself. It is one only when a **human with
their own version of the sender's problem** is at the other end. That is the test C1 has to pass,
and §7.5 is where our page fails it.

### 7.4 The Loom loop is folklore, and this document will not lean on it

Every growth essay in this space cites Loom: *every shared video is a landing page, the recipient
signs up to reply, 25M users without marketing.* It is the single most-cited proof that
using-the-product-is-distribution works.

**I could not find a primary source for any of it** (MEASURED as a negative: searched today;
everything returned was third-party growth blogs — `startupspells.com`, `marketergems.com`,
`pipelineroad.com`, `tella.com` — retelling each other, no Loom-authored post, no dated metric, no
filing). **Status: unverified, and deliberately not promoted.** The one hard fact adjacent to it is
the 2023 Atlassian acquisition, which is REPORTED and says nothing about the mechanic.

**Why naming this matters more than it looks.** C1 is rank 1 in this document, and the intuition
behind it is Loom-shaped. Stripping the folklore out leaves C1 resting on exactly three verified
things: Excalidraw's no-signup link (§7.1), Vercel's preview URL escaping the engineer (§2.1), and
Stripe's runnable example (§2.8). That is still enough to justify 2–4 days of work. It is not enough
to justify betting the roadmap, and §4's insistence that C1 be the *cheapest measurable version* is
retroactively the right call for a reason it did not state.

### 7.5 The four conditions, and our page scored against them

INFERRED from §2.1, §2.6, §2.8, §2.11, §7.1–7.3, and stated so it can be falsified. A published link
distributes only when all four hold:

| # | condition | Vercel preview | Playwright trace | Excalidraw | Chromatic (default) | ngrok | **ours today** |
|---|---|---|---|---|---|---|---|
| a | opens with **no account** | yes | yes | yes | **no** | yes | **yes** (MEASURED, §8) |
| b | **legible to a non-specialist** | yes | no | yes | yes | n/a | **yes — the sentence** |
| c | the reader can **do something from the page** without installing | no | yes (drag another trace) | yes (edit it) | no | no | **NO** |
| d | there is a reason to send it **outside the sender's team** | yes (the designer must approve) | rarely | yes | no | no | **partly** |

- **(c) is C1, and C1 is right.** It is the only row where a competitor-grade tool beats us and the
  fix costs days.
- **(d) is the row §4 does not address at all, and it is the one that decides whether any of this is
  acquisition.** A run page is sent to people who already work on the app. Chromatic and ngrok both
  fail (d) and are fine businesses; neither grows by link. Vercel passes (d) for one reason — *the
  recipient has a job to do on the page* (approve the change) — and that is why the preview URL
  became the canonical example in this whole document.

**The ranking correction this forces (INFERRED, and it contradicts §4.1):** C4 — the suite page as a
list of verified promises — is the *only* candidate in §4 that creates condition (d). A failure
report is inward-facing by nature; "here are the eleven things this app promises, each with a tick
and a date" is a page whose natural recipient is a customer, an investor, a candidate, a buyer's
security reviewer — people outside the team who have a reason to look. **C4 should move from rank 4
to rank 2, above C2 and C3**, on the specific ground that it is the only work that makes our
artefact worth sending outward. C1 stays at rank 1: it is cheaper, and it is the precondition that
makes any outward send convertible.

### 7.6 The concrete upgrade to C1 that the evidence supports

§4's C1 spec is *a copyable command containing this page's sentence*. The evidence in §7.1 and §2.8
supports one step further, at roughly the same cost:

> The command block on the share page contains **one editable field: the URL.** A reader types their
> own app's address into it and the `npx` line below updates. Nothing is sent anywhere; it is
> string interpolation in the browser.

This is Stripe's move exactly (§2.8: *"Log in to see docs with your test key and data"* — the
example belongs to the reader, not to the vendor), obtained without an account because we need no
key. It converts the page from *a command you could copy* to *your command, already written*, and it
is the smallest possible version of condition (c): the reader **did something on the page**, and
what they did was aim our product at their own app. No backend, no state, no login.

**The refusal that comes with it:** the field must never pre-fill from anything about the reader, and
the page must not report that a command was generated. The moment this page starts observing its
readers it becomes the thing Excalidraw's essay is relieved not to be — *"don't have to worry that a
SaaS company will spam them for the rest of their life."*

---

## §8 — VERIFICATION PASS (second reader, 2026-08-30)

Everything below was re-checked independently of the pass that wrote §0–§6. Recorded so that a
future reader knows which claims have been touched twice.

**Held, and now double-verified.**

- **The parallelism correction is right.** `lib/pool.mjs:60–70` states, in its own header, `workers 1
  39.2s … workers 8 4.9s … workers 16 3.8s`, plus the turn at 16 (aggregate test time 39s → 53s) and
  `43% of ONE core` on the serial run. The brief's **"4.6s" appears nowhere in `lib/`** except
  `lib/watch.mjs:544`, where it is an example output string in a docstring. **Quote 39.2 → 4.9, with
  the machine attached.** CONFIRMED.
- **File inventory.** `pool.mjs` 405, `engines.mjs` 172, `upload.mjs` 502, `uploadsafe.mjs` 121,
  `seed.mjs` 366, `seedguard.mjs` 145, `render.mjs` 685, `auth.mjs` 549, `cost.mjs` 145, `share.mjs`
  937, `select.mjs` 405, `suggest.mjs` 678 — every line count in §0 CONFIRMED by `wc -l` today.
  `MAX_TESTS = 200` at `share.mjs:78`, sliced at `:729`. CONFIRMED.
- **The C1 gap is real and is one line.** `grep -n "npx"` over
  `smolanalytics-cloud/app/s/[id]/page.tsx` returns **exactly one hit, line 375**, and it is prose:
  *"Published from the command line with `npx smolanalytics test --share`."* No copy button, no
  runnable line, nothing addressed to the reader. CONFIRMED — this is the single highest-leverage
  gap in the document and it is a paragraph of HTML.
- **`cost.mjs` is the receipt §4-C2 says it is.** Its header, verbatim: *"I am handing my Anthropic
  key to a loop that drives a browser on every pull request across a team, and I have no idea whether
  that is five dollars a month or five hundred, and no way to stop it."* Plus *"TOKENS ARE REPORTED
  ALWAYS. DOLLARS ONLY WHEN THE PRICE IS KNOWN"* and *"THE CAP IS ON CALLS, NOT ON DOLLARS."*
  CONFIRMED.
- **Every HN quote spot-checked came back verbatim** (MEASURED, HN Algolia API, today):
  `[jakebasile]` *"I have never uninstalled a program faster in my life"* (2024-11-26, story
  42247583); `[ghayes]` *"even under attack, Linear is faster than JIRA"* (2022-10-14, story
  33199304); `[Escapado]` *"Dev Server cold starts went from 34s to 0.3s and build times from 1m20s
  to 9s so I won't complain!"* (2022-07-03, story 31967420); `[user432678]` *"Too late."*,
  `[InfiniteVortex]`, `[20wenty]` — all present in 42247583's top-level children in the order §2.11
  gives them. No quote in this document was found to be inaccurate.

**New, not previously checked, and load-bearing.**

- **Our share pages are genuinely open — verified at the middleware layer, not just the route.**
  `smolanalytics-cloud/middleware.ts` (46 lines, read in full today) contains **no authentication
  logic of any kind**: it is the AI-crawler reporter, it is inert unless `SMOL_ANALYTICS_HOST` and
  `SMOL_ANALYTICS_KEY` are set, and it always returns `NextResponse.next()`. `app/s/[id]/page.tsx`
  sets `robots: { index: false, follow: true }` — deliberately unfurl-able, deliberately not indexed.
  So condition (a) in §7.5 holds all the way down. MEASURED. This mattered because a single
  auth-gating matcher in `middleware.ts` would have silently made us Chromatic (§7.2), and no amount
  of correct code in `share.mjs` would have shown it.

**Corrections to §0–§6.**

- **Line-number drift in §4-C2.** The receipt strings are at `lib/test.mjs:1213` (not 1187) and
  `lib/suite.mjs:738` and `:1105` (not 726/1067). The strings themselves are exactly as quoted, and
  `lib/cost.mjs:107` (`if (!ledger || !ledger.calls) return "no model calls";`) is the source of
  truth for the phrase. Content CONFIRMED, addresses corrected.
- **§2.5's Tailwind origin citation is now known-bad, not merely unfetched.**
  `https://adamwathan.me/tailwindcss-from-side-project-byproduct-to-multi-million-dollar-business/`
  returns **HTTP 404** today (MEASURED). The claim that Tailwind began as a side-project byproduct
  stays **unverified**, and the dead URL should be cited as dead rather than as "not fetched."
- **Sentry public issue-sharing: do not assume it.** `https://docs.sentry.io/product/issues/`
  (fetched today) says nothing about sharing an issue by public link with someone who has no
  account. §2.7's argument does not depend on it, and no claim of the form "Sentry issue links
  travel outside the team" should be made from this document. Unverified.
- **The Vercel PR-comment timing question stays open.** Searched again today; current docs describe
  the Vercel bot's PR message as established behaviour, and nothing dated 2016–2018 surfaced. §2.1's
  *"the PR comment came later"* remains INFERRED from the 2016 launch thread's contents.

**Net effect on the argument:** nothing in §0–§6 was falsified. Two things were sharpened — the C1
gap is smaller than it looked (one paragraph, one file, one line number), and the C1 *rationale* is
thinner than it looked once the Loom folklore is removed (§7.4). One ranking change is proposed on
evidence: **C4 to rank 2** (§7.5).

---

## SOURCES

All fetched or read on **2026-08-30** unless noted.

**Our own code and services (MEASURED, read today).** `/Users/arjun/smolanalytics/cli` —
`package.json` (v0.16.1), `README.md`, `bin/smolanalytics.mjs`, and `lib/{pool,engines,upload,`
`uploadsafe,seed,seedguard,render,auth,cost,share,select,suggest,suite,test,preview}.mjs`;
`/Users/arjun/smolanalytics-cloud` — `app/s/[id]/page.tsx`, `app/s/[id]/opengraph-image.tsx`,
`app/s/[id]/shot/route.ts`, `app/api/share/route.ts`, `app/r/[code]/route.ts`, `lib/share-store.ts`.
Live checks by curl: `https://smolanalytics.com` → 200; `https://smolanalytics.com/r/test123` → 307;
`https://registry.npmjs.org/smolanalytics` → `dist-tags.latest = 0.16.0`;
`https://api.npmjs.org/downloads/point/last-week/smolanalytics` → 1,191 (2026-08-22 → 2026-08-28).

**Vendor primary sources (MEASURED, fetched today).**
- `https://vercel.com/docs/deployments/environments`
- `https://playwright.dev/docs/trace-viewer`
- `https://bun.sh/blog/bun-v1.0`
- `https://docs.stripe.com/api`
- `https://docs.sentry.io/platforms/javascript/`
- `https://cra.mr/sentry-from-the-beginning/` (David Cramer, primary, first-person)
- `https://www.prisma.io/studio`
- `https://orm.drizzle.team/drizzle-studio/overview`
- `https://www.raycast.com/store` (322 pages of extensions; top listing 684,457 installs)

**Hacker News threads (MEASURED, via the HN Algolia API today — points and dates as returned).**
- 11440224 — *Now: realtime Node.js deployments*, 2016-04-06, 288 pts (Vercel/Zeit launch)
- 33199304 — *Linear.app*, 2022-10-14, 212 pts · and 19696923 — *Linear: Fast Issue Tracking*,
  2019-04-19, **3 pts**
- 37422106 — *Bun 1.0 announcement*, 2023-09-07, 150 pts
- 31967420 — *Vite – Next Generation Front End Tooling*, 2022-07-03, 483 pts
- 25140604 — *TailwindCSS v2.0*, 2020-11-18, 927 pts
- 23319901 — *Supabase (YC S20) – An open source Firebase alternative*, 2020-05-27, 1,120 pts ·
  and 29401589 — *Supabase open sourced their dashboard*, 2021-12-01, 331 pts
- 24932600 — *Show HN: Raycast (YC W20) – Spotlight for Developers*, 2020-10-29, **18 pts** ·
  and 29395720 — *Show HN: Extend Raycast with React and TypeScript*, 2021-11-30
- 33960005 — *The Terminal for the 21st Century* (Warp), 2022-12-12, 17 pts ·
  and 42247583 — *Warp terminal – no more login required*, 2024-11-26, 73 pts / 107 comments
- 30083042 — *Playwright: Automate Chromium, WebKit and Firefox*, 2022-01-26, 383 pts
- 26299026 — *Prisma Studio*, 2021-03-01, **5 pts**

**REPORTED, headline only, not fetched (do not promote to fact):** TechCrunch 2013-03-08 *"Did PayPal
Just Clone Stripe's API Documentation?"* (HN 5347093); `adamwathan.me` *"Tailwind CSS: From
Side-Project Byproduct to Multi-Million Dollar Business"* (HN 24031290, 308 pts) — **the URL now
returns HTTP 404 (MEASURED 2026-08-30); cite it as dead, not as unfetched.**

**Added by the §7–§8 pass (all MEASURED 2026-08-30).**
- `https://offbyone.us/posts/why-is-excalidraw-so-good/` — fetched; the four verbatim quotes in §7.1.
- HN 29109995 — *Why is Excalidraw so good?*, 2021-11-04, **738 pts**; comments `[systemvoltage]`,
  `[kazinator]` read in full via the Algolia items API.
- `https://www.chromatic.com/docs/publish/` — fetched; per-branch permalink
  `https://<branch>--<appid>.chromatic.com`, *"private by default with access restricted to logged in
  collaborators."*
- HN 14278703 — *Ngrok: Secure tunnels to localhost*, 2017-05-06, 468 pts; `[inconshreveable]`
  (creator, in-thread), `[yeldarb]`, `[packetized]`.
- `https://docs.sentry.io/product/issues/` — fetched; **contains no public issue-share-link
  statement.** Recorded as a negative result so the claim is not invented later.
- Our own code, second reading: `cli/lib/{pool,cost,share,test,suite,watch}.mjs` by `wc -l` and
  `grep`; `smolanalytics-cloud/middleware.ts` in full (46 lines, no auth logic);
  `smolanalytics-cloud/app/s/[id]/page.tsx` (one `npx` mention, line 375, prose).

**Searched and NOT found — recorded as negatives so they are not re-assumed.** No primary,
Loom-authored source for the "every shared video is a landing page / 25M users without marketing"
loop (§7.4 — third-party growth blogs only). No dated Vercel changelog entry establishing when PR
comments shipped (§2.1 stays INFERRED).

**Unverified and deliberately left so:** whether Vercel's PR-comment integration (as opposed to the
immutable URL) shipped in 2016 or later — the §2.1 claim that the PR comment came afterwards is
INFERRED from the 2016 launch thread's contents, not from a dated changelog.
