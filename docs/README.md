# Install smolanalytics in your stack

Two minutes, any platform. The browser SDK autocaptures pageviews + clicks; on the server
it's a plain HTTP POST. Then you [ask your analytics in your editor](../README.md#ask-it-in-your-editor-the-whole-point).

First, run a server and grab your host + write key:

```sh
docker run -p 8080:8080 -v $PWD/data:/data \
  -e SMOLANALYTICS_WRITE_KEY=$(openssl rand -hex 16) \
  -e SMOLANALYTICS_PASSWORD=$(openssl rand -hex 12) \
  ghcr.io/arjun0606/smolanalytics
# self-host anywhere: it's one binary. host = http://localhost:8080 for local.
# the image binds 0.0.0.0, so a dashboard password is required (echo the value to keep it).
```

## Docker Compose

For a setup that persists across restarts, use [`docs/docker-compose.yml`](docs/docker-compose.yml) instead of a bare `docker run`. It wires up the same image with a bind-mounted `./data` folder, so your event log survives `down`/`up`.

**1. Grab the file**

```sh
curl -O https://raw.githubusercontent.com/Arjun0606/smolanalytics/main/docs/docker-compose.yml
```

**2. Set your secrets**

Open it and set `SMOLANALYTICS_PASSWORD` and `SMOLANALYTICS_WRITE_KEY` (both required — `serve` refuses to run on a public bind without a password). Generate real values:

```sh
openssl rand -hex 12   # password
openssl rand -hex 16   # write key
```

Two ways to set them, pick one:
- **Inline (default):** edit the values directly under `environment:`.
- **`.env` file:** comment out the `environment:` block, uncomment `env_file:`, and put the same two vars in a `.env` file next to the compose file.

Only one should be active at a time — if both are uncommented, `environment:` silently wins and your `.env` file is ignored.

**3. Run it**

```sh
docker compose up -d
```

`localhost:8080` is up, data lives in `./data`.

**4. Confirm persistence**

```sh
docker compose down
docker compose up -d
```

Your login and data are still there.

## Guides

- [Next.js](nextjs.md)
- [React](react.md)
- [Vue](vue.md)
- [Backend](backend.md): Node, Python, Go, Ruby, PHP (server-side events)
- [Mobile](mobile.md): iOS (Swift), Android (Kotlin), React Native

Don't see yours? Ingestion is **one HTTP endpoint** (`POST /v1/events`), so anything that
can make an HTTP request works. See [Backend](backend.md) for the shape, or paste this into
Cursor / Claude Code and let it do it:

> "Add smolanalytics: load `https://YOUR_HOST/sdk.js`, init with my key, and `track()` the
> key moments (signup, activate, checkout) plus `identify()` on login."

## The two things every guide does

1. **Load + init once** so pageviews and clicks are captured automatically.
2. **`track()` the moments you care about** (signup, activate, checkout) so you get funnels,
   and **`identify()`** on login so a user's events tie together.

That's all the instrumentation there is. Everything else, funnels, retention, paths, the
"what to fix" verdict, you just ask for.