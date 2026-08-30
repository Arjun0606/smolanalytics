# smolanalytics

**End-to-end tests without test code.** You write one sentence describing what should work. An
agent opens a real browser, works out how to do it, and returns a verdict. On a pull request, one
comment says what broke.

```sh
npx smolanalytics test --url https://yourapp.com --test "checkout works"
```

No account, no GitHub App, nothing written to your repository. It needs a URL you already have —
staging, a deploy preview, or localhost through a tunnel — your own `ANTHROPIC_API_KEY`, and that
sentence. Playwright is fetched on first use and nothing else is installed: this package has no
dependencies.

There is no test code to maintain, because there is no test code. When a button moves, there is no
selector to update — the agent looks at the page again and works it out.

**A run that passes is recorded, and the recording replays with no model calls at all.** The agent
comes back only when the recording stops fitting the app, which is exactly when judgement is worth
paying for.

## The commands

| | |
|---|---|
| `test --url <url> --test "<sentence>"` | one test, right now |
| `test --suite tests/ --url <url>` | a folder of tests; add `--comment` on a pull request |
| `suggest --url <url>` | walk the app and write the tests worth having into `tests/*.md` |
| `audit` | read the repo you are standing in and name the user actions nothing measures |
| `init` / `connect` / `plan check` | the tracking half — see [The tracking half](#the-tracking-half) |

## What it costs to run

The runner executes on your machine or your own CI runner, on your own model key, so the cost is
yours and it is small. A pass is recorded once and replayed for free afterwards. One measured flow
on one app: 8.0s with the agent, 1.4s replayed — the shape is the claim, not the figure.

## End-to-end tests

One sentence, a real browser, a verdict. Sixty seconds from nothing, with no account:

```sh
npx smolanalytics test --url https://yourapp.com --test "the pricing page shows a monthly price"
```

The URL is one you already have — a Vercel, Netlify, Fly or Render preview, or staging. An agent
opens the page, reads it as roles and names rather than screenshots, uses it the way a careful
person would, and says what it saw:

```
  ✓  1 click link "Pricing"        412ms
  ✓  2 click button "Monthly"      288ms

PASS · 2 steps · 11.4s
The pricing page lists $29 a month under Pro.
```

Driving a browser needs Playwright and Chromium, so both are fetched the first time you run this
one command: Playwright into `~/.cache/smolanalytics`, Chromium into the browser cache Playwright
keeps for every project on the machine. Nothing is written to your project. Every other command
here still has zero dependencies.

**The second run is free.** Add `--plan` and a passing run is recorded, then replayed with no model
calls at all — fast enough that the model is the cost, not the browser (one measured flow: 8.0s with the agent, 1.4s replayed; yours will differ). The agent wakes again only when the recording
stops fitting the app.

### A suite

Tests live in a folder of markdown files. A heading is a test, and the sentence under it is the
whole test.

```md
## A shopper can add an item to the cart

From the storefront, open the first product, add it to the cart, and check that the cart shows one
line for that product at the price the product page listed.
```

```sh
npx smolanalytics test --suite tests/ --url https://yourapp.com
```

Each test gets its own recording under `.smolanalytics/recordings`, named after the file and the
heading it came from. Rename a heading and that test is recorded again from scratch; edit the
sentence and the next run checks the new sentence.

This package ships `templates/example-test.md`, a working checkout suite to start from.

A suite runs several tests at once, each in its own browser context inside one shared browser, and
still prints one test at a time: each test's output is held and written as a block when that test
finishes. The order of the summary, the pull request comment and the exit code is the suite's own
order either way. How many run at once is measured from the machine — cores, memory, and whether
`ANTHROPIC_API_KEY` is set, since a first run is that many agents talking to the model at once.
`--workers 1` runs them one at a time, exactly as before. Fifty recorded tests on an 8-core laptop:
about 39s at `--workers 1`, about 5s at the default — measured across several runs, which land
between 4.6 and 5.4 seconds. The shape is the claim, not the decimal: a suite that took most of a
minute takes a few seconds, and your machine will give you your own number.

`--since main` runs only the tests the change could have broken. Each test's recording says which
controls it clicks, what text it fills, which paths it visits and the text it proves itself with;
`--since` intersects that with `git diff`, and a test whose recording touches nothing in the diff
does not run. It is off unless you ask for it, and being wrong in the two directions is not the
same thing, so it is deliberately biased: a test with no recording always runs, a recording it
cannot read always runs, and if git is missing, the ref does not exist, the clone is too shallow to
find a merge base or the diff cannot be read, the whole folder runs and the run says why. What was
skipped is named every time — in the terminal and in the pull request comment — because a run that
quietly checked twelve of fifty tests and printed "12 passed" is a suite lying about itself.
Skipped is not passed: those tests are in no count, in no row and in no exit code.

The trade, stated plainly: the match is textual, so a change that shares no text with a test can
still break it — a refactor of the price helper does not contain the word "checkout". Run without
`--since` when you want the whole folder checked.

### The data a test creates

The agent really uses the app. A sentence about signing up creates an account; one about checking
out can create an order. So every run carries its own obviously-synthetic identity, and you can
write it straight into the sentence:

```md
## A new customer can sign up

Sign up as {{email}} with the password {{password}}, then check the account page greets {{name}}.
```

The placeholders are `{{email}}` `{{password}}` `{{name}}` `{{username}}` `{{runid}}`, replaced
before the sentence reaches the model. Every value starts with `smoltest` and carries the same run
id — `smoltest+mfz01abc@example.com`, `smoltest_mfz01abc` — so one `LIKE 'smoltest%'` finds every
row any run ever made, in any column. The default domain is `example.com`, reserved by RFC 2606:
a test signup can never land a "welcome!" in a real inbox. If your form rejects it, point
`--email-domain` at a catch-all you own.

**A production-looking URL is warned about first.** When the URL has no staging, preview or
localhost marker, the run says what it is about to do and under which identity — and, when a person
is at the terminal, asks. People test against production on purpose, so `--yes` skips the question,
and CI is told but never asked: a question nobody can see is a hung build.

**`--teardown <url>` deletes what the run made.** After every run — passed, failed, or errored,
because the failed run is the likeliest to have left half an account behind — the identity is
POSTed there as JSON, so your own endpoint can clean up. The whole handler:

```js
// app/api/test-teardown/route.js — delete what a test run created.
export async function POST(req) {
  if (req.headers.get("authorization") !== `Bearer ${process.env.TEARDOWN_SECRET}`) {
    return new Response("no", { status: 401 });
  }
  const run = await req.json(); // { runId, email, username, name, password, test, url, status, at }
  await db.user.deleteMany({ where: { email: run.email } });
  return Response.json({ ok: true });
}
```

Set `SMOLANALYTICS_TEARDOWN_SECRET` where the tests run and it arrives as that `Authorization`
header — an environment variable, not a flag, so it never lands in shell history or the command
line CI prints at the top of every log. A teardown that fails is reported and changes nothing:
the verdict and the exit code were decided before it fired.

**`--seed <url>` builds what the run needs, before it runs.** Labelling and deleting cover the data
a test makes on its way through. Most of what is worth testing needs data that is already there —
"a logged-in user with three past orders can request a refund" — and no sentence can conjure it. So
the identity is POSTed to an endpoint you write, your app fabricates whatever the test needs, and
the flat JSON object it answers with becomes placeholders the sentence can use:

```sh
npx smolanalytics test --url https://staging.app.com \
  --seed https://staging.app.com/api/test-seed \
  --test "open order {{orderId}} and request a refund"
```

```js
// app/api/test-seed/route.js — build the world this sentence describes.
export async function POST(req) {
  if (req.headers.get("authorization") !== `Bearer ${process.env.SEED_SECRET}`) {
    return new Response("no", { status: 401 });
  }
  const run = await req.json(); // { runId, email, username, name, password, test, url, placeholders, at }
  const user = await db.user.create({ data: { email: run.email, name: run.name } });
  const order = await db.order.create({ data: { userId: user.id, total: 4200 } });
  return Response.json({ orderId: order.id }); // -> {{orderId}} in the sentence
}
```

The response is a **flat object of string values** — numbers and booleans are fine, nothing nested —
and every key becomes a `{{placeholder}}`, matched without regard to case. A key that collides with
the run identity (`email`, `password`, `name`, `username`, `runid`) is refused by name, so
`{{email}}` never stops meaning the account `--teardown` deletes. `placeholders` in the request body
lists the seed tokens *this* sentence asks for, so one handler can serve a whole suite and build only
what each test needs. `SMOLANALYTICS_SEED_SECRET` arrives as the `Authorization` header, the same way
and for the same reason as the teardown one.

**A seed that fails is `errored`, never `failed`.** If the endpoint is down, slow (there is a hard
10-second cap), or answers something that is not a flat JSON object, the run stops and says which
endpoint and what it returned. It exits 2 — the runner's code — because the world the sentence
describes was never built, and a red X reading "your refund flow is broken" would be a bug report
about a bug nobody saw. A sentence that names a token the endpoint did not return is the same thing
and gets the same treatment. `--teardown` still fires, because a seed can fail halfway.

**Seeded values are treated as credentials.** An order id and a session token arrive through the
same door, so the value goes into the recording, the step labels, the pull request comment, the run
summary and the evidence text as `{{orderId}}`, and is resolved back only when the model is prompted
and when a key is pressed. The recording stays replayable — a recording made against one seeded
order replays green against the next one, because the placeholder resolves to whatever *this* run
was given. The failure screenshot is not masked and cannot be: it is a picture of your own page.

A URL counts, in both spellings. A sentence like `open order {{orderToken}}` makes the agent
navigate rather than type, so the recorded step, the terminal line and the browser's own error text
carry `?t={{orderToken}}` — and because a browser percent-encodes a value on its way into a URL, the
encoded form (`sk%2Blive%2F...`, which is every base64 token) is masked too, as
`{{orderToken__urlencoded}}`. Both resolve back at replay, so a recording whose navigation names a
seeded order still opens the *next* run's order instead of going stale at agent price. A value your
application itself rewrites — hashed, re-signed — is a different string, and no masking can find it.

Without `--seed`, none of this runs: no request, no placeholders, and byte-for-byte the same
recording.

### On every pull request

Tests run on your own GitHub Actions runner, against the preview URL your host already builds, and
the verdicts are posted as one comment that is edited in place on every push. Paste this into
`.github/workflows/e2e.yml`:

```yaml
name: e2e
on: pull_request

permissions:
  contents: read          # read tests/ out of this repo
  pull-requests: write    # post the one comment

jobs:
  e2e:
    runs-on: ubuntu-latest
    timeout-minutes: 30
    # Neither of these gets your secrets, so both would error every test and then 403 the comment.
    if: >-
      github.event.pull_request.head.repo.full_name == github.repository
      && github.actor != 'dependabot[bot]'
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 22

      # No step sets `preview`, so URL below is empty — and an empty --url inside Actions on a pull
      # request makes the CLI ask the deployments API which preview belongs to this pull request.
      # Add a step with `id: preview` only if your host does not create GitHub deployments.

      # Without this every run is an agent run, and the replay never pays off. Actions scopes a
      # cache to the branch that wrote it and this workflow only runs on pull requests, so the
      # first run of each new pull request is still a full agent run; every push after it replays.
      # Commit .smolanalytics/recordings instead if you would rather pay that once for the repo.
      - uses: actions/cache/restore@v4
        with:
          path: .smolanalytics/recordings
          key: smolanalytics-recordings-${{ github.sha }}
          restore-keys: |
            smolanalytics-recordings-

      - name: e2e
        continue-on-error: true
        env:
          URL: ${{ steps.preview.outputs.url }}
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: npx smolanalytics@latest test --suite tests/ --url "$URL" --comment

      # A failed test writes a screenshot + page text to .smolanalytics/evidence/. Evidence left
      # on a recycled runner is no evidence at all, so it goes up as an artifact.
      - name: upload failure evidence
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: smolanalytics-evidence
          path: .smolanalytics/evidence
          if-no-files-found: ignore

      # Its own step, because actions/cache only saves when the job succeeds — and the run that
      # repaired the most recordings is the one with a failing test in it.
      - if: always()
        uses: actions/cache/save@v4
        with:
          path: .smolanalytics/recordings
          key: smolanalytics-recordings-${{ github.sha }}
```

**You do not configure the preview URL.** With no `--url`, inside Actions on a pull request, the CLI
asks the GitHub deployments API — with the token the job already has — which deployment belongs to
this pull request, and waits up to four minutes for it. Vercel, Netlify and Cloudflare Pages all
announce their previews that way. If yours does not, add a step with `id: preview` that sets a
`url` output and it is used instead. Nothing is ever guessed: no ready deployment means exit 2, a
comment on the pull request saying so, and no verdict about your app.

The same file ships in this package as `templates/github-action.yml`, with comments and with three
ready-made preview steps to uncomment: a host's own wait action, a staging URL passed straight in,
or build-and-serve for a static site.

`GITHUB_TOKEN` is the one Actions gives every job for free, which is why the comment needs no
GitHub App and no install on your other repositories. `continue-on-error` is there on purpose: a
new tool that puts a red X on somebody's pull request in week one gets uninstalled before it has
earned the right to block a merge. Take it out when the suite has been right often enough.

### In a second browser

The reason to drive a real browser rather than a fake DOM is that real browsers disagree, so the
same sentence runs in any of the three Playwright ships:

```sh
npx smolanalytics test --suite tests/ --url https://yourapp.com --browser webkit
```

`chromium` is the default and is unchanged. `firefox` and `webkit` each need a one-off download —
if one is missing the run stops with the exact command that fixes it (`npx playwright install
webkit`) and exits `2`, the runner's code, because nothing was learned about your app.

**A recording remembers which engine it was made on.** Replaying a Chromium recording on WebKit
still replays — re-recording every test per engine would cost a full agent run per test per engine
— but it never does so quietly: the run says so, and so does the verdict it posts.

```
PASS — replayed 3 steps in 1.2s, no model calls.
This recording was made on Chromium and was replayed on WebKit. The steps and the proof were
checked against WebKit this time, so a WebKit-only break in this flow would have shown up here.
```

If the recording stops fitting on the other engine, that is `stale` as usual, with the engine
change named as a candidate cause — which is exactly the WebKit-only bug a second engine is for.

### Uploading a file

A sentence like *"upload a receipt and confirm it appears in the list"* works with no file on
anybody's disk and no fixtures directory in your repository. The agent attaches to a file input, to
a styled control with a hidden input behind it, or to a button that opens a picker, and the file is
**generated to match what the control accepts**:

| the input says | it gets |
|---|---|
| `accept="image/*"` or `.png` | a real 16x16 PNG |
| `.jpg` | a real 16x16 JPEG |
| `.pdf` | a real one-page PDF |
| `text/csv`, `.json`, `.txt` | a small file of that type |
| no `accept` at all | a short text file |

The run says which file it made, so a rejection is debuggable:

```
  ✓  1 upload to "Receipt image" — attached smolanalytics-test.png (a 16x16 PNG, because the
       control accepts "image/*")
```

An upload records and replays like any other step, with **no model call**. The recording holds the
control, never a path and never the bytes: the file is rebuilt from the page's own `accept` at
replay time, so it is identical on every machine and it follows the form if the form changes what
it takes. If your app rejects the generated file, that is a `fail` carrying the app's own message —
never an `errored`, which would claim the runner broke.

A control your app has **disabled** is refused rather than uploaded to: the browser automation can
put a file into a disabled input, but no user of your app can, so a green step there would be a
green step for something that cannot happen. A drag-and-drop zone with no file input behind it says
so too, instead of reporting an upload that never occurred.

### The results, kept apart

| | |
|---|---|
| **pass** / **fail** | the app did, or did not, do what the sentence describes. A failure is a bug report: the page, the control, what was expected, what happened |
| **flaky** | the test failed, then passed when retried from a clean page. Not a pass and not a bug report: the test is unreliable. It warns without failing the build — and a test that keeps doing this is hiding an intermittent bug |
| **stale** | a recording stopped fitting. A replay cannot tell a renamed button from a deleted one, so this is never red and never worded as a failure — the agent re-checks it and rewrites the recording |
| **errored** | this runner could not run: no browser, no key, no network. Never your app |

Exit codes follow the same split: `0` nothing failed (flaky exits 0 too — it is a warning, and the
comment and the log both say so out loud), `1` a test failed, `2` the runner could not finish. A
pipeline that gates on `1` alone never reddens a build because our side had an outage.

### Flags

| flag | |
|---|---|
| `--url <url>` | where the test starts |
| `--test "<text>"` | one test, in plain English |
| `--suite <dir>` | a folder of `.md` files instead |
| `--plan <file>` | replay this recording first, and record to it on a pass |
| `--plans <dir>` | where a suite keeps its recordings (default `.smolanalytics/recordings`) |
| `--since <ref>` | with `--suite`, run only the tests this change could have broken — and say what was skipped, in the terminal and in the comment |
| `--comment` | post the verdicts on the pull request (GitHub Actions) |
| `--browser <name>` | `chromium` (default), `firefox` or `webkit` — the same test in a different engine |
| `--headed` | watch it happen |
| `--yes` | install the browser, and don't ask before a production-looking URL |
| `--teardown <url>` | POST the run's identity there afterwards, so you can delete what it made |
| `--seed <url>` | POST it there BEFORE the test; the flat JSON it returns becomes placeholders the sentence can use |
| `--email-domain <dom>` | the domain in `{{email}}` (default `example.com`) |
| `--retries <n>` | re-run a failing test from a clean page; pass-on-retry is flaky, not passed (default 1; 0 disables) |
| `--workers <n>` | with `--suite`, how many tests run at once (default: measured from cores, memory and whether a key is set; `1` is one at a time) |
| `--evidence-dir <dir>` | where a failure's screenshot and page text land (default `.smolanalytics/evidence`) |
| `--max-calls <n>` | stop a test after this many model calls; reports why and exits 2 (0 = no ceiling) |
| `--login "<sentence>"` | sign in once in plain English; every test after it reuses the saved session |
| `--auth-file <path>` | instead of `--login`: a Playwright storage state you already generate |
| `--auth-dir <dir>` | where the saved session is kept (default `.smolanalytics/auth`, gitignored for you) |
| `--no-render-check` | switch off the guard that fails a pass over a blank, unstyled or crashed page |
| `--layout=off` \| `=strict` | layout sanity notes (covered controls, zero-size targets, overflow). Report-only by default; `strict` lets a finding fail the run |
| `--wait-preview <sec>` | in GitHub Actions with no `--url`, how long to wait for this pull request's own preview deployment (default 240) |

`ANTHROPIC_API_KEY` is the only key the agent needs, and it is yours — the model calls are billed to
your account, not resold. Replaying a recording needs no key at all. `SMOLANALYTICS_MODEL` picks a
different model.

### Where the tests come from, if you have none

```sh
npx smolanalytics suggest --url https://your-staging-url.com
```

A real browser walks the app — same-origin pages only, reading, never clicking or submitting —
and writes the flows worth testing into `tests/*.md`, in the format `test --suite` already runs.

**It only proposes what it actually saw.** Every proposal has to quote text that appears on a page
the crawl read; one that cannot is dropped, out loud, with the reason. A model asked "what should
an app like this test?" answers from every app it has ever read about — password resets, wishlists,
coupon codes — and one such file is worse than an empty folder, because it fails forever against a
feature that never existed and teaches you to distrust the files beside it.

Flows that create data get the placeholders from `--teardown`'s vocabulary — `{{email}}`,
`{{password}}`, `{{runid}}` — so every row a test makes is one you can find afterwards. Existing
files are never overwritten; a second run says what it skipped.

| flag | |
|---|---|
| `--out <dir>` | where the tests land (default `tests/`) |
| `--max <n>` | how many to propose (default 6) |
| `--yes` | don't ask before writing |

### What a run costs, and how to cap it

Every agent run prints what it spent, under the verdict:

```
4 model calls · 21,430 in / 1,205 out
```

The token counts come from the model API itself, so they are exact rather than estimated. A replay
prints `no model calls`, which is the whole economic argument in three words.

Money is shown only if you supply the price, because a figure invented here would sit beside real
measurements and be read as one of them:

```sh
export SMOLANALYTICS_PRICE_IN=15     # dollars per million input tokens, from your model's pricing
export SMOLANALYTICS_PRICE_OUT=75    # dollars per million output tokens
```

`--max-calls <n>` is the ceiling. It is a limit on model calls rather than on dollars deliberately:
a dollar cap is only as accurate as a price table, while a call cap is exact and needs no pricing.
Hitting it stops cleanly, says so, and exits **2** — a budget you set is our decision to enforce,
never a verdict about your application.

### Where a failure goes

The bug report is already written — the page, the control, what was expected, what happened, and
the changed file most likely responsible with the evidence linking it to the test. Copying that
into a tracker by hand is transcription, so it does not have to be done by hand.

```sh
# Slack, or any webhook
export SMOLANALYTICS_SLACK_WEBHOOK=https://hooks.slack.com/services/...
export SMOLANALYTICS_WEBHOOK=https://your-endpoint.example.com/smolanalytics

# Linear
export SMOLANALYTICS_LINEAR_API_KEY=lin_api_...
export SMOLANALYTICS_LINEAR_TEAM_ID=...

# Jira Cloud
export SMOLANALYTICS_JIRA_URL=https://you.atlassian.net
export SMOLANALYTICS_JIRA_EMAIL=you@company.com
export SMOLANALYTICS_JIRA_API_TOKEN=...
export SMOLANALYTICS_JIRA_PROJECT=ENG
```

All of it is from the environment and never a flag, because a flag lands in shell history and in
the command line CI prints at the top of every log.

**Slack gets the ship verdict, not a count.** And it stays quiet on a clean run — a channel that
posts on every green push is a channel that gets muted, and then the one message that mattered
arrives where nobody is reading. It does speak when nothing failed but recordings went stale,
because that run verified almost nothing and looks fine.

**A tracker gets failures only.** Never `stale` (our recording aged, not your bug), never `flaky`
(nobody can act on it yet), never `errored` (our runner, not your app). One issue per test however
many times it fails: each carries a `smolanalytics:<test>` tag in its body, and the next failure
comments on the open issue instead of opening another.

Nothing here can change a verdict or an exit code. Every delivery failure is reported in one line
and the verdict above it still stands.

### Tests behind a login

Most tests worth writing are behind a sign-in, so the login is recorded and reused the same way
everything else here is:

```sh
export SMOLANALYTICS_LOGIN_EMAIL=qa@yourcompany.com
export SMOLANALYTICS_LOGIN_PASSWORD=...
npx smolanalytics test --suite tests/ --url "$URL" \
  --login "sign in as {{email}} with {{password}}"
```

The agent signs in **once** for the whole suite and every test after that starts already signed in —
measured at fifty tests, one login. If the session expires mid-run it signs in again, once, and
carries on.

The credential is read from the environment and never written down: the recording stores
`{{password}}`, not the password, and resolves it at the moment of the keystroke. The saved session
lands in `.smolanalytics/auth/`, which gets a `.gitignore` of its own the first time it is written,
because that file holds a live session cookie.

If a sign-in does not work, that is reported as **errored** — our side — and never as a failed test.
A wrong password says nothing about whether your product works, and a red X on a working login is
worse than no test at all.

Already generating a storage state? `--auth-file path/to/state.json` uses it and skips all of this.

### The page that passes while looking broken

A test's proof is text on the page. That means a build whose CSS 404'd, that rendered blank, or that
is showing a crash overlay with the text still in the DOM would otherwise **pass** — a green tick on
a page nobody could use.

So a passing run also checks that the page actually rendered, and fails when it did not:

```
The steps all worked and the page still says what it should — but a full-viewport error
surface is covering the page: <div#o> opens with "Unhandled Runtime Error"
```

It only fires on catastrophe — a blank viewport, stylesheets that failed to load, a framework error
surface. A canvas game with no text, an image-only gallery, a dark theme, a cookie banner over the
whole viewport and an app that paints half a second late are all left alone. `--no-render-check`
turns it off.

### What this never asks you for

No account, no sign-up, no GitHub App across your repositories, nothing committed to your code, and
no keys to your database or another vendor's API handed over. Nothing is built before you get a
verdict.

The other shape this can take is to build an isolated environment first — a GitHub App, a
Dockerfile in your repo, your service keys, and a web app plus backend plus database stood up before
the first test runs. It buys something real: it can test an app that has no deploy preview at all.
It also means the first thing you learn is whether the build worked, an hour later, rather than
whether your checkout works, a minute later. If you already have a URL, you do not need any of it.

## The tracking half

The same walk that tests your product knows which user actions exist, so it also writes and
maintains your analytics tracking — inside the SDK you already run (PostHog, Mixpanel, Amplitude,
Google Analytics, Plausible or Segment). This does not replace them. It keeps their instrumentation
correct, which is the job nobody owns.

```sh
npx smolanalytics audit                 # what nothing is measuring, file and line. No account.
npx smolanalytics init --host <url> --key <key>   # wire the tracker into this project
npx smolanalytics connect               # wire the reports into your editor over MCP
npx smolanalytics plan check            # fail CI when a planned event stops firing
```

`audit` needs no account and makes no network call. Your project's setup page on
smolanalytics.com prints the host and keys the other commands want.

### What `init` does

**Edits the file for you** on Next.js (App and Pages Router), SvelteKit, Vite, Create React
App, and plain HTML. It inserts before `</head>` where there is one, and after `<body>` in a
Next.js layout that builds its head from `metadata` — that's the default `create-next-app`
shape and the case most install snippets get wrong.

**Prints the install and changes nothing** on Nuxt and Astro. Neither one installs as a
script tag in an HTML file: Nuxt's belongs in `nuxt.config`, and Astro needs `is:inline` or
the bundler reorders the script so `init` runs before the SDK exists. Editing those with a
generic snippet would give you a page that looks instrumented and sends nothing, so it shows
you the version that works instead.

**Adds** `SMOLANALYTICS_HOST` and `SMOLANALYTICS_WRITE_KEY` to your existing `.env.local` or
`.env`. It will not create one if you don't already keep one.

It is **idempotent**: run it twice and the second run leaves everything alone. It never
half-edits a file, and when it can't find a safe place to insert it says so and prints the
snippet instead of guessing.

#### `init` flags

| flag | |
|---|---|
| `--host <url>` | your instance, e.g. `https://you.fly.dev` (or `SMOLANALYTICS_HOST`) |
| `--key <key>` | public write key (or `SMOLANALYTICS_WRITE_KEY`) |
| `--yes` | don't ask before editing |
| `--print` | print the snippet, change nothing |

The write key is **public** and ingest-only — it cannot read your data. Reads use a separate
secret key that is never embedded in a page.


The write key is **public** and ingest-only — it cannot read your data. Reads use a separate
secret key that is never embedded in a page.

## Why this exists

End-to-end tests are the ones everybody agrees they should have and nobody keeps. You write them,
a button moves, forty of them go red for no reason, and within a month the suite is muted and the
next regression ships to customers instead.

Removing the maintenance is only half of it. A suite is worth having only while people still
believe it, so the verdicts stay apart on purpose — a stale recording is never reported as a bug,
our own runner failing is never reported as your app failing, and a test that passed only on retry
is called flaky rather than green.

Commercial software, licensed not sold — see LICENSE. Your tests, recordings and evidence are
plain files in your own repository and stay yours.
