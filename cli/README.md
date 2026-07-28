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

- **Detects** Next.js (App and Pages Router), SvelteKit, Vite, Create React App, Astro, Nuxt,
  and plain HTML.
- **Inserts** the tracker into the right place — before `</head>` where there is one, and
  after `<body>` in a Next.js layout that builds its head from `metadata`, which is the
  default and the case most snippets get wrong.
- **Adds** `SMOLANALYTICS_HOST` and `SMOLANALYTICS_WRITE_KEY` to your existing `.env.local`
  or `.env`. It will not create one if you don't already keep one.

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

Analytics tools give you a number. Almost none of them let you check it. smolanalytics
answers in plain English from a deterministic report, and a CI test proves the dashboard, the
HTTP API and your editor all return the same number for the same question.

You can run that check yourself: **https://smolanalytics.com/proof**

MIT. Self-host free forever.
