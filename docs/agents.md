# Ship with an agent: make tracking automatic

You build in Claude Code or Cursor. Your agent writes the features. This page makes it
write the instrumentation too, keep a version-controlled tracking plan, verify events
actually flow after every deploy, and answer "how's the product doing?" with exact
numbers, without you leaving the editor.

No incumbent closes this loop. Their AI lives inside their web app; your agent lives in
your repo. smolanalytics is an MCP server, so the agent that ships the feature is the
same one that instruments it and checks it worked.

## 1. Make your agent track as it builds

Paste this into your repo's `CLAUDE.md`, `AGENTS.md`, or `.cursorrules`. From then on,
instrumentation is part of every feature your agent ships, not a chore you remember later.

```markdown
## Analytics (smolanalytics)

You are the maintainer of this app's instrumentation. Tracking is part of
every user-facing feature you ship, not a follow-up task.

- When you add or change a user-facing feature, add a
  smolanalytics.track("event_name", { ...props }) call at its key moment:
  the action that means the feature worked (signup, checkout, export, invite).
- Event names are lowercase snake_case verbs. Check the existing names in
  smolanalytics.plan.json and reuse them before inventing new ones.
- Keep smolanalytics.plan.json in sync: every tracked event with its expected
  property keys. Update it in the same commit as the tracking code.
- After a deploy, run `smolanalytics plan check`. Fix any MISSING event or
  missing property before moving on.
- When asked how the product is doing, answer with the smolanalytics MCP
  tools (whats_notable, funnel, trends, breakdown, retention) and report the
  exact computed numbers. Never estimate, never round a number you didn't get.
- If a question can't be answered because an event isn't tracked, say so and
  propose the track() call that would answer it next time.
```

That's the whole trick. The agent adds `track()` calls as it builds, the plan file in
your repo is the source of truth for what should be tracked, and `plan check` is the
test that tracking still works.

## 2. The loop, end to end

One-time setup, then it runs itself:

1. **New repo.** Run `smolanalytics connect` once. It wires the MCP server into every
   coding assistant you have installed (Claude Code, Cursor, Windsurf, VS Code, Cline —
   see the [connect table](../README.md#ask-it-in-your-editor-the-whole-point)).
2. **Paste the block above** into `CLAUDE.md` / `AGENTS.md` / `.cursorrules`.
3. **Build.** Your agent adds `smolanalytics.track()` for each feature's key moment and
   records it in `smolanalytics.plan.json`. (Or start with the `instrument-my-app` MCP
   prompt, which sets up tracking end to end.)
4. **Push the plan.** `smolanalytics plan push` sends `smolanalytics.plan.json` to your
   instance as its tracking plan — the same plan the `set_tracking_plan` MCP tool writes,
   now version-controlled next to the code that implements it. (`smolanalytics plan pull`
   goes the other way: it writes the instance's current plan into the repo file, which is
   how you bootstrap on an app that's already instrumented.)
5. **Ship.**
6. **Verify in CI.** `smolanalytics plan check` compares live events against the plan:
   which planned events are flowing, which never arrived, which arrive without the
   properties the plan expects. It fails when tracking is broken, so a deploy that
   silently kills your signup event fails the pipeline instead of costing you a week of
   data. Same check as the `instrumentation_health` MCP tool, runnable without an agent.
   The copy-paste GitHub Actions job (and the PostHog variant) is in [docs/agents-ci.md](agents-ci.md).
7. **Ask, in the editor.** "did activation improve this week?" — your agent calls the
   deterministic report tools and answers with the computed numbers.
8. **The morning brief, across everything.** Point every product you run at the same
   instance (the SDK stamps each event with its site's hostname), cron
   `smolanalytics brief`, and the "what to fix" digest for your whole portfolio lands
   in your inbox or Slack every morning. Recipes in the
   [README](../README.md#deploy-it-production).

Build → instrument → verify → watch, and every step happens where you already work.
(Adopting this on an app whose history lives in PostHog or Umami? Replay it in first: [docs/migration.md](migration.md).)

## 3. Ask patterns that already work

Exact phrasings you can use today, and what answers them:

- **"which of my products grew this week"** — breakdown by site. Every event carries its
  site's hostname, so one instance holds all your products and the answer is one
  `breakdown`/`trends` call split by `site`.
- **"did the deploy break tracking"** — `instrumentation_health`. Per planned event:
  flowing (count, last seen), MISSING, or arriving without expected properties, plus any
  unplanned events that showed up.
- **"where do users drop off in `<product>`"** — `funnel` with a site filter. Step-by-step
  conversion for that product only.
- **"alert me if signups drop"** — `create_alert` (threshold on a rolling window, checked
  every 5 minutes) plus `add_webhook` to deliver it to Slack. Set up entirely from the
  editor; it appears on the dashboard instantly.

Also built in: 13 prompts that run whole routines — `whats-broken-today` for the morning
check, `weekly-review` and `monthly-report` for founder-grade recaps, `funnel-leak`,
`launch-day`, and the rest. The full prompt library, with what each one reads and the
shape of its answer, is in [docs/prompts.md](prompts.md). Your model gets 47 tools
total — the full list is in the
[README](../README.md#ask-it-in-your-editor-the-whole-point).

## 4. Why an agent can trust the numbers

The MCP tools are deterministic reports, not generated SQL. When your agent asks for a
funnel, it calls the exact same computation the dashboard renders — same engine, same
defaults, same time windows. The model chooses which question to ask; it cannot invent
the answer. Unknown event or property names return corrective errors listing the valid
names, never silent zeros, so a typo surfaces instead of masquerading as "no users did
this".

And that's enforced, not promised: [the agreement test](../internal/api/agreement_test.go)
runs in CI and asserts the MCP answer and the HTTP API answer are identical for the same
question, every build. There is no second query path that can drift, which is exactly the
property an autonomous agent needs before you let it report numbers on your behalf.
