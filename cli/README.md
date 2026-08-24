# smolanalytics

Wire [smolanalytics](https://smolanalytics.com) into your app with one command.

```sh
npx smolanalytics init --host https://your-instance --key sa_xxx
```

It works out what your project is, edits the one file that needs editing, and tells you
exactly which file it touched before it touches it.

```
  detected  Next.js (App Router)
  file      app/layout.tsx
  edited    app/layout.tsx
  edited    .env.local
```

## What it does

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

## Flags

| flag | |
|---|---|
| `--host <url>` | your instance, e.g. `https://you.fly.dev` (or `SMOLANALYTICS_HOST`) |
| `--key <key>` | public write key (or `SMOLANALYTICS_WRITE_KEY`) |
| `--yes` | don't ask before editing |
| `--print` | print the snippet, change nothing |

The write key is **public** and ingest-only — it cannot read your data. Reads use a separate
secret key that is never embedded in a page.

## Where the key comes from

Self-hosting the MIT Go binary prints both keys when it starts. On the hosted plane they're
on the project's setup page.

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
calls at all: 0.7s for a three-step test, measured. The agent wakes again only when the recording
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

      - name: the preview URL
        id: preview
        uses: patrickedqvist/wait-for-vercel-preview@v1
        with:
          token: ${{ secrets.GITHUB_TOKEN }}
          max_timeout: 600

      # Without this every run is an agent run, and the replay never pays off.
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

The same file ships in this package as `templates/github-action.yml`, with comments and with the
other two ways to get a URL: pass one straight in for staging, or build and serve a static site in
the job.

`GITHUB_TOKEN` is the one Actions gives every job for free, which is why the comment needs no
GitHub App and no install on your other repositories. `continue-on-error` is there on purpose: a
new tool that puts a red X on somebody's pull request in week one gets uninstalled before it has
earned the right to block a merge. Take it out when the suite has been right often enough.

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
| `--comment` | post the verdicts on the pull request (GitHub Actions) |
| `--headed` | watch it happen |
| `--yes` | install the browser, and don't ask before a production-looking URL |
| `--teardown <url>` | POST the run's identity there afterwards, so you can delete what it made |
| `--email-domain <dom>` | the domain in `{{email}}` (default `example.com`) |
| `--retries <n>` | re-run a failing test from a clean page; pass-on-retry is flaky, not passed (default 1; 0 disables) |
| `--evidence-dir <dir>` | where a failure's screenshot and page text land (default `.smolanalytics/evidence`) |

`ANTHROPIC_API_KEY` is the only key the agent needs, and it is yours — the model calls are billed to
your account, not resold. Replaying a recording needs no key at all. `SMOLANALYTICS_MODEL` picks a
different model.

### What this never asks you for

No account, no sign-up, no GitHub App across your repositories, nothing committed to your code, and
no keys to your database or another vendor's API handed over. Nothing is built before you get a
verdict.

The other shape this can take is to build an isolated environment first — a GitHub App, a
Dockerfile in your repo, your service keys, and a web app plus backend plus database stood up before
the first test runs. It buys something real: it can test an app that has no deploy preview at all.
It also means the first thing you learn is whether the build worked, an hour later, rather than
whether your checkout works, a minute later. If you already have a URL, you do not need any of it.

## Why this exists

Self-hosting PostHog means ClickHouse, Kafka, Redis and a Postgres. Plausible and Umami
install easily but stop at web analytics, so no funnels, no retention, no cohorts.

smolanalytics is one Go binary with no external database, and it does both: visitors and
referrers alongside funnels, retention, paths and cohorts, plus feature flags, A/B tests,
heatmaps and surveys. You ask it in plain English from the dashboard or your editor over MCP,
using your own model, so the AI part costs nothing.

MIT, every feature in the free binary, self-host forever.
