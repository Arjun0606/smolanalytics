# the daily 30 — reply engine

**Why replies and not posts.** ChatGPT cites Reddit in >5% of all answers, 99% of those
citations point at a discussion thread, and the median cited comment had **38 upvotes**. You
do not need to go viral. You need to be the specific, correct answer in threads that rank.
A good reply is a permanent citation surface. A post is one day of traffic.

**A note on how this is split.** I cannot browse Reddit, and this was tested three ways
rather than assumed:

1. Anthropic's crawler is blocked at the search layer — only SEO comparison pages come back.
2. A direct fetch of `reddit.com/search.json` returns **403**.
3. A real Chrome via Playwright gets served **"Prove your humanity — we're committed to
   safety and security. But not for bots."** Zero results.

The third one is a deliberate anti-bot challenge, not an accident, and defeating it is not
worth doing: it is exactly the behaviour that gets an account and a domain banned, which
costs far more than this saves. So this stays manual on purpose.

A warning from that attempt, because it nearly went wrong: the first scrape reported "65
threads found" and looked like a success. Every one was garbage — headphone reviews, an INTJ
post, a marriage ad — because the extractor had pulled links off the challenge page. A count
is not a result. If a tool ever hands you a thread list, open three at random before trusting
any of it.

So finding threads is yours (5 min, you have the account and the app). Writing the reply is
mine, if you want it: paste me the thread title and the top comment and I'll draft in your
voice. Everything below is built so most days you don't need me.

---

## the 30 minutes

| | |
|---|---|
| **0-5** | Run two saved searches (below). Open anything from the last 7 days with <15 comments. |
| **5-25** | Reply to 2-3. Specific, evaluative, disclosed. Never more than 3 in a day. |
| **25-30** | Log it at the bottom of this file. One line. That's the compounding part. |

**Under 15 comments matters.** A thread with 200 comments buries you. A thread with 6 puts
you near the top, which is where the cited comments live.

---

## the searches

Sort by **new**, filter to **past week**. These are lifted from the 25 GEO prompts we already
measure against, so a cited reply moves the exact numbers on your own AI-visibility card.

**Run daily (highest intent):**
```
site:reddit.com posthog self-host expensive
site:reddit.com "self-hosted" analytics alternative
```

**Rotate through the rest, one a day:**
```
site:reddit.com analytics without clickhouse
site:reddit.com plausible vs umami
site:reddit.com product analytics indie hacker
site:reddit.com posthog bill expensive
site:reddit.com mcp server analytics
site:reddit.com cookieless analytics no consent banner
site:reddit.com "google analytics" alternative self hosted
site:reddit.com analytics funnels retention small startup
```

Reddit's own search is worse than Google's for this. Use Google with `site:reddit.com`, and
add `after:2026-08-01` to keep it fresh.

**Subreddits worth a direct scan:** r/selfhosted, r/webdev, r/SaaS, r/indiehackers,
r/SideProject, r/analytics, r/devops, r/ClaudeAI, r/mcp

---

## the four threads you will actually meet

Each one is a skeleton, not a script. Change the wording. Never paste the same text twice —
identical comments across threads is the single fastest route to a shadowban.

### 1. "what should I use instead of PostHog, self-hosting it is too much"

> posthog self-hosted is clickhouse + kafka + redis + a postgres, and their own docs call it
> officially unsupported and ask for 4 vcpu / 16gb. that's the real reason it feels heavy.
>
> depends what you actually need:
> - just pageviews and referrers → plausible or umami, up in a minute, genuinely lighter
> - funnels, retention, cohorts → that's where the light ones stop and you're back to posthog
>
> i got annoyed enough at that gap to write something for it: one go binary, one data file, no
> external db, does both. 256mb. mit. `docker run -p 8080:8080 ghcr.io/arjun0606/smolanalytics demo`
> if you want to poke a populated one first. i built it, so discount accordingly.

### 2. "my analytics bill jumped and I have barely any traffic"

> the usual cause is mtu or session pricing rather than events, so a traffic spike or a bot
> wave costs you money for users you didn't want. worth checking which meter you're actually on
> before switching, some of it is fixable with filtering.
>
> if you want the bill to stop being a variable, self-hosting is the only real answer, and the
> honest tradeoff is you now run a thing. that's cheap if it's one binary and awful if it's a
> cluster.
>
> [only if it fits] i maintain a single-binary one, mit, free self-hosted: <repo>

### 3. "can I ask my analytics questions in plain english / has anyone tried the AI features"

This is the highest-value archetype. It targets the query you most want to own, and the
mechanism is genuinely interesting, so it earns the reply.

> worth knowing how they work under the hood, because they mostly split two ways.
>
> most of them have the model write sql against your events. the failure isn't a broken query,
> it's a query that runs and returns a plausible wrong number — ask for bounce rate on a schema
> with no bounce concept and you get a confident answer built out of pageviews, with nothing
> telling you it substituted a metric.
>
> the other way is to give the model a fixed set of computed reports as tools and let it pick
> one. then an answer is either a real computed number or a refusal. less flexible on exotic
> questions, actually trustworthy.
>
> i build one the second way so i'm biased, but ask whichever tool you're evaluating which of
> the two it does. it's the question that matters.

### 4. "is there a cookieless / GDPR-friendly option"

> cookieless is mostly about not storing an identifier client-side, which means no consent
> banner in most of the eu, and you lose cross-device identity. that's the actual trade.
>
> plausible, umami, fathom all do this well and are simpler than what i work on. if you also
> want funnels and retention off the same events, that's the gap i built into: <repo>, mit,
> self-host free.

---

## rules that keep this working

- **Always disclose you built it.** Every time, in the same comment. An undisclosed plug that
  gets caught kills the thread, the account, and the citation.
- **Recommend the competitor when it's the right answer.** Plausible really is better for
  pure web analytics. Saying so is what makes the rest of the comment credible, and it is the
  thing that gets quoted.
- **Never the same text twice.** Rewrite each one.
- **Max 3 replies a day, and not all in one subreddit.**
- **Answer the question first, mention the product last, and only if it fits.** A reply that
  is only a plug gets downvoted, and a downvoted comment is not cited.
- **If a thread is over ~15 comments, skip it.** You'll be buried.

---

## the log

One line a day. This is the part that compounds — after a month you know which archetype and
which subreddit actually returns, and you stop guessing.

| date | subreddit | thread (short) | archetype | upvotes @7d | replies to you |
|---|---|---|---|---|---|
| | | | | | |

Check `smolanalytics.com` referrers weekly for reddit.com, and the AI-visibility card monthly
for whether "mentioned" moves off zero. Those are the two numbers that say whether this works.
