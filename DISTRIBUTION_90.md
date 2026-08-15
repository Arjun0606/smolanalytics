## 0. THE HONEST CEILING

**"As big as Autonoma" is the wrong target and should be deleted today.** Autonoma is a 17-person pre-seed company with no revenue figure on Crunchbase, PitchBook or Tracxn. Its logo wall came from its cap table (Rauch angel, Bessemer led the round and sits on a customer's board, two LatAm unicorns from the founders' home market). Its measurable open-source output, 175 stars and a 5-point Show HN, was beaten in nine days by one unfunded solo dev (Rybbit, 5,000 stars) who converted that into roughly $1,500. You would be copying the marketing outputs of a company that has not proven it can make money, minus the one input that actually produced its logos.

**Probability-weighted, 12 months from today, for you specifically** (part-time, no network, no budget, parallel bets, India):

| Outcome at 2027-08-15 | P |
|---|---|
| Dormant or abandoned | ~40% |
| $1 to $500 MRR | ~25% |
| $500 to $2,000 MRR | ~20% |
| $2,000 to $8,000 MRR | ~12% |
| Above $8,000 MRR | ~3% |

P(100 paying customers at any price within 12 months): **15-20%.** P(100 at $199, i.e. ~$20k MRR): **1-2%.**

Calibration, all measured: Plausible, the category's bootstrapped winner, working full time with a career growth marketer as the second half of the company, took 324 days to reach $400 MRR and 3 years 1 month to $1M ARR. Rybbit ran the best permissionless open-source analytics launch of the era and made ~$1,500 in month one. Coolify converts 154,000 self-hosters into 1,700 cloud customers at ~1.1%. Nothing in the evidence supports 100 paying customers in 90 days, and I could not find a single documented case of a zero-network, zero-budget solo founder doing it.

**The target is MRR, not logo count.** 100 customers at $19 is $1,900 and a support job. Set this instead:

- **12 months: $3,000 MRR.** At a blended $75 ACV that is 40 customers.
- **90 days (2026-11-15): 8 paying customers, $400 MRR.**

The modal outcome is abandonment, and it gets decided between month 9 and month 15. The plan below is built to force that decision early instead of late.

**One positioning decision before anything else:** the buyer is a 2-15 person engineer-led team, entry price $49. Every channel available to you delivers solo devs and small teams. Keep $199 Team on the page as the anchor and for self-selecting inbound, but do not forecast against it and do not build a sales motion for it. You have ruled out calls, and a $199 procurement buy without calls is not a thing that happens at zero brand.

---

## 1. THE ONE CHANNEL

**Hand-delivered ship-ledger backtests, sourced from people who publicly described the pain, republished afterwards as the content.**

The mechanism: free keyword alerts (F5Bot covers Reddit, HN, Lobsters) on *pain phrasing*, not on your brand. When a stranger publicly writes "we shipped the redesign and I genuinely can't tell if it helped" or "our PostHog bill just tripled" or "we ran the test and never looked at it", you reply in-thread with a real answer, and offer to run a 90-day backtest on their repo and their data, by hand, free. You deliver it as a written adjudication. The ask for the card is the last paragraph of that document.

**Why it beats the alternatives for you:**

- It is the only channel with a documented first-100 mechanism that requires no audience, no budget and nobody's approval. Every documented first-100 in this corpus (Plausible, PostHog, Simple Analytics) is one-at-a-time human contact. PostHog hand-onboarded over Slack and WhatsApp while editing the database directly.
- **HN essays** are the highest-ceiling channel and a lottery. Plausible's step change (462 points, 166 trials in a week) came from a ten-year professional growth marketer, full time, with two prior 900+ point front pages already banked. Base rate for 100+ points is ~5%. Honest time-to-first-hit at one post a week is 6-9 months. It is a byproduct of this plan, not the bet.
- **SEO** is 12-24 months away. Domain is 42 days old, 707 impressions, 2 clicks, position 52. 1.74% of new pages reach top 10 within a year.
- **A Reddit launch** is Rybbit's play, and Rybbit made $1,500 with it. Worse, the subs that permit standalone launch posts (r/selfhosted, r/SideProject) are definitionally full of people who will `docker run` your MIT single binary and never pay, and the subs with budgets are gated: r/ExperiencedDevs requires mod approval (someone else's yes), r/devops and r/startups confine promotion to weekly threads.
- **MCP directories, llms.txt, GitHub stars**: measured zeros on your own instance. Registry since 2026-07-28 and Glama listing have produced zero referral visits in 30 days.

**Two structural reasons this is the right bet beyond yield:**

One unit of work produces four assets: a customer conversation, a case study, a blog post, and an HN/Reddit artifact. Nothing else at your hour budget compounds like that.

And it is the only channel that tells you whether the ship ledger is a real product before you spend a year selling it. A 20-person company shipping five changes a week does not have the traffic to adjudicate anything (detecting a 20% relative lift on a 3% baseline needs ~3,800 visitors per variant). If the first five backtests all return "insufficient data", you have learned in week 8 what you would otherwise learn in month 11. Build the honest low-data path now: zero-adoption detection needs counts, not statistics, and works at any traffic level.

---

## 2. WEEK BY WEEK (Aug 17 to Nov 15)

**Standing rules for all 13 weeks.** No new features unless a named human asked in the last 7 days. One number on the wall: card-on-file trials started this week. Budget ~21 hrs/week: 7 on replies, 6 on the week's backtest, 8 on the week's build/write item.

**Weeks 3 onward carry a fixed spine:** 25-30 substantive in-thread replies per week (no link unless asked) and one backtest delivered per week. The week item below is what changes.

| Week | Dates | Item | Expected output |
|---|---|---|---|
| **W1** | Aug 17-23 | **Make the money path real.** Kill $19 for new signups; entry becomes Pro $49, Team $199 as anchor. Require a card on the 14-day trial (measured: card-required trials convert 25-35% vs 4-6% without, n=200). Create `lib/facts.ts` with PRICE_PRO / PRICE_TEAM / OVERAGE / MCP_TOOL_COUNT, import everywhere, CI grep fails the build on a bare `$19`/`$9`/`$29` outside it. Right now all 42 `/for` pages say $19 + $6/M while `/pricing` says $49 + $8/M, and that is what ChatGPT quotes to buyers. Run `smolanalytics gsc auth`. | Coherent price, card gate live, GSC connected. 0 customers. |
| **W2** | Aug 24-30 | **The offer and the instrument.** Ship `/backtest`: one page, one paragraph, email + repo, nothing else. Cut the homepage to one sentence, one screenshot of a real ledger verdict, one button; delete the feature list (Plausible, same category, 2026: homepage simplification lifted trial signups 84% on +2% traffic, visitor-to-trial 2.65% → 4.80%). Homepage is 75% of your entries and the only page with volume. Set F5Bot on pain phrases, not brand. | Offer live, homepage rebuilt, alerts running. |
| **W3** | Aug 31-Sep 6 | **Backtest #0, on yourself.** Adjudicate every change smolanalytics has shipped since 2026-06-26. Publish it with the failures in it, with recompute digests. This is simultaneously the demo, the sales asset and the future HN ticket. Start the reply habit. | 1 published backtest, 30 replies. 0-2 requests. |
| **W4** | Sep 7-13 | **First outside backtest.** Deliver to a real stranger, 4-8 hours, as a written document, not a dashboard login. Card ask is the last paragraph of the doc. | 1 delivered, 1-3 card-on-file trials. |
| **W5** | Sep 14-20 | **Indexation surgery part 1** (section 3, items 3-5). | 42 thin pages → 8 real ones. |
| **W6** | Sep 21-27 | **Indexation surgery part 2 + third-party corpus.** Sitemap split, /best and /glossary linking, request-indexing batch. Then one 4-hour sitting: AlternativeTo, SaaSHub, LibHunt, StackShare, Slant, SourceForge, OpenAlternative, free G2 profile, PRs to punkpeye/awesome-mcp-servers (91.7k stars, fast-tracks agent PRs), wong2, appcypher, oxnr/awesome-analytics. | ~12 third-party placements. You currently have close to zero corpus you don't own, and 84-93% of AI citation weight sits on pages you don't own. |
| **W7** | Sep 28-Oct 4 | **Gate 1 (Oct 2).** Read the numbers in section 5. Publish backtest #2 anonymized. | Go / adjust / pivot decision, in writing. |
| **W8** | Oct 5-11 | **The HN ticket.** Submit the ship-ledger post as a *regular link*, never Show HN. Flat title with the number in it: "We shipped 41 changes in 90 days. 27 did nothing." Do not submit "open source analytics, single Go binary, MCP server" — that exact post was made by someone else on 2026-08-14 and got 5 points, and two more like it died the same week. | ~5% chance of a real traffic event. Cross-post to Indie Hackers only (Lobsters is invite-only, 70-day new-user gate). |
| **W9** | Oct 12-18 | **Reddit, once, aimed at the wedge.** The artifact is the post, not the product: a ship ledger of a real public repo. r/SideProject and r/analytics standalone; r/devops and r/startups via their weekly threads. Hand-written, one sub at a time. | 3-15 trials if it lands, 0 if it doesn't. |
| **W10** | Oct 19-25 | **Convert the backtests into assets.** Case-study pages from every delivered backtest. Migration pages for Eppo (platform scrapped by Datadog) and Statsig (absorbed into Amplitude) users, a real time-boxed switching window. Three unedited 3-minute screencasts using the machine you already built, titled as the buying queries verbatim. | 4-6 durable pages, 3 videos. |
| **W11** | Oct 26-Nov 1 | **awesome-selfhosted PR opens Nov 1** (4-month release rule clears; v0.1.0 shipped 2026-07-01, submitting early burns the shot). Second HN link post from the accumulated backtest corpus. | 1 high-authority listing in flight. |
| **W12** | Nov 2-8 | **Nothing new. Convert.** Go back to every trial that did not convert, every backtest recipient, every thread where someone replied. Hand-onboard anyone still warm, PostHog-style, 30-minute response standard. | The largest single-week conversion yield of the quarter. |
| **W13** | Nov 9-15 | **Gate 2 (Nov 15).** Read section 5. Decide in writing the same day. | Continue / park. |

---

## 3. THE INDEXATION FIX

**It is not a mystery and it is not one problem.** The facts: domain created 2026-07-04, so it is 42 days old. All 161 sitemap URLs return 200, prerendered, self-canonical, `index, follow`, TTFB 0.26-1.1s, robots.txt clean. There is no technical block, and crawl budget is definitionally not the constraint at 161 URLs. Against a 16-million-page benchmark (27.4 day average time-to-index, 64.9% within 30 days), you are on the curve. So a large part of "61 discovered, not indexed" is age plus authority and cannot be fixed with code.

But there are four real defects, and they are the ones you can act on:

1. `app/for/[slug]/page.tsx` prints `p.intro` three times per page (hero sub, `<Citable>`, `faqs[0]`). Measured: 12-15% intra-page 8-gram duplication on generated pages vs 1.0-1.3% on hand-written ones.
2. `faqs[1]` and `faqs[2]` are hardcoded identical across all 42 generated pages, and emitted into FAQPage JSON-LD on all 42.
3. The 42 generated pages average **232 unique words**. Autonoma's comparable pages are 2,100-7,100.
4. Price is wrong on all 42.

**The sequence, with dates:**

- **W1.** `lib/facts.ts` + CI grep. Connect GSC and **export the actual list of the 61 URLs.** Everyone including me has been guessing which cluster is in the bucket. `lib/for-graph.ts` already gave every `/for` page in-degree 4 on 2026-07-28; `/best` (1-11 inbound, and no component in `components/` links to any of them) and `/glossary` (16 pages) are the starved ones, and `/best` is where your only organic entries come from.
- **W5.** Delete the 3x intro repeat. **Delete `faqs[1]` and `faqs[2]` entirely.** Do not write 84 bespoke variants: the cookie-banner answer is identical for SvelteKit, FastAPI and Laravel, and 42 rewordings of one true fact is spinning, which sits inside Google's scaled-content-abuse definition.
- **W5.** **Merge the 42 generated pages into 8** with 301s: python (flask/fastapi/django), node-backend (express/go), react-spa, svelte-astro-solid-qwik, mobile (flutter/rn/ios/android), hosting (vercel/railway/cloudflare/netlify), agent-editors, no-code. **Leave the 20 hand-written pages alone** (cursor, claude-code, lovable, bolt, replit, v0, windsurf, nextjs and the rest); they measure clean at 900-1,055 words and are where the demand actually is.
- **W5.** For each of the 8 survivors, actually run the SDK against a minimal app on that stack and publish the **real captured-event table**: event names, property keys, what fires on client-side navigation, what doesn't. One stack per evening. That is the "unique data per page" the pSEO evidence says separates indexed from not, and it is the one thing no competitor can copy.
- **W6.** Sitemap index split into 7 children (for / vs / blog / glossary / alternatives / best / core) so coverage becomes diagnosable per cluster instead of one opaque number. Drop `<priority>` and `<changefreq>`, which Google ignores. Derive `<lastmod>` from `git log`, not `new Date()` at build time (you currently have 16 distinct timestamps across 161 URLs, which Google discounts as not verifiably accurate).
- **W6.** Ring-link `/best` (4 pages) and `/glossary` (16) using the pattern `for-graph.ts` already proves.
- **W6.** Request indexing on exactly 12 URLs: the 8 hand-written agent pages plus `/pricing`, `/proof`, `/instrument`, `/mcp`. Measured success rate 29.4%, quota ~10-12/day. Do not spend it on `/for/carrd`.

**Do not:** rebuild IndexNow (already wired, running since 2026-08-02, and Google has never adopted it); publish a single new programmatic page; touch internal links again before Oct 2, which is the pre-registered 4-week read on the for-graph experiment; delete anything beyond the merge above.

**Gate, 2026-10-02 (domain day 90):** if the 8 consolidated pages are still "discovered, currently not indexed" and total indexed coverage is under 60% of the reduced sitemap, **stop all Google SEO work until 2027** and reclassify the pages as AI-retrieval assets only. That is not defeatism, it is where your traffic actually is: in 13 days AI crawlers hit you 1,010 times across 219 pages including 89 live-assistant fetches, Bingbot hit you 252 times, and Googlebot does not appear in the log at all. Google sent 6 visits in 30 days. Bing sent 10.

---

## 4. WHAT TO STOP DOING

- **Building features.** 94 MCP tools, 4 native SDKs, flags, A/B, heatmaps, surveys, autofix cron, GitHub App, guardrails with auto-revert, all shipped to zero customers. That is Kite's exact sequence (team → product → distribution → monetization) at solo scale, and Kite had 500,000 monthly active developers when it died.
- **Counting GitHub stars as progress.** 2 stars, 1 fork, 0 watchers in 7 weeks. Rybbit's ~12,000 stars produced ~$1,500. Remove stars from the scoreboard entirely.
- **MCP directory submissions.** Official registry and Glama both live since July 28. Zero referral visits in 30 days, in a field of 72,476 servers on Glama alone. Keep the entries current (automated already) and spend zero further hours. Fix the stale description ("79 tools") once, in W1, as part of the facts.ts pass.
- **llms.txt.** 97% of llms.txt files received zero traffic across 137,210 domains, and AI bots never request one that doesn't exist. The file is already there. Never expand it.
- **Publishing new programmatic pages.** Autonoma's 41-page batch worked because it landed on a 527-post blog with investor-driven PR authority. You have neither. Copying the artifact without the authority reproduces your current GSC reading.
- **The Product Hunt alternatives tier** (DevHunt, Uneed, MicroLaunch, TinyLaunch, Peerlist). No published outcome data exists for any of them; every article recommending them is written by one of them. Product Hunt itself: half a day, once, for the backlink, nothing more (Plausible's PH launch produced 15 trials).
- **winget, nixpkgs, Snap, Homebrew core.** Wrong platform for a Linux/Docker Go server, and Homebrew core is hard-gated at 75 stars against your 2. A Homebrew tap is one hour and permissionless; core is not.
- **Everything requiring someone else's yes.** Delete from the planning doc so it stops consuming attention: Vercel Marketplace (500 installs then email), Anthropic's official plugin directory, Docker MCP catalog, G2 paid tiers, AppSumo/SaaSMantra LTDs, standalone r/ExperiencedDevs posts, Cursor's curated directory. The permissionless substitutes are: your own Claude Code marketplace repo, and "Add to Cursor" / "Add to VS Code" one-click MCP deeplinks in your README. Thirty minutes, W2.
- **The $19 tier.**
- **Forecasting against $199.**
- **Building any GEO tooling for yourself.** You already own the instrument. Use `ai_crawlers` and `ai_visibility`; buy nothing.

---

## 5. KILL CRITERIA

Three gates, all on numbers you already collect, all specific enough to be unarguable.

**Weekly tripwire, every Friday from 2026-09-04:**
Card-on-file trials started this week. **If it is 0 for three consecutive weeks while you are still committing code**, the following week you write no code at all: replies and backtests only. No exceptions, including "I was only fixing a bug."

**Gate 1, 2026-10-02.** Read all four:
- **≥ 5 public pain threads found per week, sustained.** If you cannot find five people a week describing the ship-ledger pain in their own words, the category has no demand and no volume of pages will manufacture it. In that case pivot the headline to the one query you already rank #1 on ("best analytics for apps built with Cursor or Claude Code") and demote the ship ledger to a feature.
- **≥ 4 outside backtests delivered.** Under 2 means the offer is not compelling enough to be *given away*, and it will never be compelling enough to sell.
- **≥ 3 card-on-file trials, cumulative.**
- **Indexation gate as specified in section 3.**

**Gate 2, 2026-11-15.** Two numbers:
- **≥ 8 paying customers with a card charged at least once.** Not trials. Not signups. Not stars. Not installs.
- **≥ $400 MRR.**

**Both missed:** smolanalytics is not the bet. Park it, keep the cloud running, put the hours on whichever parallel bet is closer to money. With ~₹3,000 in the bank, the cost of being wrong for another six months is not abstract, and your documented strength is cutting cleanly when the math doesn't pencil. This is that call.

**Exactly one missed:** you know which variable failed. Customers hit and MRR missed means the price is wrong. MRR hit and customers missed means the channel is too narrow. Fix the named one, extend 60 days to 2027-01-14, and no further extension.

**Both hit:** you are in the 12% branch. Then the only question that matters is full-time or not, and MicroConf's n=700 measured full-time founders growing 2.2x faster than part-time ones.

**The rationalisations that are not admissible at either gate:** impressions are up, stars are up, crawler hits are up, the product got much better, the last two weeks were unusual, the awesome-list PR hasn't merged yet. None of those are on the gate. Every one of them will be true on 2026-11-15 regardless of whether this works.

Write these two numbers, and the date, somewhere you cannot avoid looking at them, today:

**8 customers. $400 MRR. 2026-11-15.**