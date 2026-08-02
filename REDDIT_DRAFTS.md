# reddit drafts — you post these, i don't

Written to sound like you: lowercase, short, no em dashes, no AI polish. Change anything
that doesn't sound right in your head when you read it back.

**Rules I'm working to:** one post at a time, you send it, and you reply to comments
yourself. No DMs to strangers, no same-post-everywhere on the same day.

---

## ⚠️ before you post anything

**r/selfhosted is not eligible yet.** My note says a standalone post there opens
**2026-09-25**. Posting early is the fastest way to get the domain shadowbanned across
Reddit, and r/selfhosted is the single best subreddit for this product. Do not spend it now.

**The GAMEPLAN says "the same honest post weekly" to r/SideProject and r/selfhosted.
Don't.** That is the plan that gets accounts banned rather than the plan that worked for
Rybbit. Same post + same link + weekly cadence is the exact pattern spam filters are tuned
for. One post per subreddit, weeks apart, each one written for that subreddit.

---

## post 1 — r/SideProject (safe to post now)

**Title:** i wanted analytics i could self-host without running clickhouse and kafka, so i wrote it as one go binary

**Body:**

been building this for a while and finally using it on my own stuff.

the thing that annoyed me: if you want product analytics (funnels, retention, paths) and
not just pageviews, self-hosting means posthog, and posthog self-hosted means clickhouse +
kafka + redis + a postgres. their own docs call self-hosting officially unsupported and
want 4 vcpu / 16gb. for a side project that is insane.

plausible and umami install easily but stop at web analytics.

so smolanalytics is one go binary, one data file, no external database. `docker run -p
8080:8080 ghcr.io/arjun0606/smolanalytics demo` and you get a populated dashboard to poke
at. it does web analytics and product analytics off the same events, plus feature flags,
a/b tests, heatmaps and surveys. cookieless so no consent banner.

the part i actually use every day: it is an mcp server, so i ask my coding agent "where do
people drop off between signup and activate" and it answers from my real data without me
opening a dashboard. it never writes sql, it calls fixed reports, so it either gives you
the real number or says it can't.

mit, self-host free forever. there's a hosted version if you don't want to run it but the
binary is not crippled.

repo: https://github.com/Arjun0606/smolanalytics
demo (real product, demo data): https://smolanalytics-demo.fly.dev

happy to answer anything. genuinely want to know what breaks.

---

## post 2 — r/webdev (post this ~1 week after post 1, not the same day)

r/webdev is strict about self-promo. Lead with the technical thing, not the product.

**Title:** TIL browsers ship the entire ISO country register, so you don't need a 250-row lookup table

**Body:**

was adding country names to a dashboard and about to paste in a 250 row iso 3166 map. then
remembered `Intl.DisplayNames` exists:

```js
new Intl.DisplayNames(navigator.languages, { type: 'region' }).of('IN')
// -> "India"
```

it's cldr backed so it stays current when a country renames itself, and it answers in the
user's language, not yours. works for languages and currencies too (`type: 'language'`,
`type: 'currency'`).

flag emoji don't need a table either, they're just the two letters shifted into the
regional indicator block:

```js
const flag = cc => String.fromCodePoint(...[...cc].map(c => 0x1F1E6 + c.charCodeAt(0) - 65))
```

one gotcha that bit me: that arithmetic is only meaningful for A-Z. i fed it a lowercase
code and got 🈚🈘 back, which is two unrelated japanese glyphs sitting where a flag should
be. validate before you shift.

---

## post 3 — r/opensource or r/golang (hold until you have a second thing to say)

Don't post this yet. Post it when the answer to "how many people use it" is not "me".
A repo with no users posted to r/golang gets ignored, and you only get one first
impression per subreddit.

---

## how to handle comments

- "why not just use plausible/umami" → they're good, they stop at web analytics. say that.
- "another analytics tool?" → agree it's crowded, the specific gap is self-hostable
  product analytics without a cluster.
- "is this AI slop" → the answer is the repo. don't get defensive, link the code.
- if someone finds a bug, fix it and reply that you fixed it. that single behaviour is
  worth more than any post.

## don't

- don't post the same text to two subreddits on the same day
- don't reply to your own post to bump it
- don't DM anyone who comments
- don't say "we" if it's just you, people can tell and it reads as a fake company
