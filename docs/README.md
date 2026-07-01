# Install smolanalytics in your stack

Two minutes, any platform. The browser SDK autocaptures pageviews + clicks; on the server
it's a plain HTTP POST. Then you [ask your analytics in your editor](../README.md#ask-it-in-your-editor-the-whole-point).

First, run a server and grab your host + write key:

```sh
docker run -p 8080:8080 -v $PWD/data:/data \
  -e SMOLANALYTICS_WRITE_KEY=$(openssl rand -hex 16) ghcr.io/arjun0606/smolanalytics
# or self-host anywhere — it's one binary. host = http://localhost:8080 for local.
```

## Guides

- [Next.js](nextjs.md)
- [React](react.md)
- [Vue](vue.md)
- [Backend](backend.md) — Node, Python, Go, Ruby, PHP (server-side events)
- [Mobile](mobile.md) — iOS (Swift), Android (Kotlin), React Native

Don't see yours? Ingestion is **one HTTP endpoint** (`POST /v1/events`), so anything that
can make an HTTP request works — see [Backend](backend.md) for the shape. Or paste this into
Cursor / Claude Code and let it do it:

> "Add smolanalytics: load `https://YOUR_HOST/sdk.js`, init with my key, and `track()` the
> key moments (signup, activate, checkout) plus `identify()` on login."

## The two things every guide does

1. **Load + init once** so pageviews and clicks are captured automatically.
2. **`track()` the moments you care about** (signup, activate, checkout) so you get funnels,
   and **`identify()`** on login so a user's events tie together.

That's all the instrumentation there is. Everything else — funnels, retention, paths, the
"what to fix" verdict — you just ask for.
