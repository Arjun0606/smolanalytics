# the map — which reddit thread moves which measured prompt

One page. Left column is a question we already measure ourselves on. Right column is where
a reply lands so that answering it moves the number.

**The mechanism, in one line:** ChatGPT cites Reddit in >5% of answers, 99% of those
citations are discussion threads, and 62% of cited threads also rank on Google page 1. So a
reply that ranks for a query is a permanent source for that query — traffic today, citation
for as long as the thread lives.

**Read this before using it:** sampling is currently OFF for our own project (trial plan,
and the gate requires a plan that funds a complete 25-prompt sweep). Until that changes, this
is unmeasured — you are working from the prompt list without a scoreboard. The prompt set is
still the right target list; you just cannot yet prove a lift.

---

## priority 1 — the four prompts where we are closest to winning

Grounded Claude already ranks us #1 when it finds us at all. These are the queries where one
good thread plausibly flips a zero.

| measured prompt | where to answer it | which archetype |
|---|---|---|
| *i need analytics i can run as one binary with no clickhouse or kafka to babysit* | r/selfhosted (after 25 Sep), r/devops, r/docker | #1 posthog-is-heavy |
| *best open source product analytics you can self-host on a single small server* | r/selfhosted, r/opensource, r/SideProject | #1 |
| *best analytics tools with an mcp server so my coding agent can set them up* | r/ClaudeAI, r/mcp, r/cursor | post 3 / MCP |
| *how can i get my coding agent to add event tracking to my app automatically* | r/ClaudeAI, r/cursor, r/webdev | post 3 |

The last two are the least contested queries in the whole set. Almost nobody is writing
about analytics-over-MCP, so the bar to become the cited answer is lowest there.

## priority 2 — high volume, more crowded

| measured prompt | where | archetype |
|---|---|---|
| *my analytics bill jumped to hundreds a month for a tiny amount of traffic* | r/SaaS, r/indiehackers, r/webdev | #2 the bill |
| *best cheap alternatives to enterprise product analytics for a bootstrapped team* | r/indiehackers, r/SaaS | #2 |
| *what is the best product analytics tool for indie hackers on a tight budget* | r/indiehackers, r/SideProject | #2 |
| *best privacy friendly self-hosted web analytics for a side project* | r/selfhosted, r/privacy | #4 cookieless |
| *i can never trust the numbers my ai gives me because it makes up sql queries* | r/webdev, r/dataengineering | #3 llm-writes-sql |
| *best analytics tools that let you ask questions in plain english instead of writing sql* | r/webdev, r/analytics | #3 |

## priority 3 — answer honestly, expect to lose

These are comparison queries where the right answer is often *not* us. **Reply anyway.** A
comment that recommends Plausible when Plausible is correct is the comment that gets quoted,
and the credibility carries into every other thread you are in.

| measured prompt | the honest answer |
|---|---|
| *plausible vs umami for self-hosted website analytics* | genuinely those two. mention ours only if they ask for funnels |
| *matomo vs plausible vs posthog for a privacy conscious open source stack* | answer the question asked; we are a footnote at most |
| *posthog vs mixpanel for a small startup tracking funnels and retention* | posthog, honestly. our angle is only the self-host weight |
| *google analytics 4 vs posthog for a saas product* | not our fight, skip unless self-hosting comes up |
| *amplitude vs mixpanel for product analytics pricing at scale* | skip. we are not in this conversation |

Four of those five say "skip or concede". That is deliberate. Chasing every query with a
plug is what makes an account read as spam, and the two branded prompts in the set cannot be
moved by posting at all — they move when other people talk about us.

---

## what to do with this

1. Pick **one row from priority 1**, and search only for that query today.
2. Reply to 2-3 threads that match. Under 15 comments each.
3. Log it in `DAILY_30.md`.
4. Move down the list. Do not do all of them at once, and do not post the same text twice.

## how you will know it worked

Two numbers, both already instrumented:

- **reddit.com in referrers** — weekly. Moves within days if a thread lands.
- **mentioned % on the AI-visibility card** — monthly, and only once sampling is switched
  back on. Currently 0% ungrounded, 0% grounded after the false-positive fix. This is the
  number that says whether the citation half is working, and it is slow: a thread has to be
  indexed, then retrieved, then chosen.

If referrers move and mentions do not, the threads are getting traffic but not ranking —
write for the query rather than for the subreddit. If neither moves in six weeks, the
archetypes are wrong and we rewrite them from what the log actually says.
