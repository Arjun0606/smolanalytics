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

## Why this exists

Self-hosting PostHog means ClickHouse, Kafka, Redis and a Postgres. Plausible and Umami
install easily but stop at web analytics, so no funnels, no retention, no cohorts.

smolanalytics is one Go binary with no external database, and it does both: visitors and
referrers alongside funnels, retention, paths and cohorts, plus feature flags, A/B tests,
heatmaps and surveys. You ask it in plain English from the dashboard or your editor over MCP,
using your own model, so the AI part costs nothing.

MIT, every feature in the free binary, self-host forever.
