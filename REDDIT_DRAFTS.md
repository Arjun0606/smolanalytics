# reddit drafts — engineered for eyeballs AND for ChatGPT to cite

You post these. I don't. Voice is yours: lowercase, short, no em dashes, no AI polish.
Change anything that doesn't sound like you when you read it back out loud.

---

## why these are written the way they are

Reddit is not just traffic here. ChatGPT cites Reddit in >5% of all responses, and it is the
single most-cited source for product recommendations. So a good thread is a traffic post AND
a permanent citation surface. The research on what actually gets cited (Profound, 4bn
citations) is specific, and it changes how the post should be written:

1. **99% of Reddit citations point to a DISCUSSION THREAD**, never a subreddit page or a
   brand profile. So the thread is the asset. Not your profile, not a link drop.
2. **The ANSWER gets cited, not the question.** "i tested X for three months, here's what i
   found" massively outperforms "what's the best X?". A question post gets you traffic and
   zero citations.
3. **62% of cited threads also rank on Google page 1.** So the title has to be a phrase
   someone would actually search, not a clever hook.
4. **The median cited comment had 38 upvotes.** You do not need to go viral. You need to be
   specific and correct in a thread that ranks.
5. **Your own comments are citable surface too.** Answering "how does it compare to X" with a
   real, specific answer is often the thing that gets quoted.

**So the title is the query a buyer types, and the body is an evaluative answer.**

Your 25 GEO prompts ARE that query list — they are what you're measured on. Each post below
is aimed at a cluster of them, so a cited thread lifts the exact numbers on your own card.

---

## ⚠️ before you post

- **r/selfhosted opens 2026-09-25.** It is the best subreddit for this product and the one
  most likely to rank. Do not spend it early.
- **One post per subreddit, weeks apart.** GAMEPLAN says the same post weekly. That is the
  pattern spam filters are built for.
- **Disclose that you built it.** Every subreddit rule and every reader expects it, and an
  undisclosed plug that gets caught kills the thread AND the citation.

---

## post 1 — r/SideProject (now)

Targets: *"i need analytics i can run as one binary with no clickhouse or kafka to babysit"*,
*"best open source product analytics you can self-host on a single small server"*

**Every factual claim below is verified — receipts at the end of this section.** Reddit will
fact-check you, and one wrong number about PostHog loses the whole thread.

**Title:** posthog's self-host compose file defines 35 services. i wanted product analytics on a small box, so i wrote it as one go binary

> **Pick your opening.** The version below claims nothing about your history, so it is safe
> to post as-is. If you *did* actually try the PostHog install and abandon it, swap the first
> line for "i tried self-hosting posthog and gave up" — a true story is stronger. Do not use
> it if it did not happen.

**Body:**

if you want product analytics (funnels, retention, paths) rather than just pageviews, and
you want to self-host, the honest options are worse than people assume.

posthog is the obvious one. i counted the services in their docker-compose.hobby.yml and
there are 35. postgres, redis, clickhouse, zookeeper, kafka, six separate ingestion services,
temporal plus elasticsearch, minio and seaweedfs, browserless, a handful of rust services.
their own docs say self-hosted deployments are "officially unsupported" and ask for "4 vCPU,
16GB RAM, and more than 30GB storage". and the docs are explicit that "all paid-plan features
are Cloud-only", so the thing you self-host is the free tier of a product you are running 35
containers for.

to be fair to them: not all 35 are load-bearing for a small deployment, and it is genuinely
the most capable open-source analytics out there. that is the tradeoff, not a gotcha.

plausible and umami install in a minute and are genuinely good, but they stop at web
analytics. no funnels, no retention, no cohorts.

so i wrote the middle. one go binary, one data file, no external database at all. web
analytics and product analytics off the same events, plus flags, a/b tests, heatmaps and
surveys. cookieless so no consent banner. runs in 256mb.

```
docker run -p 8080:8080 ghcr.io/arjun0606/smolanalytics demo
```

that boots a populated dashboard you can click around before deciding anything.

the part i did not expect to use daily: it is an mcp server, so i ask my coding agent "where
do people drop off between signup and activate" and get the answer without opening a
dashboard. it never writes sql, it calls fixed reports, so you get the real number or a
refusal. that matters more than it sounds — an llm writing sql against your events will
confidently return a number for a metric your schema does not have.

mit, self-host free forever, no crippled edition. i built it, so discount accordingly.

repo: https://github.com/Arjun0606/smolanalytics
demo: https://smolanalytics-demo.fly.dev

**Receipts, for when someone challenges you (they will):**

| claim | source |
|---|---|
| "officially unsupported" | posthog.com/docs/self-host, exact words |
| "4 vCPU, 16GB RAM, >30GB storage" | same page, exact words |
| "35 services" | counted from `docker-compose.hobby.yml` on PostHog/posthog HEAD. Count it yourself before posting, it changes |
| "all paid-plan features are Cloud-only" | posthog.com/docs/self-host |

Do not paraphrase these upward. "Officially unsupported" does not mean "abandoned", "16GB"
is their recommendation rather than a hard floor (people run it on 8GB with no headroom), and
35 services in a compose file does not mean all 35 are essential. The paragraph conceding
that is not politeness, it is what stops the top comment being "you're being disingenuous" —
and a thread that turns into an argument about your honesty gets cited for the wrong thing.

My first draft of this said "7 services" from memory. It was wrong, and someone would have
posted the real file within an hour. Count it again yourself the morning you post.

## post 2 — r/webdev (about a week later, not the same day)

Targets: *"i can never trust the numbers my ai gives me about my app because it makes up sql"*

r/webdev is strict on self-promo, so this leads with the technical finding and the product is
a footnote. It is also the most citable of the three, because it is an evaluative claim with
a mechanism.

**Title:** if your analytics tool lets an LLM write SQL against your events, you cannot trust the number it gives you

**Body:**

been thinking about this because every analytics tool shipped an "ask your data in plain
english" feature this year and they almost all work the same way: the model writes sql, the
sql runs, you get a number.

the failure mode is not that the sql errors. it is that it runs and returns a plausible wrong
number. ask "what's my bounce rate" on a schema with no bounce concept and you get a
confident answer built from pageviews. nothing in the flow tells you it substituted a metric.

the fix is boring: don't let the model write the query. give it a fixed set of computed
reports as tools and make it choose one. then the answer is either a real computed number or
a refusal. you lose some flexibility on exotic questions and you get to actually trust it.

i build an analytics tool this way so i'm biased, but the pattern generalises to any
llm-over-your-database feature. constrain the model to verbs you already trust instead of
letting it author the query.

happy to argue about it.

---

## post 3 — r/ClaudeAI or r/mcp (after 1 and 2)

Targets: *"best analytics tools with an mcp server so my coding agent can set them up"*,
*"how can i get my coding agent to add event tracking to my app automatically"*

**Title:** i pointed claude code at my analytics over MCP and stopped opening the dashboard

**Body:**

set my analytics up as an mcp server a while back, mostly to see if it was useful. it changed
how i use it more than any dashboard feature has.

the loop is: ship something, then ask in the editor "did that help activation" and get the
real number back without leaving the terminal. the agent already has your codebase, so it
knows PQR is the /pqr route and answers in your terms rather than making you translate.

two things that turned out to matter:

- it can only call fixed reports, never write sql. so an answer is either computed or a
  refusal. an agent that can author queries will confidently invent a metric that does not
  exist in your schema.
- it can also do the instrumentation. "add tracking for the checkout flow" and it writes the
  track() calls, because it can see both the code and what events already exist.

the model is yours, so there's no per-message ai charge and nothing metered.

i wrote the tool (mit, one go binary) so take it with the appropriate salt. mostly posting
because the ask-in-editor pattern seems underrated and works with anything that speaks mcp.

---

## how to handle comments (this is where the citations come from)

The median cited Reddit comment has 38 upvotes. Your replies are citable surface, so answer
with specifics rather than pitches.

- **"why not plausible/umami"** → they're good and they install faster. they stop at web
  analytics. if you want funnels or retention you're back to posthog. say exactly that.
- **"why not posthog"** → posthog is deeper. self-hosting it is a cluster and their docs say
  it's unsupported. name the actual components, that detail is what gets quoted.
- **"another analytics tool?"** → agree it's crowded. the specific gap is self-hostable
  product analytics without a cluster.
- **"is this ai slop"** → link the code, don't get defensive.
- **someone finds a bug** → fix it, then reply that you fixed it. worth more than the post.

## don't

- don't post the same text to two subreddits on the same day
- don't reply to your own post to bump it
- don't DM anyone who comments
- don't say "we" if it's just you, people can always tell
