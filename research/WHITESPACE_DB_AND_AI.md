# Whitespace deep-dive: DB state branching for tests + testing non-deterministic AI apps

Date: 2026-08-24. Method: primary-source fetches (docs, API refs, pricing pages, GitHub API, npm API)
plus targeted search. Every fact is labelled **MEASURED** (fetched/ran it today), **CLAIMED** (their own
docs/marketing), or **INFERRED** (my read). Anything I could not verify says so.
Builds on `/Users/arjun/smolanalytics/AUTONOMA_TEARDOWN.md` — not re-derived here.

Verdicts up front, because the market analysis needed correcting on both:

- **Frontier 1 (DB branching) is real whitespace but it is a FEATURE, not a product.** No testing
  tool orchestrates branches (verified below, including Autonoma's own blog admitting the customer
  assembles it). The honest build is Neon-only, ~1-2 days, and the durable piece is not the branch —
  it is putting the branch's LSN into our run evidence, making us the only runner whose replay can
  pin the *database state* the proof was recorded against.
- **Frontier 2's premise as handed to us is FALSE.** "Anyone browser-level?" — yes: Momentic ships
  browser-level AI assertions for exactly this ($19.2M raised, Notion/Webflow/Retool logos), and mabl
  has had GenAI Assertions in GA since 2024. The actual open seam is the *opposite* mechanism:
  restoring determinism (record the model at the network boundary, replay the UI exactly) instead of
  making the assertion probabilistic. That extends our proof-carrying model cleanly; an LLM judge
  does not, and must never emit `passed`.

---

## FRONTIER 1 — DB state branching for tests

### 1.1 Verified provider facts

**Neon** (owned by Databricks — acquired ~$1B, May 2025; TechCrunch/CNBC, seen 2026-08-24)

- Mechanism, CLAIMED (neon.com/docs/introduction/branching, seen 2026-08-24): copy-on-write;
  "writes saved as deltas so parent branches see zero load"; "by default, branches are created with
  all of the data that existed in the parent branch", at current time or an earlier point.
- Speed, CLAIMED: docs say "instantly"; their FAQ page and multiple Neon posts say **under one
  second regardless of database size** — branch creation is a metadata operation (new branch points
  at existing storage pages) (neon.com/faqs/managed-postgres-providers-instant-database-provisioning-api,
  neon.com/blog/how-to-copy-large-postgres-databases-in-seconds, seen 2026-08-24). **We did not
  measure this ourselves** (no Neon account in this session) — treat "<1s" as CLAIMED; realistic
  end-to-end including compute endpoint provisioning is INFERRED single-digit seconds, plus
  cold-start on first query if the endpoint scales to zero.
- API, CLAIMED from the API reference (api-docs.neon.tech/reference/createprojectbranch, seen
  2026-08-24): `POST /projects/{project_id}/branches`, Bearer API key. One call can carry:
  - `branch.parent_id`, `branch.parent_lsn` / `branch.parent_timestamp` (point-in-time branch),
  - `branch.init_source`: `"parent-data"` (default) | `"parent-schema"` | `"schema-only"`,
  - `branch.expires_at` (RFC 3339) — **TTL auto-delete**, so failed CI runs clean themselves up
    (`ttl_interval_seconds` read-only; feature labelled early access in the API ref; has its own
    docs page neon.com/docs/guides/branch-expiration, seen 2026-08-24),
  - an `endpoints` array (read_write compute provisioned in the same call),
  - response includes `connection_uris` with ready connection strings.
  Rate limits: not documented — unverified.
- **Neon Local**, CLAIMED (neon.com/docs/local/neon-local, seen 2026-08-24): a Docker proxy that
  "automatically creates a new ephemeral branch of your database when the container starts, and
  deletes it when the container stops" (pass `PARENT_BRANCH_ID`), needs only API key + project ID,
  app connects to `postgres://neon:npg@localhost:5432/...`. Caveat: the serverless-driver path is
  HTTP only, no websockets. **This is 80% of the feature, already shipped by the vendor.**
- Cost, CLAIMED (neon.com/docs/introduction/plans, seen 2026-08-24): Free = 10 branches/project,
  100 CU-hrs/mo, 0.5GB. Launch = 10 branches included, extra **$1.50/branch-month prorated hourly**
  (≈ $0.002/branch-hour — a 10-minute CI branch is fractions of a cent), cap 5,000 branches/project,
  compute $0.106/CU-hr; Scale = 25 included, $0.222/CU-hr; storage $0.35/GB-mo, branches share
  unchanged pages so a fresh branch adds ~zero storage.

**Supabase** ($10.5B valuation, Jun 2026, CNBC, seen 2026-08-24)

- A branch is **a separate Supabase project-like instance with its own API credentials**, and
  **"New branches do not start with any data from your main project"** — explicitly framed as
  protecting production data; seeding is via seed files (supabase.com/docs/guides/deployment/branching,
  seen 2026-08-24). CLAIMED.
- Branching 2.0 (blog, 2025-07-16, seen 2026-08-24): removed the Git requirement — branches via
  dashboard, CLI, or **Management API**; "a copy of your Supabase project, minus the data";
  branches carry Postgres + edge functions + configuration.
- Cost, CLAIMED (supabase.com/docs/guides/platform/manage-your-usage/branching via search results +
  Xata's comparison, both seen 2026-08-24): **$0.01344/hour** on default Micro compute, no fixed
  fee, and compute credits do NOT apply to branch compute (~$9.68/mo left running).
- No copy-on-write, no production-data clone, no point-in-time branch. Creation time: not
  documented; community reports say minutes (unverified).

**PlanetScale Postgres** (planetscale.com/docs/postgres/branching, seen 2026-08-24)

- "There are two ways to create a new Postgres branch: from the Branches page (no schema or data
  included) or by restoring from a backup (schema and data included)." CLAIMED. **No CoW**; a data
  branch is a physical restore — slower and storage-billed per branch ("You will be charged for the
  storage consumed by all production and development branches"). Dev branches run `PS-DEV` from
  **$5/month**. And: "We do not recommend using production data for development environments."
  Creation time undocumented; INFERRED minutes for any real dataset (it is a restore).

**Prisma Postgres**

- **No CoW branching.** Its substitute is many cheap separate databases (50 free / 1,000 paid,
  from search results incl. a Jul 2026 note, seen 2026-08-24; unverified exact quotas). "Prisma
  Compute" public beta mentions "database branches" but that is env-wiring per compute branch, not
  storage-level CoW (prisma.io blog/roadmap, seen 2026-08-24). CLAIMED/INFERRED.

**Xata** (xata.io/docs/core-concepts/branching, seen 2026-08-24) — smaller player, included because
their mechanism is the most explicitly documented CoW: "Xata uses Copy-on-Write (CoW) at the storage
layer to create branches instantly … completes in seconds even for terabyte-scale databases";
per-branch compute billed hourly with scale-to-zero; notably honest on PII: "If the parent contains
sensitive production data, so does the child … consider branching from an anonymized staging
replica." All CLAIMED.

### 1.2 Who already does this in TESTING tools — the hard search

- **No E2E testing product creates/manages DB branches or snapshots for the customer.** Searched
  QA Wolf, Momentic, Checksum, Octomind, mabl, Autonoma docs/blogs + generic queries; zero hits for
  branch orchestration as a product feature. This is absence-of-evidence after a genuine look, not
  proof — but the strongest datapoint is affirmative:
- **Autonoma's own blog** (getautonoma.com/blog/database-branching, Mar 2026, seen 2026-08-24):
  "Once your database branching is in place, Autonoma's agents read your codebase and generate E2E
  tests that run against your branched database in CI." They recommend Neon, quote "<1 second", and
  ship nothing. Consistent with the teardown's "any environment/seeding layer" refusal — they made
  it the customer's problem and kept the SEO page.
- Adjacent prior art (none of it a test *runner*):
  - **Tonic Ephemeral** (docs.tonic.ai/ephemeral + blog, launched Nov 2024, seen 2026-08-24):
    ephemeral test DBs via CoW "data virtualization", user snapshots, auto-expiration, GitHub
    Action + API. Enterprise test-data-management, contact-pricing, pairs with their masking
    product. Closest thing to "snapshots as a service" — but it provisions databases, it does not
    run tests.
  - **Database Lab Engine** (postgres-ai) — self-hosted ZFS thin clones/branching; **2,689 stars,
    active (pushed 2026-08-07)** — MEASURED via GitHub API today.
  - **integresql** — template-DB pooling for integration tests; 806 stars, **dormant since
    2024-01-30** (MEASURED).
  - **Neon's own CI actions** — create-branch-action **56 stars**, delete **9**, reset **3**
    (MEASURED today). The vendor path exists and adoption is visibly small.
  - **Seedfast** (seedfa.st) — a seeding product positioning around Neon branch workflows; signal
    that the branch-plus-data workflow has enough pain to host a startup, size unknown.

### 1.3 Sizing, honestly

There is no "DB branching for tests" market to size; it is a capability inside CI/testing spend.
What is sizeable: the primitive is now owned by a $1B-exit company (Neon/Databricks) and a $10.5B
company (Supabase) and both push it hard, so the concept needs zero evangelism from us. But the
runner-side glue the customer currently writes is ~30 lines of GitHub Actions YAML, and Neon's
56-star action shows how many people bother. INFERRED: this converts as a differentiator and demo
("your suite just ran against a disposable copy of prod-shaped data") rather than as a purchase
driver on its own.

### 1.4 Smallest honest version — build spec

Scope v1 to **Neon only, CI-run apps only**. Fits the zero-dep CLI: two HTTPS calls, no SDK.

```
smolanalytics.config: { db: { provider: "neon", projectId, parentBranch?, apiKeyEnv: "NEON_API_KEY" } }

suite start:
  POST /projects/{id}/branches
    { branch: { name: "smol-ci-<pr>-<sha>", expires_at: now+2h, init_source: "parent-data" },
      endpoints: [{ type: "read_write" }] }
  -> connection_uris[0]
  export DATABASE_URL to the app process we boot (we already own the boot in CI)
  run pending migrations if a migrate command is configured (the PR's schema must exist on the branch)
run suite (unchanged: record/replay, five statuses, retries, teardown safety)
suite end:
  DELETE branch (expires_at is the backstop for crashed runs — the cleanup Neon built expiration for)
evidence:
  record { branch_id, parent_lsn } in the run record
```

The last line is the differentiator, not the branch. `parent_lsn` in the evidence means a replay can
be cut against **the same database state the proof was recorded at** (`parent_lsn` is a documented
create-branch param). Proof-carrying replay currently pins the steps and the page text; this pins the
data. No runner anywhere makes that claim — Autonoma's own README admits 48% of classifications have
no recording at all.

Where it breaks — enumerate in docs, do not paper over:

1. **Connection strings**: only works when the runner boots the app (CI). A deployed Vercel preview
   needs env injection + redeploy — that is Neon×Vercel's integration, not ours. Punt explicitly.
   Pooled vs direct hosts differ per endpoint; use the `connection_uris` the create call returns,
   never string-munge.
2. **Migrations**: branch = parent data incl. parent schema; the PR's new migrations must run
   against the branch before the suite or every test of the new feature fails honestly-but-uselessly.
3. **Supabase is an app platform, not a database**: the app talks to a project URL + keys, branches
   have **no data**, plus separate edge functions/storage/auth (auth users live in the DB, so a
   data-less branch = nobody can log in). "Point the app at the branch" is a false promise there.
   v1: refuse politely, print why. Possibly forever.
4. **Non-Postgres state**: Redis, S3, Stripe test mode, webhooks, background workers still pointed
   at the parent DB. The branch isolates one store; say so.
5. **Cold start**: scale-to-zero endpoints add first-query latency (INFERRED seconds); pre-warm with
   one `SELECT 1` after create.
6. **Whose bill**: branch compute/storage lands on the customer's Neon account ($0.106/CU-hr Launch;
   a 10-min suite on 0.25CU ≈ $0.004). Cheap, but print the cost line in output — their bill, our
   blame otherwise.

### 1.5 Why this might be a trap

- **The vendor already shipped it.** Neon Local creates-and-deletes an ephemeral branch on container
  start/stop today. Databricks-owned Neon can ship "test mode" any quarter and our layer is ~200
  lines over their public API. There is no moat in the branch; the only durable part is the
  LSN-in-evidence claim, which requires our replay model to matter to the buyer.
- **Our ICP sits on the wrong provider.** Vibe-coder tier (Bolt/Lovable/Replit adjacents) defaults
  to Supabase — the one platform where the model breaks (no data in branches, app-platform coupling).
  Neon-first means building for the minority of our own funnel. (INFERRED from ICP memory + provider
  positioning; not measured.)
- **Autonoma looked at this and chose SEO over product.** Given their appetite for shipping plumbing
  (660KB of env/seeding code they DID ship), declining this one is a datapoint that the demand is
  content-shaped. The counter: a blog post *with a working `--db neon` flag* beats their post in its
  own SERP, and costs two days.
- **Kill criterion**: if `--db neon` is used by <5% of active suites after 60 days, freeze it at v1
  and keep the SEO page.

---

## FRONTIER 2 — Testing non-deterministic AI apps end-to-end

### 2.1 Verified facts — who is doing what

**Browser-level semantic assertion already exists. Two funded products ship it.**

- **Momentic** (momentic.ai/docs/steps/ai-check, seen 2026-08-24): AI assertions are evaluated
  "against the DOM, accessibility tree, and a screenshot of the viewport"; guidance: under 20 words,
  answer "clearly true or false given the page state"; run-level assertions judged by "an AI agent
  that samples frames from the run's video recording". Marketing (via search, seen 2026-08-24):
  for LLM features "the AI can validate that the response is 'relevant' or 'contains a
  recommendation' without expecting exact text." **No determinism/caching guarantees documented.**
  Funding MEASURED-from-press: $15M Series A Nov 2025 (Standard Capital, Dropbox Ventures), $19.2M
  total, customers incl. Notion, Xero, Webflow, Retool, "2,600 users"; revenue undisclosed
  (TechCrunch 2025-11-24, seen 2026-08-24).
- **mabl GenAI Assertions** (help.mabl.com, early access 2024-06-10, seen 2026-08-24): natural-
  language prompt, multimodal LLM "acts as the judge of that state", pitched for "AI-powered
  features, text translations, and chatbots". Their own caveat, verbatim: "Since generative AI is
  non-deterministic in nature, the results of a GenAI Assertion can vary." The vendor admits the
  assertion itself flakes.
- **Autonoma**: runtime is agentic (inherently semantic — the 7-verdict adjudicator IS an LLM
  judge over runs; see teardown). Their content play covers this frontier too:
  getautonoma.com/blog/how-to-test-non-deterministic-ai-outputs (seen 2026-08-24) is **guidance,
  not product**: invariant assertions; embedding similarity with a calibrated threshold ("Picking
  0.8 because it sounds reasonable is a guess, not a measurement", example 0.82); n-run sampling
  (5-20 runs, example 60% pass floor); suite-level threshold gate "set from a measured baseline".
  Product framing = behavioral side-effects: "checks what an AI feature actually caused in a
  running app (the right screen, the right record, the right side effect)."
- Micro-tools, MEASURED via GitHub API 2026-08-24: `llm-assert/llm-assert` (Playwright matchers for
  LLM outputs) **0 stars, abandoned Apr 2026**; `agorischek/semantic-expect` 5 stars. Browser-level
  semantic assertion as OSS: nobody cares yet.

**API-level (not browser) eval stack is crowded and well-funded — do not fight it:**

- **Braintrust**: $36M Series A led by a16z at ~$150M (Forbes/Techmeme, Oct 2024, seen 2026-08-24),
  Databricks + Datadog investors; eval/scorer platform; per comparison content, weak on multi-turn.
- **LangSmith** (LangChain): trace-first evals/observability; framework-agnostic. (LangChain widely
  reported at ~$1.25B valuation Oct 2025 — unverified here, no primary source fetched.)
- **DeepEval** 17,819 stars; **LangWatch Scenario** 955 stars, active (MEASURED 2026-08-24):
  simulated-user + judge-agent testing with criteria strings ("Agent should not ask more than two
  follow-up questions"), multi-turn — but it "calls agent functions directly … receiving structured
  message input", **not a browser** (README, seen 2026-08-24).
- **VCR-for-LLMs is established DIY practice, not a product**: vcrpy/pytest-recording cassettes for
  OpenAI/Anthropic; `sixty-north/langchain-replay` records the LLM's *decisions* and replays them
  while executing real tools (blog + repo, seen 2026-08-24). Nobody has productized this at the
  browser/E2E layer.

**"Semantic assertion" concretely** — across every source it is one of exactly four mechanisms:
(1) natural-language claim over page state, judged true/false by a multimodal LLM (Momentic, mabl);
(2) structural invariants (format, must-contain/must-not-contain, parses-as-JSON, length, latency);
(3) embedding similarity vs an empirically calibrated threshold;
(4) rubric LLM-judge + n-run pass rate against a floor.
Practitioner caveat that recurs: validate the judge against humans first; 85-90% agreement is the
commonly cited bar (Medium/Cresta pieces, seen 2026-08-24 — INFERRED consensus, not a standard).

### 2.2 Does proof-carrying replay extend? Yes — but not by making the proof fuzzy

Our current contract (test.mjs, read today): a pass requires `proof` — "a short, distinctive run of
text that was visible on the page"; replay is `text.includes(plan.proof)` with **no model**; a
missing proof = outcome-changed → stale → hand back to the agent. On an AI chat page this proof
breaks *every* run by design. Three tiers replace the single proof string; each keeps the honesty
rules:

**Tier 1 — FREEZE (default): make the input deterministic, keep the proof exact.**
Record at the LLM network boundary during the agent (recording) run: intercept requests to
api.openai.com / api.anthropic.com / a customer-declared route, store request-hash → response
(including SSE chunk sequence) as a cassette in the evidence dir. Replay serves the cassette; the
UI becomes deterministic; the exact-text proof works again, unchanged. This tests the app —
streaming render, error states, retries, tool-call plumbing — not the model, and says so out loud.
Prior art is DIY libraries only; **no E2E product ships this**. It preserves everything we market:
five statuses, no-model replay, zero probabilistic assertions.

**Tier 2 — SHAPE: proof becomes a predicate list, still zero model calls at replay.**
`proof: [{assistant_message_appeared}, {min_length: 40}, {no_selector: ".error-toast"},
{contains_any: [...]}, {json_parses: false}, {within_ms: 15000}]` — deterministic invariants that
hold across acceptable outputs. This is Autonoma's "invariant assertions" advice made first-class
in a recorder instead of left as reader homework.

**Tier 3 — JUDGE (opt-in, loudly labelled): rubric + pinned judge + n runs.**
`proof: { rubric: "reply recommends a vegetarian recipe", judge: {model, version-pinned}, runs: 3,
pass_floor: 2/3 }`. Judge I/O logged as evidence. **A judge verdict must never emit `passed`** —
new status `judged` (rank between flaky and passed), same design logic as our flaky-is-not-passed
rule and our exit-code contract. Mabl's own docs admitting GenAI assertions "can vary" is the
competitor-written justification for this status existing.

The one-sentence differentiation, which is real because the mechanism above is real: **everyone
else made the assertion probabilistic; we make the input deterministic and quarantine the judge.**

### 2.3 Sizing

MEASURED today via npm registry API: `ai` (Vercel AI SDK) **22.4M downloads/week**, `openai`
**34.5M/wk**, `@anthropic-ai/sdk` **33.9M/wk**. Essentially every app our ICP ships now has an LLM
boundary — Tier 1 has a boundary to record in ~every new project. Willingness-to-pay exists at the
eval layer (Braintrust $150M valuation on evals alone) and at the E2E layer (Momentic $19.2M);
nobody prices the seam between them.

### 2.4 Why this might be a trap

- **Freeze tier tests the plumbing, not the intelligence.** Buyers who *feel* the pain of "is the
  model good" belong to Braintrust/LangSmith, who have the war chest and the trace data. If we ever
  market Tier 1 as "test your AI", we invite that comparison and lose it. The honest pitch is
  narrower: "your chat UI stops flaking in CI."
- **Cassette brittleness is real work, not a weekend**: any prompt edit is a cache miss (prompt
  changes on most AI-feature PRs → frequent re-record); temperature>0 means each re-record produces
  a different fixture (churny diffs); SSE replay fidelity (chunk timing) decides whether streaming
  UI tests are honest; provider SDK/transport changes (websockets, batching) break interception
  quietly. Budget: this is the whole feature.
- **Judge tier imports the exact flake we market against**, plus a calibration burden Autonoma's own
  guidance says must be *measured* per domain — that is a service, and we sell a $19/mo product.
  Shipping it silently-default would make us Momentic with worse funding.
- **Momentic owns the wording already** ("handles non-deterministic outputs") with $19.2M and
  Notion/Retool logos. A me-too semantic assertion from us is an echo; only the determinism-restored
  angle is unoccupied.
- **Kill criterion**: if Tier 1 cassettes require >1 re-record per PR in our own dogfood app, the
  DX is net-negative; stop before marketing it.

---

## Cross-cutting call

Both frontiers are the same product sentence: **the state a test ran against becomes part of the
proof.** Frontier 1 pins the database (branch @ `parent_lsn` in evidence); Frontier 2 pins the model
(cassette in evidence). Combined claim — "this run is reproducible: same steps, same data, same
model output" — is one no competitor can echo without rearchitecting: Momentic/mabl chose
probabilistic assertions, Autonoma has no deterministic replay and 48% of its classifications have
no recording (their README, per teardown).

Build order:
1. **Tier 1 LLM cassette** (Frontier 2) — no provider dependency, boundary present in ~every ICP
   app (MEASURED npm numbers), pure extension of the existing record/replay code path.
2. **Tier 2 shape proofs** — small, same release.
3. **Neon `--db neon` + LSN-in-evidence** (Frontier 1) — 1-2 days, ship with the SEO page pair
   ("database branching for E2E tests" + "test your app against production-shaped data"), which
   Autonoma has already validated as a content surface and left unbacked by product.
4. **Do not build**: Supabase branching v1 (data-less branches break the promise), judge tier until
   users ask twice, any embedding-threshold feature (calibration is a service).

## Source register (all seen 2026-08-24)

- neon.com/docs/introduction/branching · /docs/introduction/plans · /docs/local/neon-local ·
  /docs/guides/branch-expiration · api-docs.neon.tech/reference/createprojectbranch ·
  neon.com/faqs/managed-postgres-providers-instant-database-provisioning-api
- supabase.com/docs/guides/deployment/branching · supabase.com/blog/branching-2-0 (2025-07-16) ·
  usage docs + Xata comparison for $0.01344/hr
- planetscale.com/docs/postgres/branching · prisma.io/blog (Compute beta, roadmap)
- xata.io/docs/core-concepts/branching · xata.io/blog/neon-vs-supabase-vs-xata-postgres-branching-part-2
  (pricing figures; Xata-authored, bias noted)
- getautonoma.com/blog/database-branching (Mar 2026) · /blog/how-to-test-non-deterministic-ai-outputs
- momentic.ai/docs/steps/ai-check · techcrunch.com/2025/11/24 (Momentic $15M A)
- help.mabl.com GenAI Assertions articles (EA 2024-06-10)
- github.com API (stars/pushed_at MEASURED): postgres-ai/database-lab-engine 2689 ·
  allaboutapps/integresql 806 (dormant) · neondatabase/create-branch-action 56 ·
  langwatch/scenario 955 · confident-ai/deepeval 17819 · antiwork/shortest 5666 ·
  llm-assert/llm-assert 0 · agorischek/semantic-expect 5
- api.npmjs.org (MEASURED): ai 22,395,585/wk · openai 34,510,842/wk · @anthropic-ai/sdk 33,909,665/wk
- docs.tonic.ai/ephemeral + tonic.ai blog (Ephemeral launch Nov 2024)
- forbes.com / techmeme.com (Braintrust $36M @ ~$150M, Oct 2024) ·
  techcrunch.com/2025/05/14 (Databricks–Neon ~$1B) · cnbc.com 2026-06-04 (Supabase $10.5B)
- Local ground truth: /Users/arjun/smolanalytics/cli/lib/test.mjs + suite.mjs (proof contract,
  five statuses, exit codes) · /Users/arjun/smolanalytics/AUTONOMA_TEARDOWN.md
