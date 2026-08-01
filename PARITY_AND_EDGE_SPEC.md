# Mobile parity + web differentiators — build spec (2026-08-01)

Two tracks Arjun asked for: (1) mobile as good as web, (2) web features the incumbents
(PostHog, Mixpanel, Plausible) don't have, aimed at solo/agent-native builders.
Grounded in a code audit: the engine is currently MOBILE-BLIND — no $screen_view
handling anywhere in internal/; mobile events flow as generic events. The four SDKs
(Swift/Kotlin/RN/Flutter) are published; their emitted event names + properties are
the FIRST thing to verify (repos: check gh repo list Arjun0606 for the sdk repos)
before building reports against them.

## Track 1 — mobile parity (ranked)

M1. **mobile_overview report** (engine). The web_overview equivalent keyed on screen
    views: top screens (screen prop), app versions, OS versions, device models, live
    now, per-version visitors. Same shape discipline: /v1/mobile + MCP tool +
    dashboard pane, agreement-locked. PREREQ: confirm SDK event names ($screen_view
    vs screen_view) and props (app_version, os_version, model). If SDKs don't send
    these yet, SDK work comes first (4 repos).
M2. **Engaged time on mobile** = foreground seconds. SDK-side: send $engagement with
    engaged_ms on background/close. Engine already aggregates $engagement — zero
    engine work once SDKs send it.
M3. **Crash/error capture**: $error events from SDKs (uncaught handler). Engine gets
    an errors-by-version report; the VERDICT learns "crash rate jumped after 1.2.3".
    This is the mobile feature devs actually switch for (their alternative: Firebase).
M4. **Version adoption + version deploy-impact**: app_version is mobile's deploy
    marker. Wire versions into the existing deploys/impact primitive → "did 1.2.3
    tank retention" for free, and the PR-comment loop works for app repos.
M5. Docs + /for/ios //for/android pages advertising all of it (shipped-means-
    reachable rule: build M1-M4 before the copy).

## Track 2 — web differentiators (ranked by love-per-effort)

W1. **"Explain this change"** — click any spike/dip (or ask over MCP): the engine
    decomposes the delta between the two windows by property contribution ("signups
    +40: +32 from source=hn, +6 mobile, rest noise"). Computed, deterministic,
    agreement-locked. Mixpanel gates root-cause behind enterprise; Plausible doesn't
    have it; ours is one click and free. Engine: contribution analysis over two
    windows per property — a focused new report (explain_change).
W2. **Feature graveyard** — tracked events whose volume decayed to ~zero + features
    with <N users in 30d. "These 3 features earned 4 users this month — delete the
    code." Solo builders LOVE deleting code; no incumbent frames analytics as
    subtraction. Cheap: event-volume trend analysis over existing data.
W3. **Verdict-driven fix PRs everywhere** (extend the existing fix-PR runner): the
    weekly "biggest drop-off" finding links "open a PR that fixes the funnel copy" —
    the loop nobody else can close (they don't hold the repo).
W4. **$ per channel** — Dodo/Stripe webhook → revenue events → revenue by
    source/campaign on the same single query path. Big pull for indie SaaS;
    medium build (webhook ingestion + a money-typed report).
W5. **Public open-startup dashboards** — share links already exist; add a designed
    public mode (the /open page angle). Growth loop + differentiator vs Mixpanel
    (Plausible has public dashboards; ours add the verdict).

## Sequencing recommendation

Week 1: W1 + W2 (pure engine, no SDK coordination, immediately demo-able to the
outreach list). Week 2: M1 + M2 (verify SDK payloads first). Week 3: M3 + M4 (the
mobile switcher features). W3-W5 as the funnel demands.

Non-negotiables carried from the codebase: every new report ships on all three
surfaces (HTTP /v1, MCP tool, dashboard pane) with agreement-test locks; low-sample
honesty (n<5 annotations); no vapor copy before the feature is reachable.
