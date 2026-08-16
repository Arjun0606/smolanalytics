# Deploy markers: "which ship did it"

A deploy marker is one line telling smolanalytics that something shipped, and when. It costs a
`curl` in your build command. What it buys is the difference between:

```
checkout fell 33% on 2026-06-27
  no deploy recorded near this day and the drop is spread evenly across browsers,
  devices and pages — record deploys and this line becomes 'which ship did it'
```

and:

```
checkout fell 33% on 2026-06-27
  Ship a3f91c2 "rewrite the payment form" landed the same day (correlation, not proof).
```

The investigation runs either way. Only one of those two versions ends an argument.

---

## The one line

```sh
curl -sS -X POST "$SMOLANALYTICS_HOST/v1/deploys" \
  -H "Authorization: Bearer $SMOLANALYTICS_WRITE_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"sha\":\"$GIT_SHA\",\"message\":\"$GIT_MSG\",\"source\":\"ci\"}"
```

It takes the **public write key** — the same one in your browser snippet, not an admin key. A
deploy marker is a fact about your own release, not a secret, and requiring a privileged
credential in CI is how this stops getting done.

If the smolanalytics binary is already on the machine, `smolanalytics deploy` reads the SHA,
subject and author out of git for you and does the same POST.

**Failures must not break your build.** Use `curl -sS ... || true`, or `continue-on-error` in
Actions. Analytics that can fail a deploy is analytics that gets removed after the first outage.

---

## GitHub Actions

A copyable workflow lives at [`.github/workflows/deploy-marker.yml`](../.github/workflows/deploy-marker.yml)
in this repository. The whole of it:

```yaml
name: deploy marker
on:
  push:
    branches: [main]
jobs:
  mark:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Record the deploy
        continue-on-error: true          # never fail a release over a marker
        env:
          HOST: ${{ secrets.SMOLANALYTICS_HOST }}
          KEY: ${{ secrets.SMOLANALYTICS_WRITE_KEY }}
        run: |
          [ -n "$HOST" ] || exit 0       # unconfigured fork: do nothing, quietly
          curl -sS -X POST "$HOST/v1/deploys" \
            -H "Authorization: Bearer $KEY" \
            -H "Content-Type: application/json" \
            -d "$(jq -n \
                  --arg sha "$GITHUB_SHA" \
                  --arg msg "$(git log -1 --format=%s)" \
                  --arg author "$(git log -1 --format=%an)" \
                  --arg ref "$GITHUB_REF_NAME" \
                  --arg url "$GITHUB_SERVER_URL/$GITHUB_REPOSITORY/commit/$GITHUB_SHA" \
                  '{sha:$sha,message:$msg,author:$author,ref:$ref,url:$url,source:"github"}')"
```

`jq` is preinstalled on GitHub runners, and it is doing real work here: a commit subject
containing a quote or a backslash breaks a hand-built JSON string, and the commits most worth
marking are the ones with interesting messages.

**On the cloud**, if you have installed the GitHub App, merged PRs are already recorded and you
do not need this. Use it when you self-host, deploy from somewhere other than a PR merge, or want
a marker per release rather than per merge.

---

## Per host

Every host lets you run a command as part of the build. That path works everywhere and needs
nobody's permission, which is why it is the one documented here. Native deploy webhooks are
mostly paywalled: of the six hosts below, only **Netlify** and **Railway** offer one free and
self-serve. Vercel gates them behind Pro, Render behind Pro *and* a single destination slot
total, and **Fly.io has none at all**.

### Vercel

`package.json`, so it runs on every deployment including previews:

```json
{
  "scripts": {
    "vercel-build": "next build && curl -sS -X POST \"$SMOLANALYTICS_HOST/v1/deploys\" -H \"Authorization: Bearer $SMOLANALYTICS_WRITE_KEY\" -H 'Content-Type: application/json' -d \"{\\\"sha\\\":\\\"$VERCEL_GIT_COMMIT_SHA\\\",\\\"message\\\":\\\"$VERCEL_GIT_COMMIT_MESSAGE\\\",\\\"ref\\\":\\\"$VERCEL_GIT_COMMIT_REF\\\",\\\"source\\\":\\\"vercel\\\"}\" || true"
  }
}
```

Set `SMOLANALYTICS_HOST` and `SMOLANALYTICS_WRITE_KEY` in Project Settings → Environment
Variables. Scope them to Production only if you do not want preview builds marked — preview
deploys have no users on them, so a marker for each one is noise in the deploy list.

### Netlify

```toml
# netlify.toml
[build]
  command = "npm run build && curl -sS -X POST \"$SMOLANALYTICS_HOST/v1/deploys\" -H \"Authorization: Bearer $SMOLANALYTICS_WRITE_KEY\" -H 'Content-Type: application/json' -d \"{\\\"sha\\\":\\\"$COMMIT_REF\\\",\\\"ref\\\":\\\"$BRANCH\\\",\\\"source\\\":\\\"netlify\\\"}\" || true"
```

### Fly.io

Fly has no deploy webhook, so this is the only route:

```sh
flyctl deploy && smolanalytics deploy --host "$SMOLANALYTICS_HOST"
```

### Railway, Render, Cloudflare Pages

Append the same `curl` to your build command. The SHA variable differs:

| Host | Commit SHA variable | Branch |
|---|---|---|
| Railway | `RAILWAY_GIT_COMMIT_SHA` | `RAILWAY_GIT_BRANCH` |
| Render | `RENDER_GIT_COMMIT` | `RENDER_GIT_BRANCH` |
| Cloudflare Pages | `CF_PAGES_COMMIT_SHA` | `CF_PAGES_BRANCH` |

---

## What you get

```sh
# every recorded ship
curl -H "Authorization: Bearer $KEY" "$HOST/v1/deploys"

# before/after for one metric across each ship
curl -H "Authorization: Bearer $KEY" "$HOST/v1/deploys?event=checkout"
```

In your editor, over MCP: **"did my last deploy move signups?"** — that is the `deploy_impact`
tool, and it is the question no other analytics tool can answer, because they have your data and
not your releases.

On the dashboard, markers appear on the trend chart and in the deploy pane, and the investigation
starts naming ships in its cause lines.

**Correlation, not proof.** A ship that lands the same day as a change is a strong lead and not a
verdict, and every line the product writes about it says so. Two things shipped the same afternoon
are indistinguishable to this, and it will tell you that rather than pick one.
