# smolanalytics GTM playbook — the escape (Jul 2026)

Positioning: the FIRST-CHOICE analytics for indie builders / small startups / vibe-coders shipping on Bolt, Lovable, Replit, v0. We are the approachable tier (like Bolt/Lovable are to Claude Code/Cursor). NOT the enterprise BI tool. We win at the moment of app CREATION inside an agent, never in a Mixpanel bakeoff.

Wedge (un-owned, do NOT lead with privacy — that slot is saturated/owned by PostHog+Plausible): provably-correct AI answers (CI-proven determinism, competitors' AI hallucinates), free BYO-model AI, one-paste agent instrumentation.

## The ranked playbook

### 1. Docs-as-installer: publish /install.md + llms.txt + AGENTS.md so a Lovable/Bolt/Replit builder pastes ONE URL into their agent and it installs the binary, calls create_api_key, runs propose_instrumentation, and confirms with verify_instrumentation — fully autonomous, zero email, zero UI.
- **Week 1:** Ship smolanalytics.com/install.md in Mintlify's OBJECTIVE / DONE-WHEN / TODO-checkbox format (OBJECTIVE: instrument this app with smolanalytics; DONE WHEN: verify_instrumentation returns healthy). Add llms.txt (curated: MCP endpoint, quickstart, create_api_key, BYO-model note) and AGENTS.md. Landing-page hero becomes literally: 'Paste this into Cursor/Lovable/Bolt: [URL]'.
- **Why (evidence):** Mintlify's install.md standard is live on Cerebras/Firecrawl/Langchain; the corpus flags this as 'the highest-leverage, lowest-cost mechanic on the list for a 0-user solo founder' because smol already exposes every primitive the agent needs. Netlify's AX pivot (3k→40k daily signups, 96% non-pro-devs) proves making the agent the installer is how you win the vibe-coder tier.
- **Effort:** Low — 1-2 days, pure docs, no new product code.
- **Leading indicator:** Count of create_api_key calls originating from agent sessions (not the dashboard) per week; secondary: verify_instrumentation success events.

### 2. npx setup-wizard the builder's own AI agent runs: `npx @smolanalytics/wizard` auto-detects the stack (Vite/React/Next), proposes a starter event set from the codebase, installs the SDK, and verifies events flow — the exact PostHog/Amplitude 'become-the-default-analytics' mechanism, wrapped so Cursor/Bolt/Lovable agents invoke it.
- **Week 1:** Build the wizard as a thin npx script wrapping propose_instrumentation → verify_instrumentation, with a one-liner 'run this prompt in your coding agent' snippet. Detect client (Cursor/Claude Code/Codex/VS Code/Zed) and install the MCP everywhere in one step. Gate behind a generous free events tier so trying is free (BYO-model means the AI is already free).
- **Why (evidence):** PostHog (`npx @posthog/wizard`, wraps Claude Agent SDK, explicitly 'run with Cursor and Bolt') and Amplitude (`npx @amplitude/wizard`, '10-20 min, auto-detects Vite+React') both run this exact play in this exact category. This is not experimental — it's the proven category-winning motion, and smol's provably-correct + free-AI angle is a strictly better wizard payload.
- **Effort:** Medium — 3-5 days; most logic already exists behind the MCP tools.
- **Leading indicator:** Weekly wizard completions (install started → verify_instrumentation healthy); the drop-off between the two is your onboarding bug list.

### 3. One opinionated, incumbent-attacking Show HN launch post with the AGENT-NATIVE wedge (NOT privacy), product buried at the very end — the single event that historically changes the trajectory.
- **Week 1:** Write ONE ~2,000-word teardown: 'Your analytics AI is lying to you — and your vibe-coded app has no analytics at all.' Lead with the pain (Mixpanel/Amplitude/PostHog AI hallucinates numbers; a CI test proves smol's AI/MCP/dashboard/API always return the same number), then 'the app you built on Lovable last week has zero instrumentation and no way to ask what happened.' Product mentioned only in the last paragraph. Ship same-day to HN (Show HN, no YC tag), Lobsters, r/selfhosted, r/webdev, r/SideProject.
- **Why (evidence):** Plausible's single post drove 48k visitors/166 trials in week one ('one blog post changed traction'). PostHog's Launch HN (282 pts, 200+ signups) led with the wedge normie tools can't touch. Simple Analytics' one Show HN (#1 for 9 hrs) produced 30-40 paying customers overnight. The corpus is explicit: privacy is saturated in 2026 — the un-owned wedge is provably-correct + agent-native.
- **Effort:** Medium — 1 day writing, but requires ranks 1-2 shippable first (the post must have a working paste-to-install demo).
- **Leading indicator:** HN front-page dwell + signups in the 48 hrs after; benchmark ~50-200 signups (not thousands — the OSS-privacy first-mover slope is gone).

### 4. Demand-capture SEO: 'How to add analytics to Lovable / Bolt / Replit / v0' attack pages (that trash each platform's shallow built-in analytics) + durable 'vs PostHog / vs Mixpanel / vs Plausible / vs GA4' comparison pages.
- **Week 1:** Publish 4 platform pages ('The built-in analytics on Lovable tells you people arrived — not what they did. Here's how to add real, ask-in-your-editor analytics in one paste') and 4 vs-pages leading with the buyer's exact search phrase in the H1 + 3 checkable differentiators (provably-correct numbers, free BYO-model AI, single open binary). You already have the teardown facts in memory.
- **Why (evidence):** Amplitude runs the identical 'How to add analytics to Lovable' attack page verbatim; Plausible's /vs-google-analytics and intent pages are a core compounding channel; Cal.com/Umami won by capturing search demand people were already typing. This is exactly on-positioning — it intercepts the vibe-builder mid-search.
- **Effort:** Low-Medium — 8 pages, ~2 days; compounds at zero ongoing cost.
- **Leading indicator:** Organic impressions/clicks on the platform + vs pages (Search Console) at 30/60 days; assisted signups from those landing pages.

### 5. The permissionless rails INTO the vibe platforms that need no email: a Vercel 'connectable account' integration (OAuth redirect, self-lists with a Community badge, works inside v0) + documented custom-MCP-connector setup for Lovable / Base44 / Cursor.
- **Week 1:** Ship the Vercel connectable-account OAuth integration (reuse the OAuth-2.1 server pattern already built for Cabbge on 2026-06-13) that injects smol env vars into the user's project — self-lists, no integrations@vercel.com approval. Write a 3-line 'paste this connector URL' doc for Lovable/Base44/Cursor custom MCP.
- **Why (evidence):** Corpus confirms Vercel's two-tier split: 'native' is BD-gated but 'connectable account' is self-serve with a Community badge and downloadable by external users. Lovable explicitly supports custom MCP chat connectors; Base44 documents connecting an analytics MCP. So smol is addable inside these platforms TODAY without being on any partner list — the permissionless principle holds.
- **Effort:** Medium — 2-3 days, OAuth server largely reusable.
- **Leading indicator:** Installs of the Vercel Community integration; custom-MCP connector activations traced via create_api_key origin.

### 6. Ship a Claude Code skill/plugin to a GitHub-hosted marketplace ('/plugin marketplace add Arjun0606/smolanalytics') that installs the MCP AND instruments the user's app — a shelf where installs are actually counted and ranked by usage.
- **Week 1:** Create a GitHub repo that IS a Claude Code marketplace containing a smolanalytics skill bundling the propose_instrumentation/verify_instrumentation flow. One CLI line to add, zero emails. List it on claudemarketplaces.com.
- **Why (evidence):** Unlike the MCP directory long tail (52% dead, no demand), the Claude Code plugin ecosystem has real measured installs (claudemarketplaces.com: 300k monthly visitors, top skills 185k-2.5M installs, filtered to actively-used only). Git-native, permissionless, free.
- **Effort:** Low — 1 day.
- **Leading indicator:** Skill install count on the marketplace listing; ranking movement (installs+stars) week over week.

### 7. Hosted remote MCP with OAuth-2.1 one-click + an 'Add to Cursor / Add to VS Code' deep-link button on the docs page; stay in the 17% 'production' tier by keeping the install pipeline green.
- **Week 1:** Expose the MCP as a hosted remote server with one-click OAuth (Cabbge OAuth server is the template), and put deep-link 'Add to Cursor' / 'Add to VS Code' buttons on the quickstart. Keep commits fresh and install green on clean Node.
- **Why (evidence):** Sentry's hosted OAuth MCP does ~85k weekly npm downloads; the corpus audit shows official + hosted-remote + OAuth servers are 11% dead / 71% production-ready vs 74% dead for hobby. One-click removes the highest-friction step for the non-pro-dev tier.
- **Effort:** Low-Medium — 2 days, mostly reusing existing OAuth work.
- **Leading indicator:** Weekly MCP OAuth completions; % of installs that reach a first successful query (overview/trends call).

### 8. Make smol's OWN product dashboard public as the live demo, and put a 'powered by smolanalytics' link on every shared/public dashboard (create_share_link) by default — removing it is the paid/white-label feature. Live proof + compounding backlink loop.
- **Week 1:** Publish smolanalytics.com/demo pointing at smol's real traffic ('this is smol tracking itself — ask it anything'), doubling as the 'live demo' CTA. Add the default badge to create_share_link output; gate badge-removal behind the paid tier.
- **Why (evidence):** Simple Analytics and Buttondown both grew on this exact default-branding badge loop; Plausible's public self-dashboard is its live proof-of-product. Honest caveat from corpus: a dashboard link gets fewer impressions than an email footer, so this is a multiplier on the content/wizard engine, not the cold-start igniter — which is why it's rank 8, not higher.
- **Effort:** Low — 1 day.
- **Leading indicator:** Number of active shared dashboards carrying the badge; click-throughs on 'powered by smolanalytics' links.

### 9. Sustain the engine with news-riding contrarian follow-ups + build-in-public open-startup content, in Arjun's own voice (lowercase, no em-dashes, no AI-polish tells), each backed by a number only smol can produce.
- **Week 1:** After the launch post, queue a monthly cadence: 'we measured how often analytics AIs give three different answers to one question' (self-referential proof using smol's own CI/determinism), plus open revenue/signup numbers as build-in-public. Ride every GA4/AI-analytics controversy within 24 hrs with a strong opinion.
- **Why (evidence):** Plausible's follow-ups ('Google AMP is dead!' 35k readers; the adblocker self-data post) kept the HN engine running; PostHog's founder-authored tactical essays drove ~30% of early inbound. Corpus caveat: content compounds over months and can't manufacture the word-of-mouth smol doesn't yet have — so treat as the sustaining layer, not the spike.
- **Effort:** Low ongoing — 1 post / 2-4 weeks.
- **Leading indicator:** Repeat-visitor and brand-search ('smolanalytics') trend on Search Console; referral traffic from each post.

### 10. Self-serve 20-30% lifetime-recurring affiliate program via Dodo/Stripe with a ready-made content kit + a signup credit — a cheap amplifier that compounds off the first happy cohort.
- **Week 1:** Wire the affiliate program self-serve (no vendor emails), 25% lifetime recurring + a signup credit + pre-made teardown/YouTube content kit. Ship it now but expect ~zero for the first months.
- **Why (evidence):** Fathom built its program 'in three hours' and has paid $100k+; economics work because of 90% trial-to-paid / 2% churn. Corpus is blunt that affiliates ARE happy paying customers, so at 0 users this produces near-zero until ranks 1-4 create a base — hence rank 10: build cheap now, don't rely on it to cold-start.
- **Effort:** Low — half a day (Dodo already in the stack).
- **Leading indicator:** Number of active affiliates who've referred ≥1 paying account; % of new paid signups attributed to referral code.

## Do NOT

- **Do NOT lead your positioning, homepage, or launch post with 'privacy-first open-source Google Analytics alternative.' That slot is saturated and owned. Lead instead with the un-owned agent-native wedge: provably-correct AI answers (the CI-proven determinism no competitor has), free BYO-model AI, and one-paste agent instrumentation for vibe-coded apps.**
  - proof: PostHog case (plus Fathom/Plausible/Umami): the corpus states OSS-privacy is '2026 saturated, PostHog owns it,' and PostHog's own launch worked precisely because it led with a wedge the incumbents couldn't touch, not with 'privacy.' Fathom and Plausible already occupy the GA-alternative privacy narrative — entering there means fighting funded incumbents on their turf with zero audience.

- **Do NOT chase the BD/email-gated 'default integration slot' in Lovable/Vercel/Replit, and do NOT treat MCP-directory listings (Smithery/Glama/PulseMCP/mcp.so) as a growth channel, from zero users. Both violate the permissionless principle and/or are dead weight. Use ONLY the self-serve rails (Vercel connectable-account, custom MCP connectors, Claude Code skill, wizard).**
  - proof: vibe_platforms + agent_native cases: Lovable's Integration Partners Program is a 4-step application form, Vercel native listing requires emailing integrations@vercel.com, Replit connectors are curated first-party with no public submission — all unreachable pre-traction and email-gated. Separately, the audit of 1,847 listed MCP servers found 52% dead and the directories have 'no demand of their own' — being listed ≠ being used.

- **Do NOT build supported self-hosting, and do NOT defer monetization 'for years' to grow the free tier. Keep the paid cloud tier live and self-serve from day one; offer self-host free-and-UNSUPPORTED (one-command Docker only). The open binary is pure top-of-funnel, not a product you operate for people.**
  - proof: PostHog case: it sunset supported Kubernetes/Helm self-hosting because a small team spent 'an outsized amount of time supporting the 3.5% of users' on it — a documented solo-founder trap. And PostHog could defer revenue only because it raised $3M seed + $9M Series A; the corpus explicitly warns a founder at ~₹3,000 with no runway 'CANNOT copy the don't-monetize-for-five-years posture' — steal the free-binary/paid-cloud shape, not the timeline.

## 30-day launch sequence

1. Days 1-3 — Make the product ready to RECEIVE agents. Ship /install.md (OBJECTIVE/DONE-WHEN/checkboxes), llms.txt, and AGENTS.md so the whole flow (create_api_key → propose_instrumentation → verify_instrumentation) is agent-completable from one pasted URL. Rewrite the homepage hero to the 3 differentiators (provably-correct numbers, free BYO-model AI, one-paste agent install) with an explicit no-card free-trial CTA and 'paste this into Cursor/Lovable/Bolt' front and center.
2. Days 3-6 — Build and ship `npx @smolanalytics/wizard` (auto-detect stack, propose starter events, install SDK, verify) plus the hosted remote MCP with one-click OAuth and 'Add to Cursor / Add to VS Code' deep-link buttons on the quickstart. This is the become-default mechanism; nothing else matters if paste-to-working-analytics isn't smooth.
3. Days 6-9 — Stand up the permissionless rails: the Vercel connectable-account OAuth integration (Community badge, self-lists, works in v0), a documented custom-MCP connector for Lovable/Base44/Cursor, and a Claude Code skill pushed to a GitHub marketplace repo listed on claudemarketplaces.com. Also flip smol's own dashboard public as the live demo and add the default 'powered by smolanalytics' badge to create_share_link.
4. Days 8-12 — Publish the SEO layer: four 'How to add analytics to Lovable / Bolt / Replit / v0' attack pages and four 'vs PostHog / Mixpanel / Plausible / GA4' comparison pages, each H1 = the buyer's search phrase, each with 3 checkable differentiators. These start compounding immediately and are ready to catch launch-day search spillover.
5. Days 12-13 — Write the ONE opinionated Show HN post ('Your analytics AI is lying to you, and your vibe-coded app has no analytics at all'), agent-native wedge, product buried in the final paragraph, in Arjun's voice (lowercase, no em-dashes). Dry-run the paste-to-install demo end-to-end so every HN visitor can go from URL to live events in under 5 minutes.
6. Day 14 — Coordinated launch: Show HN (no YC tag) + Product Hunt + r/selfhosted + r/webdev + r/SideProject the same morning, plus genuinely-helpful (non-spam) drops in Lovable/Bolt/Replit builder Discords answering 'how do I see what users do in my app.' Set broad GitHub topics (analytics, product-analytics, self-hosted, mixpanel-alternative). Expectation: ~50-200 signups, not thousands.
7. Days 14-16 — Convert attention while it's hot: reply to every HN/PH/Reddit comment within ~5 minutes (PostHog's 5-min-callback data), watch wizard completions and create_api_key-from-agent counts, and hotfix the biggest install drop-off you see between install-started and verify_instrumentation-healthy.
8. Days 16-25 — Sustain: publish the first contrarian build-in-public follow-up ('we measured how often analytics AIs give 3 different answers to 1 question' using smol's own determinism), submit a smolanalytics rule to cursor.directory + awesome-cursorrules, and seed a memorable 'ask smolanalytics' convention into AGENTS.md starter snippets.
9. Days 25-30 — Wire the self-serve 25% lifetime-recurring affiliate program via Dodo (3-hour job) so the first happy cohort can start referring, and review the 30-day leading indicators (agent-origin API keys, wizard completions, MCP OAuth installs, Search Console impressions on the platform/vs pages) to decide which single mechanic to double down on next month.