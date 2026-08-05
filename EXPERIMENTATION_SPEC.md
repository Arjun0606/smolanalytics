# smolanalytics — Experimentation & Determinism: ranked build spec

**Scope:** make A/B testing and the determinism story in `/Users/arjun/smolanalytics` best-in-class.
**Ranking:** trust impact × feasibility for one implementer with an AI agent. Tier 1 items are days; Tier 3 are the week-long structural ones.
**Ground rule used throughout:** everything must stay pure (`[]event.Event → report`), MCP==API pinnable, and honest when it can't compute.

---

## 0. What is already correct — do NOT rebuild

Read this first. Several items below build *on top of* these; none of them replace these.

| Thing | Where | Verdict |
|---|---|---|
| Bucketing hash | `internal/flag/flag.go:127-133` — `sha256(salt+":"+id)`, top 60 bits `>>4`, `/2^60` | **Correct and better than most incumbents.** The FNV-1a defect is documented in-code and fixed. Do not touch the hash function. Only *version* it (item 8). |
| Per-purpose salts | `"rollout:"+key` (`flag.go:66`), `"variant:"+key` (`flag.go:98`) | Correct. Rollout and variant buckets are independent. |
| Fixed [0,1) variant space | `flag.go:98-110` — cumulative weight ranges, not `hash % sum(weights)` | Correct. Editing weights mid-experiment only moves a boundary. Pinned by `bucketing_test.go:20` (`TestChangingWeightsDoesNotReshuffleExistingUsers`) using a **sum-changing** 1:1→2:1 edit, which is the only shape that exposes the bug. Keep that test exactly as is. |
| Device-scoped bucket key across login | `internal/api/flags_api.go:81-94` + `sdk.js:74-95` (`smol_bucket_id`, never rewritten by `identify()`/`reset()`) | **Correct.** Pinned by `internal/api/bucket_stability_test.go:63-150` including a control test proving the old bug reproduces at ~50%. This is *sticky bucketing done properly* for web. Item 10 only extends it, does not redo it. |
| Exposure attribution | `measure.go:106` — conversion counts only at/after the user's **first** exposure (`!g.Before(ex.at)`) | Correct. Pinned at `measure_test.go:68`. |
| Wilson interval | `interval.go:34-50` | Correct. Never leaves [0,1]. Keep. |
| Relative-lift log-ratio interval + near-zero-control guard | `interval.go:84-108`, guard `p2 < 10*seCtrl` | Correct, and the guard is the thing that stops "+4,000% lift". Keep. |
| Two-proportion z + exact `erfc` p-value | `measure.go:170-182`, `interval.go:58-72` | Arithmetic is right and internally consistent (`interval_test.go:60-84`). The *inference regime* is wrong (item 4), not the arithmetic. |
| SRM engine | `internal/flag/srm.go:56-120` | **Excellent.** α=0.001, ≥5-expected validity floor, counts only declared arms, uses earliest exposure, hand-rolled regularized incomplete gamma validated against 15 published criticals (`srm_test.go:17-48`). Do not rewrite. It just isn't *reachable* (item 1). |
| SRM culprit attribution | `srm.go:178-245`, ≥30 users/segment, per-segment χ² > 10.83, sorted tie-break | Correct and deterministic. Keep. |
| Flag store persistence | `store.go:67-110` — validation, `Created` preserved, memory rolled back on persist failure | Correct. |
| Auth split | `flags_api.go:66-71` — evaluate is write-key + CORS and returns **only** resolved `key→variant`, never rules | Correct. Targeting logic cannot leak to the browser. |
| Scope parity | `flags_api.go:137-141` uses `query.Apply(evs,nil)` to match MCP `applyDefaultScope`; `experiment_health` deliberately **skips** scoping (`mcp/flags.go:221-223`) | Both choices are right and the reasoning in the comments is right. |

---

# Tier 1 — ship first (huge trust, S effort, engine already exists)

---

## 1. Make SRM reachable: HTTP endpoint + dashboard banner + ask bar

**Why it matters:** `flag.CheckSRM` is the single most valuable thing in the package and it is callable from exactly one place — `internal/mcp/flags.go:224`. There is no route in `api.go:373-378`. The dashboard cannot show it, the ask bar cannot cite it, and no agreement test can pin it. Meanwhile `measure.go:163` prints a Note *telling the reader to check it* on a surface where checking is impossible. That is worse than not having the check.

**What to build**

1. `GET /v1/flags/{key}/health?days=30` → `flag.SRMResult`, read-key authed, **not** default-scoped (identical to MCP).
2. Dashboard: run health **before** the A/B table in `flMeasureBody`. If `detected`, render a red banner *above* the table and grey out the lift column entirely.
3. Ask bar: `tkFlagImpactAnswer` (`ask_toolkit.go:425-444`) calls health first and prefixes the answer when it fires.

**Algorithm:** none new. `flag.CheckSRM(evs, f, days)` verbatim.

**Files**
- `internal/api/api.go` — add `mux.HandleFunc("GET /v1/flags/{key}/health", s.flagHealth)` next to line 376.
- `internal/api/flags_api.go` — new `flagHealth` handler; `s.store.Range(zero,zero)` with **no** `query.Apply` (match `mcp/flags.go:221`).
- `internal/api/dashboard.tmpl.html:3760` — insert health fetch/banner in `flMeasureBody`'s `run()`.
- `internal/api/ask_toolkit.go:425-444`.

**Surfaced as:** HTTP `/v1/flags/{key}/health`; dashboard banner in `pane-flags`; ask-bar prefix; existing MCP `experiment_health`.

**Test**
- `internal/api/agreement_test.go` — add case `{"experiment health", "/v1/flags/checkout_v2/health?days=30", "experiment_health", '{"key":"checkout_v2","days":30}', false}`. This is the test that makes the whole item non-regressible.
- `internal/api/flags_test.go` — `TestSRMBannerBlocksLift`: seed a 70/30 split on a 50/50 flag, assert the dashboard HTML/JSON path marks the result unreadable.

**Effort:** S (half a day).

---

## 2. Stop discarding the intervals we already compute

**Why it matters:** `interval.go`'s own doc comment says *"This is the 'computed, not guessed' claim made visible."* The server ships `rate_ci`, `delta_ci`, `p_value` and a hand-written `read` sentence (`measure.go:132-153`). The dashboard at `dashboard.tmpl.html:3775-3782` **throws all four away** and re-derives its own worse sentence. `tkFlagImpactAnswer` does the same. The headline claim never reaches a human.

**What to build**

Render, in the dashboard A/B table:
- rate column → `43 / 512 = 8.4%` with `(6.3–11.1%)` beneath in muted type
- vs-control column → `+18.2%` with `(−2.1% to +31.4%)` beneath
- read column → **the server's `v.read` verbatim**, never a locally re-derived string
- when `delta_ci` is absent, print the server's reason, not "n/a"

Same in `tkFlagImpactAnswer`, and fix the bug at `ask_toolkit.go:439-440` where the control arm's never-set `Significant` makes it always print "not yet significant".

**Files:** `internal/api/dashboard.tmpl.html:3768-3790`; `internal/api/ask_toolkit.go:425-444`.

**Test:** `internal/api/dashboard_findings_test.go` — `TestABTableRendersIntervals`: assert the rendered pane contains the `read` string byte-for-byte from `flag.Measure` and contains both CI bounds. A UI that *silently drops* a field is exactly what a rendered-not-read test catches.

**Effort:** S.

---

## 3. Zero-exposure arms must show as 0, not vanish; control must be declared, not alphabetical

**Why it matters:** two silent-wrongness bugs in one function.

- `Measure` never receives the `Flag`. `byVariant` is built purely from observed exposures (`measure.go:98-109`), so **an arm with zero exposures is absent from the report** — the single most damning failure mode (treatment code never shipped, SDK never evaluated it) renders as "nothing to see".
- Control is `sort.Strings(variants)[0]` (`measure.go:115-120`). Variants named `{control, a_new}` pick `a_new` as control and **invert the sign of every lift**. Not configurable anywhere.

**What to build**

```go
func Measure(evs []event.Event, f Flag, goal string, from, to time.Time) Report
```

- Seed `byVariant` from `f.Variants` so every declared arm appears with `exposed:0, converted:0`.
- Control = `f.Experiment.Control` (item 5); fall back to `f.Variants[0].Key` (declaration order, not alphabetical); fall back to the old alphabetical rule only when `f` is zero-valued.
- If any declared arm has `exposed == 0` while another has ≥30, set `rep.Note` to: *"arm `<key>` has zero exposures — the code path that reads this flag has probably never run. Nothing below is a result."*
- Fix `measure.go:81-94`: `case ExposureEvent:` and `case goal:` share a switch, so a goal literally named `$feature_flag_called` is unreachable. Split into `if e.Name == ExposureEvent { … }` then `if e.Name == goal { … }`.
- **Replace `days int` with absolute `from, to time.Time`.** `measure.go:69` computes `time.Now().UTC().AddDate(0,0,-days)` *inside* the pure function, so the same input returns different output as the clock moves. That single line makes receipts (item 14) impossible. Resolve `days` → absolute instants at the API/MCP boundary and pass them down. Window is half-open `[from, to)` to match `contracts_test.go` WINDOW-1.

**Files:** `internal/flag/measure.go`; callers `internal/api/flags_api.go:141`, `internal/mcp/flags.go:194`, `internal/api/ask_toolkit.go`.

**Surfaced as:** all existing surfaces; new `from`/`to` echoed in the JSON as absolute RFC3339 UTC.

**Test**
- `internal/flag/measure_test.go` — `TestDeadArmIsReportedNotOmitted`, `TestControlIsDeclaredNotAlphabetical` (variants `{control:50, a_new:50}`, assert `Control == "control"` and lift sign is correct), `TestGoalNamedLikeExposureEventStillCounts`.
- `TestMeasureIsClockIndependent`: call twice with a fake clock advanced by 1h, assert byte-identical JSON.

**Effort:** S.

---

## 4. Sequential / always-valid inference, with the mode locked at experiment start

**Why it matters:** `significant()` is a fixed-horizon 1.96 z-test (`measure.go:181`) sitting behind a dashboard built for daily checking. Under continuous peeking the true false-positive rate is 20–25%, not 5%. Every serious platform in 2026 ships sequential — Eppo and Harness make it the *default*. Shipping a naive p-value on a live dashboard is the one thing that makes an experiment feature read as a toy.

**What to build:** `internal/flag/sequential.go` — **asymptotic confidence sequences** (Waudby-Smith/Ramdas 2023, the GrowthBook construction). Chosen over mSPRT because it is nonparametric, closed-form, needs no likelihood assumption, and matches a published OSS implementation so a skeptic can cross-check us against `gbstats`.

**Exact algorithm**

Parameters, stored on the experiment and immutable after start:
- `alpha` (default `0.05`)
- `n_tune` = N\*, "the total number of exposed units at which you expect to decide" (default `5000`)
- `mode` ∈ `{sequential, fixed}` (default `sequential`)

```
ρ = sqrt( ( -2·ln(α) + ln( -2·ln(α) + 1 ) ) / N* )

N = n_treat + n_control                       // total exposed units, both arms

// inflation factor over the fixed-horizon half-width
k(N) = sqrt( N · ( 2·(N·ρ² + 1) / (N²·ρ²) ) · ln( sqrt(N·ρ² + 1) / α ) ) / z_{1-α/2}

half_fixed = z_{1-α/2} · se,  se = sqrt( p_t(1-p_t)/n_t + p_c(1-p_c)/n_c )
half_seq   = k(N) · half_fixed
```

Report the **absolute** difference interval `(p_t − p_c) ± half_seq`, and the relative-lift interval by applying the same `k(N)` inflation to `liftInterval`'s `z` argument (i.e. call `liftInterval(..., z95*k(N))` — the existing log-ratio machinery is reused untouched, which is why this is cheap).

Always-valid p-value: bisect α ∈ [1e-12, 1] for the value at which `half_seq(α)` exactly equals `|p_t − p_c|`. Fixed 60 iterations → deterministic and bit-reproducible. **No Monte Carlo anywhere in a reportable number.**

Invariants to enforce in code: `k(N) > 1` always; `half_seq → 0` as `N → ∞`; `k` is minimized near `N = N*`.

**Anti-peeking UX (the part that makes it a product, not a stat):**
- `mode` and `alpha` and `n_tune` are locked at experiment start (item 5) and hashed into the receipt (item 14).
- If `mode == fixed` and the user opens the result before `n_planned` (item 6) is reached, **render the sequential interval instead**, with: *"you pre-registered a fixed-horizon test at 12,400 users and you're at 3,100. The fixed interval isn't valid yet, so this is the always-valid one — it's wider on purpose."*
- Print the honest cost once, on the experiment page: *"always-valid inference costs you roughly 16% of the power of a fixed-horizon test. That's the price of being allowed to look whenever you want."*

**Files:** new `internal/flag/sequential.go`; `internal/flag/measure.go` (choose `z` by mode); `internal/flag/interval.go` (accept `z` — already parameterised, no change needed); `dashboard.tmpl.html` flag pane; `internal/mcp/flags.go` (`flag_impact` returns `mode`, `alpha`, `n_tune`, `k`).

**Test:** `internal/flag/sequential_test.go`
- `TestSequentialIsAlwaysWiderThanFixed` — sweep N ∈ [100, 1e6], assert `k(N) > 1`.
- `TestSequentialWidthShrinksWithN` — monotone decrease in half-width.
- `TestSequentialWidthMinimizedNearNTune` — `k(N*) < k(N*/10)` and `k(N*) < k(10·N*)`.
- `TestAlwaysValidPValueRoundTrips` — for random (c1,n1,c2,n2), the bisected α reproduces a half-width equal to the observed difference to 1e-9.
- **`TestSequentialControlsErrorUnderPeeking`** (this is the real one): 2,000 simulated A/A experiments, peeked at every 100 users up to 20,000, seeded RNG. Assert empirical rejection rate ≤ 0.06 for `sequential` and assert it **exceeds 0.15** for `fixed` — proving the sequential mode is doing work and the old code was genuinely broken.

**Effort:** M (2 days, mostly the simulation test).

---

## 5. The experiment object: a pre-registered, immutable analysis plan

**Why it matters:** right now the goal event is retyped on every read, the control arm is inferred, α is a constant, there are no start/stop dates, no guardrails, no layer, and no record of who changed the weights mid-flight — which is precisely the cause `srm.go:114`'s own verdict text blames for mismatches. Items 4, 6, 7, 8, 11, 12 and 14 all need somewhere to live. **This is the substrate; build it before them.**

Statsig locks the engine choice at start to prevent cherry-picking. Nobody makes the *whole plan* an immutable pre-registration record. That is a defensible, one-sentence anti-p-hacking claim.

**What to build:** `internal/flag/experiment.go`, persisted inside the existing `.flags.json` (no new store):

```go
type Experiment struct {
    Goal        string    `json:"goal"`                   // primary metric event
    Control     string    `json:"control"`                // declared, not inferred
    Guardrails  []Guardrail `json:"guardrails,omitempty"` // item 7
    Secondary   []string  `json:"secondary,omitempty"`    // excluded from BH family
    Mode        string    `json:"mode"`                   // sequential | fixed
    Alpha       float64   `json:"alpha"`                  // 0.05
    Power       float64   `json:"power"`                  // 0.80
    NTune       int       `json:"n_tune"`                 // 5000
    MDEPct      float64   `json:"mde_pct"`                // relative, from item 6
    NPlanned    int       `json:"n_planned"`              // total exposed units
    CUPED       *CUPEDCfg `json:"cuped,omitempty"`        // item 9
    Layer       string    `json:"layer,omitempty"`        // item 12
    HashVersion int       `json:"hash_version"`           // item 8
    Started     time.Time `json:"started"`
    Stopped     time.Time `json:"stopped,omitempty"`
    PlanHash    string    `json:"plan_hash"`              // sha256 of canonical JSON of the above
    Locked      bool      `json:"locked"`
}
```

**Lock semantics (enforced in `Store.Save`):**
- Once `Started` is non-zero and `Locked`, mutating **any** of `Goal, Control, Alpha, Mode, NTune, MDEPct, NPlanned, CUPED, Layer, HashVersion` or the variant weights returns an error:
  *"checkout_v2 has been running since 2026-07-21 with 4,102 users exposed. Changing the goal now means the result you report was chosen after seeing the data. Stop it and start a new experiment, or explicitly amend (which is recorded)."*
- `POST /v1/flags/{key}/amend` with a `reason` writes an audit entry via `internal/audit` and appends to an `Amendments []Amendment` list that ships **inside the report payload**, so a reader always sees "this plan was changed on day 4".
- `PlanHash` = SHA-256 over `json.Marshal` of the struct with `PlanHash`/`Locked`/`Amendments` zeroed, keys sorted (use a small canonical-encode helper, not `MarshalIndent`).

**Files:** new `internal/flag/experiment.go`; `internal/flag/flag.go` (add `Experiment *Experiment` to `Flag`); `internal/flag/store.go:67-110` (lock enforcement + audit hook); `internal/api/flags_api.go` (`POST /v1/flags/{key}/start`, `/stop`, `/amend`); `internal/mcp/flags.go` (`start_experiment`, `stop_experiment`, `amend_experiment`).

**Surfaced as:** dashboard — an experiment sub-pane per measured flag showing the locked plan and a "started 6 days ago · 4,102 / 12,400 users · est. 9 days left"; MCP tools; `GET /v1/flags` payload.

**Test:** `internal/flag/experiment_test.go`
- `TestLockedPlanRejectsGoalChange`, `TestLockedPlanRejectsWeightChange`, `TestAmendmentIsRecordedAndSurfaced` (assert the amendment appears in `Measure`'s output, not just the audit log).
- `TestPlanHashIsStableAcrossMarshalling` — same plan, different field insertion order, same hash.

**Effort:** M.

---

## 6. Power / MDE / duration calculator, computed from *this instance's own* traffic

**Why it matters:** there is no sample-size, MDE, or duration calculator anywhere. Without one, a user has no idea whether to look today or in three weeks, so they look today — which is exactly what makes item 4 necessary. GrowthBook paywalls this at $40/seat. Better than any of them: don't ask the user to type σ or a baseline rate. **Read it from their events.**

**Exact algorithm** (`internal/flag/power.go`)

Baseline from the instance, over the last 28 days in production scope:
- `p_A` = (users who did `goal`) / (users who did any event) — the same denominator the funnel uses
- `daily_units` = median distinct users/day eligible for the flag's targeting rules

Fixed-horizon, two-sided, unpooled (planning form):

```
n_per_arm = (z_{1-α/2} + z_{1-β})² · [ p_A(1-p_A) + p_B(1-p_B) ] / (p_B - p_A)²
p_B = p_A · (1 + r)                              // r = relative MDE
```

Given `n`, solve for `r` by bisection on `r ∈ (1e-6, 10)`, 60 iterations → deterministic.

Sequential penalty: required `n_seq` = smallest N such that `half_seq(N) ≤ |p_B − p_A|` using item 4's `k(N)`. Report both: *"12,400 users fixed-horizon, 14,900 with always-valid inference — that's the 16% you're paying to be allowed to peek."*

Unequal allocation with `k = n_T/n_C`: `n_C = (1 + 1/k)·(z_{1-α/2}+z_{1-β})²·p̄(1-p̄)/(p_B-p_A)²`. **Show this number live when someone edits the weights** — a 90/10 split costs ~2.8× the total sample of 50/50, and nobody knows that until it's too late.

Duration = `n_total / daily_units`, floored at 14 days with the note *"run at least two business cycles regardless of what the math says — weekday and weekend users are different people."*

Refuse honestly: if the goal's per-user distribution has `|skewness| > 5`, return an error rather than a number — *"revenue-per-user here has skewness 22, so the normal approximation needs roughly 355·skew² ≈ 172,000 users per arm before a t-test is trustworthy. Use a binary conversion goal instead."* (Kohavi's `n ≈ 355·skew²` rule.)

**Files:** new `internal/flag/power.go`; `internal/api/api.go` + `flags_api.go` → `GET /v1/flags/{key}/power?mde_pct=&alpha=&power=`; `internal/mcp/flags.go` → `experiment_power`; `dashboard.tmpl.html` — live in the create-experiment row and as a progress bar on a running experiment.

**Test:** `internal/flag/power_test.go`
- `TestSampleSizeMatchesLehrRule` — at α=0.05, power=0.80, standardized effect Δ, assert `n ≈ 16/Δ²` within 2%.
- `TestMDEBisectionRoundTrips` — `mde(n(r)) == r` to 1e-6.
- `TestUnequalAllocationCosts` — 90/10 requires ≥2.5× the total of 50/50.
- `TestRefusesOnHeavyTail` — synthetic Pareto revenue metric returns an error, not a number.

**Effort:** M.

---

## 7. Guardrail metrics as one-sided non-inferiority tests

**Why it matters:** a guardrail asks *"can I rule out a degradation worse than δ"*, not *"did it change"*. Running a two-sided significance test on a guardrail is backwards — you want that test to be **sensitive**, which is also why guardrails must be excluded from the multiple-comparison family (item 11). No small tool ships guardrails at all.

**What to build**

```go
type Guardrail struct {
    Event    string  `json:"event"`               // e.g. "$exception", "checkout"
    Direction string `json:"direction"`           // "not_worse" (default) | "not_better"
    MarginPct float64 `json:"margin_pct"`         // δ, relative, e.g. 2.0 = "don't lose >2%"
}
```

**Algorithm.** One-sided at level α: compute the **(1−α) one-sided lower bound** on relative lift (reuse `liftInterval` with `z_{1-α}` = 1.6449 for α=0.05, inflated by `k(N)` in sequential mode). Then:

- `lower > -δ` → **PASS** — *"we can rule out a loss bigger than 2%."*
- `upper < -δ` → **FAIL** — *"this arm is worse by more than the 2% you said you'd accept. Do not ship."*
- otherwise → **INCONCLUSIVE** — *"not enough data to rule out a 2% loss yet. This is not a pass."*

The three-state output matters: collapsing INCONCLUSIVE into PASS is how guardrails become decorative.

Ship two guardrails **auto-suggested at experiment creation** from events that already exist: `$exception` (error rate, `not_worse`, δ=0) and the last funnel step. Latency is the standard third but is not instrumented here — don't advertise it.

**Files:** `internal/flag/guardrail.go`; `internal/flag/experiment.go`; `internal/flag/measure.go` (a `Guardrails []GuardrailResult` block on `Report`); dashboard flag pane; `internal/mcp/flags.go`.

**Test:** `internal/flag/guardrail_test.go` — `TestGuardrailPassFailInconclusive` over a fixture table; `TestInconclusiveIsNeverReportedAsPass` (the honesty test); `TestGuardrailUsesOneSidedBound` (assert the bound differs from the two-sided one at the same α).

**Effort:** M.

---

## 8. Pin the hash as a spec: version it, publish it, make it reproducible by hand

**Why it matters:** LaunchDarkly shipped a 60-bit-into-53-bit-float precision defect and closed it "not planned" — because once users are bucketed you can never fix your hash. GrowthBook had to ship `hashVersion` as *per-experiment data* to migrate off a biased v1. Split's Ruby SDK silently used a different hash from every other language for years. This repo already has the *right* hash; what's missing is the promise that it can never silently change, and the tool that lets a skeptic verify one specific user.

**What to build**

1. **`HashVersion int` on `Experiment`, default 1**, written at creation, immutable after start. `bucket()` dispatches on it. There is exactly one version today; the point is that version 2 can never retroactively re-randomise a running experiment.
2. **`internal/flag/spec.go`** — an exported `BucketSpec` string constant, published verbatim at `GET /v1/flags/spec` and in `/install.md`:
   > `bucket(salt, id) = uint64(sha256(salt + ":" + id)[0:8], big-endian) >> 4, divided by 2^60`
   > `salts: "rollout:" + flagKey, "variant:" + flagKey`
   > `variant selection: walk cumulative weight/total boundaries in [0,1), first boundary strictly greater than bucket wins`
3. **Conformance fixture** `internal/flag/testdata/bucketing_v1.json` — 500 `(salt, id) → bucket` pairs to 12 decimals, plus 100 full `(flag definition, distinct_id) → variant` cases. Any SDK, in any language, written by anyone's agent, is verifiable in CI against this file. This is Unleash's `client-specification` idea, which is the only mechanism that has ever kept cross-language hashes identical.
4. **`explain_assignment`** — MCP tool + `smolanalytics bucket <flag> <id>` CLI, printing every intermediate:
   ```
   flag        checkout_v2   (hash_version 1)
   salt        variant:checkout_v2
   input       variant:checkout_v2:u_8813
   sha256      9f2c7a1e04b5...   (first 8 bytes: 9f2c7a1e04b53c11)
   top 60 bits 716329041887215617
   bucket      0.62089...
   ranges      control [0.000, 0.500)   treatment [0.500, 1.000)
   → variant   treatment
   ```
   Nobody in the category makes it one command to verify a *specific user's* bucket. This is the cheapest credibility in the whole document.

**Files:** new `internal/flag/spec.go`; `internal/flag/flag.go` (version dispatch); new `cmd/smolanalytics/bucket_cmd.go` + `main.go:91` case; `internal/mcp/flags.go`; `internal/api/api.go` (`GET /v1/flags/spec`).

**Test:** `internal/flag/spec_test.go`
- **`TestBucketingConformanceFixture`** — every fixture row reproduces exactly. If someone touches `bucket()`, this fails loudly with "you are about to re-randomise every running experiment".
- `TestHashVersionIsImmutableAfterStart`.
- `TestExplainAssignmentMatchesEvaluate` — the CLI's printed variant equals `Flag.Evaluate` for 10,000 random ids.

**Effort:** S (the fixture is generated once by the code itself).

---

## 9. Fix exposure dedupe: per *session*, not per page-load; and log server-side exposures

**Why it matters:** `flagExposed` (`sdk.js:54`) is a plain in-memory object reset on every page load. The SDK comment (`sdk.js:695`) and **three** places in the dashboard (`dashboard.tmpl.html:1925, :3741, :3745`) all promise "one exposure per visitor per session". A five-page visit logs five. Stats are unaffected (both `Measure` and `CheckSRM` dedupe to first exposure), but **this meter bills events** and the copy is wrong — which is a `shipped-means-reachable` violation in the other direction: we're advertising a cost we don't charge.

Second gap: exposures are logged **only** when app code calls `smolanalytics.flag()`. Server-side evaluation via `evaluate_flag` or a backend calling `/v1/flags/evaluate` logs nothing, so a server-rendered experiment is invisible to `Measure` and `CheckSRM`.

**What to build**

1. `sdk.js` — persist the dedupe set in `sessionStorage` under `smol_fx` as a JSON object of `flagKey → variant`. Wrap in try/catch; fall back to in-memory when storage is denied. **Re-log** if the cached variant differs from the current one — a variant change mid-session is a multi-variant exposure and item 13 needs to see it.
2. `/v1/flags/evaluate` — accept `&log_exposure=1`. When set, and the flag is `Measured`, the server enqueues the `$feature_flag_called` event itself. This is the path a backend or a native SDK uses.
3. Native SDKs (Swift/Kotlin/RN/Flutter, external repos): flags are **web-only** today — no `$feature_flag` or `/v1/flags/evaluate` reference exists in any of them. Until they ship, the dashboard must say "feature flags are web-only" rather than let a mobile user wonder why the pane is empty.

**Files:** `internal/api/sdk.js:54, :693-702`; `internal/api/flags_api.go:99-111`; copy at `dashboard.tmpl.html:1925, :3741, :3745`.

**Test:** `internal/api/flags_test.go` — `TestExposureDedupeIsPerSessionNotPerPageLoad` (drive the SDK logic via the existing JS test harness or a Go-side port of the dedupe rule); `TestServerSideEvaluateLogsExposure`; `TestVariantChangeMidSessionRelogs`.

**Effort:** S/M.

---

## 10. Flag flip records a deploy marker + full audit trail

**Why it matters:** `flag.go:3-5` calls this *"what makes it deeper than a plain flag console"* and `store.go:114-115` says *"a future increment records this flip as a deploy marker."* It is prose only — grep confirms no `deploys` reference in `internal/flag`. The deploy-impact engine it would feed **already exists** (`internal/deploys/impact.go`). This is unbuilt, not blocked, and it's the differentiator the package doc leads with.

Separately: `Store` keeps only `Created`/`Updated`. There is **no record of a weight edit mid-experiment** — the exact event `srm.go:114`'s verdict text blames for mismatches. When SRM fires, the very first thing the verdict should say is "the weights were changed on day 4".

**What to build**

- `Store` takes an optional `func(Deploy)` sink and an `*audit.Log`. On `SetEnabled` and on any `Save` that changes `Enabled`, `Variants`, `Rules` or `Measured`, record a `deploys.Deploy{Source:"flag", Message:"flag checkout_v2 turned on (50/50 control/treatment)", At: now}` and an `audit.Entry`.
- Config-change timeline ships **inside** `SRMResult.Verdict` when a change falls inside the exposure window: *"the variant weights were changed 4 days into this experiment. That alone explains a split mismatch — the users bucketed before the change were bucketed under different weights."*

**Files:** `internal/flag/store.go:114-123`; wiring in `cmd/smolanalytics/main.go` and `internal/api/api.go` (where the deploy store is already constructed); `internal/flag/srm.go` (verdict enrichment).

**Test:** `internal/flag/store_test.go` — `TestFlipRecordsDeployMarker`, `TestWeightEditIsAudited`, `TestSRMVerdictNamesConfigChange`.

**Effort:** S.

---

# Tier 2 — the real moat (M effort, high trust)

---

## 11. Multiple-comparison control (Benjamini–Hochberg), with the family scoped like the incumbents

**Why it matters:** a 4-arm flag today runs 3 uncorrected tests against control at 95% each — a ~14% chance of at least one false winner before you even add a second metric. PostHog handles this **editorially** ("more metrics can help, if they're planned") with no correction documented anywhere. Applying an explicit correction and stating the family is a straight honesty win we can put in writing.

**Algorithm** (`internal/flag/mcc.go`) — BH controlling FDR at `q = α`:

```
family = { goal metric } × { non-control arms }   (item 5's Secondary and Guardrails are EXCLUDED)
sort p ascending → p_(1) … p_(m)
k = max{ i : p_(i) ≤ (i/m)·q }
reject 1..k
adjusted p_(i) = min over j ≥ i of ( (m/j)·p_(j) )       // monotonicity enforced
```

Excluding guardrails is deliberate and matches GrowthBook and Statsig: a guardrail is a test you *want* sensitive.

**Adjusted intervals.** There is no canonical BH-adjusted CI. GrowthBook back-solves the SE that would produce the adjusted p and rebuilds the interval from it, purely so an adjusted-significant result has an interval excluding zero. Do the same — **and label it as ad hoc in the payload**, in the field name: `delta_ci_adjusted_note: "back-solved from the adjusted p-value so the interval agrees with the decision; this is a presentation convention, not a derived interval"`. Being the one tool that says this out loud is worth more than the interval.

**Files:** new `internal/flag/mcc.go`; `internal/flag/measure.go` (add `PValueAdjusted`, `SignificantAdjusted`, and `Family int` to `VariantResult`; a `Correction string` on `Report`).

**Surfaced as:** dashboard shows both raw and adjusted p with a one-line explanation of the family size; `flag_impact` returns both.

**Test:** `internal/flag/mcc_test.go`
- `TestBHAgainstPublishedTable` — the standard 10-hypothesis worked example.
- `TestBHAdjustedPIsMonotone`.
- `TestGuardrailsExcludedFromFamily`.
- `TestBHReducesFalseWinnersInAAN` — 1,000 seeded 4-arm A/A/A/A simulations; assert raw "any arm significant" rate ≈ 14% and BH-adjusted ≤ 6%.

**Effort:** M.

---

## 12. CUPED via regression adjustment, with per-user pre-exposure lookback and honest refusal

**Why it matters:** 20–50% variance reduction on engagement metrics — Microsoft measured it as "+20% more traffic" for the majority of metrics. GrowthBook paywalls it at $40/seat; shipping it free in an OSS binary makes this the most statistically capable zero-cost option in the category. And the *honest refusal* half is unique: nobody tells you when CUPED did nothing.

**Algorithm** (`internal/flag/cuped.go`)

Covariate `X_u` = the user's count of the goal event in the **7 days strictly before that user's own first exposure** (Statsig's per-user window, not a fixed calendar window — strictly better, identical effort, and it also debiases pre-exposure imbalance).

```
θ = Cov(Y, X) / Var(X)                  // pooled across arms
Y_adj_u = Y_u − θ·(X_u − X̄)             // X̄ = pooled mean
ρ = corr(Y, X)
Var(Y_adj) = Var(Y)·(1 − ρ²)
```

Implement as **OLS regression adjustment** rather than the closed form. `Y = a + b·T + θ·X + ε` with heteroskedasticity-robust (HC1) standard errors gives CUPED, post-stratification (X = stratum dummies) and CUPAC (X = an out-of-sample prediction) as *configurations of one auditable code path*, and Lin (2013) guarantees it can't hurt asymptotic precision. One code path, three features, one set of tests.

**Guards (all three must hold, else skip):** `X` must be strictly pre-exposure; ≥100 units with pre-period data; >5% of units have it; pooled adjusted variance < unadjusted variance.

**Honest refusal — the differentiating half.** When skipped, the report says which guard failed, in plain language:
> *"CUPED was skipped: ρ = 0.04 between pre-period and post-period behaviour. This is a new-user experiment — these users have no history to adjust against, so there is nothing to subtract. Your interval is unchanged and that is correct, not a bug."*

Always report `variance_reduction_pct` and the effective sample-size gain: *"CUPED cut the variance 31%, worth about 4,400 extra users you didn't have to wait for."*

**Files:** new `internal/flag/cuped.go` + `internal/flag/ols.go`; `internal/flag/experiment.go` (`CUPEDCfg{Enabled bool, LookbackDays int (default 7), Covariate string}`); `internal/flag/measure.go`.

**Test:** `internal/flag/cuped_test.go`
- `TestCUPEDReducesVarianceOnCorrelatedCovariate` — synthetic data with known ρ=0.6; assert measured reduction ≈ 36% ± 3pp.
- `TestCUPEDIsUnbiased` — 1,000 seeded sims, mean adjusted effect equals true effect within MC error.
- `TestCUPEDSkippedOnNewUserExperiment` — assert skipped *and* assert the reason string names ρ.
- `TestCUPEDRejectsPostTreatmentCovariate` — covariate window overlapping exposure must error, not silently bias.
- `TestOLSMatchesClosedFormCUPED` — the two paths agree to 1e-9. (This is a free continuous correctness check.)

**Effort:** M/L.

---

## 13. Exposure health: the checks nobody runs, as one verdict

**Why it matters:** SRM on assigned counts is table stakes; the failure modes that actually bite are the ones around it. Spotify: *"a poorly designed trigger analysis can instead lead to a loss in what you can learn"* — if treatment changes **who** triggers, you have post-treatment selection bias and the comparison is broken, and SRM on the assigned counts won't show it.

**What to build** — extend `SRMResult` into an `ExposureHealth` with six checks, each returning pass/warn/fail + a plain-English cause:

1. **Split SRM** — existing `CheckSRM`, unchanged.
2. **Multi-variant exposure** — count users whose exposures name >1 variant. Today `firstExposures` (`srm.go:147-171`) silently first-wins them. Report the count and the cause: identity stitching (`internal/alias` collapsed two ids), a changed salt, or logged-out→logged-in without `bucket_id`. Threshold: warn >0.1%, fail >1%.
3. **Dead arm** — any declared arm with 0 exposures while another has ≥30 (item 3).
4. **Exposure-after-goal** — users whose first `goal` event precedes their first exposure, as a share. High share = the exposure is logged too late (after the user already converted), which dilutes the measured effect toward zero.
5. **Dilution / exposed fraction** — `f = exposed_users / eligible_users`, with the implied sample-size multiplier `1/f²`. Spotify wrote the blog post; nobody built the widget. Show: *"only 9% of eligible users were exposed. Either the code path is rarer than you think, or the exposure is logged in the wrong place — and at f=0.09 you need 123× the sample to see the same effect."*
6. **Control-side exposure present** — if only one arm ever logs an exposure, that is not an SRM, it's a missing `sendExposure()` on the control branch. Non-negotiable failure.

**Verdict:** a single `decision_ready: true|false` boolean with `blockers []string`.

**Files:** `internal/flag/srm.go` → `internal/flag/health.go`; `internal/api/flags_api.go` (item 1's `/health` endpoint returns the full object); `internal/mcp/flags.go` (`experiment_health` returns it).

**Surfaced as:** dashboard banner (item 1) lists blockers; **MCP `experiment_readiness`** — one call returning `decision_ready` + blockers + the adjusted interval + guardrail states, so a coding agent can answer *"is this safe to ship"* without opening a scorecard. That is the ICP fit and no incumbent exposes it to an agent.

**Test:** `internal/flag/health_test.go` — one table-driven test per check with a hand-built fixture; `TestDecisionReadyIsFalseWhenAnyBlockerFires`; `TestTriggeredSRMCatchesDifferentialTriggering` (seed a treatment that suppresses triggering in one segment; assert check 5 + check 1 disagree, which is the signature).

**Effort:** M.

---

## 14. Result receipts: content-address every number, and ship `verify`

**Why it matters:** the repo's actual claim is "one question, one answer, recomputed from raw events." PostHog has open, unresolved bugs where the same question returns different numbers depending on where you ask it (insight vs dashboard: 3431 vs 3286; experiment result vs funnel breakdown; `This month` vs `Date to now` vs manual range). GrowthBook and Eppo counter with "here's the SQL, run it yourself" — which still depends on the warehouse not having changed underneath you. **Nobody emits a result hash.** This turns a marketing claim into a command anyone can run.

**What to build** (`internal/flag/receipt.go`, generalisable to every report later)

Every `Report` carries:

```go
type Receipt struct {
    Hash        string    `json:"hash"`          // sha256, hex, first 16 chars shown
    Engine      string    `json:"engine"`        // build version
    PlanHash    string    `json:"plan_hash"`     // item 5
    Flag        string    `json:"flag"`
    Goal        string    `json:"goal"`
    From, To    time.Time `json:"from","to"`     // ABSOLUTE, half-open — item 3
    ScopeVersion string   `json:"scope_version"` // query.Keeper definition version
    Timezone    string    `json:"timezone"`
    EventCount  int       `json:"event_count"`
    MaxEventTS  time.Time `json:"max_event_ts"`  // the completeness watermark
    InputDigest string    `json:"input_digest"`  // sha256 of sorted event IDs in scope
}
```

`Hash` = SHA-256 over canonical JSON of everything above except `Hash`. Two people pasting the same receipt provably have the same number.

**`smolanalytics verify <receipt-json|hash>`** — recompute against the current store and either print `MATCH` or print the diff *and the reason*:
- `input_digest` changed, `max_event_ts` advanced → *"6 late events arrived after this receipt was issued"*
- `plan_hash` changed → *"the analysis plan was amended on 2026-07-25"*
- `engine` changed → *"engine upgraded from v0.9.9 to v1.0.1"*
- `scope_version` changed → *"the production-scope definition changed"*

**Prerequisite this exposes:** `event.Event` has no ingest timestamp (`internal/event/event.go:11-17`), so a genuinely-late event is indistinguishable from a re-dated one. Add `Received time.Time` at ingest (`api.go:748-823`, where clamping already happens). It costs one field and unlocks the completeness watermark, "this range is still settling" markers, and every future as-of report.

**Files:** new `internal/flag/receipt.go`; `internal/event/event.go` (+`Received`); `internal/api/api.go:811` (stamp it); new `cmd/smolanalytics/verify_cmd.go` + `main.go` case; `internal/mcp/flags.go` (`verify_receipt`); dashboard — the receipt hash rendered in muted mono under every A/B table with a copy button.

**Test:** `internal/flag/receipt_test.go`
- `TestSameInputsSameReceipt` — 100 runs, one hash.
- `TestLateEventChangesReceiptAndVerifyExplainsWhy` — assert the *reason string*, not just the mismatch.
- `TestAmendedPlanChangesReceipt`.
- `TestReceiptIsClockIndependent` — depends on item 3's absolute window; this is the test that keeps `time.Now()` out of the pure path forever.

**Effort:** M/L.

---

## 15. The A/A harness: publish measured false-positive rate and coverage for *our* code

**Why it matters:** every incumbent cites papers. **None publishes empirical coverage evidence for its own implementation.** Eppo comes closest — it publishes exact formulas *and* the limitation that its own SRM test isn't sequentially valid, which is the single highest-trust artifact in the whole competitive sweep. This beats it: pure engineering, no research, and it is a checkable claim rather than a citation.

**What to build** (`internal/flag/aa_test.go` + `cmd/smolanalytics` hidden `selftest` subcommand)

For each shipped configuration — `{fixed, sequential} × {no CUPED, CUPED} × {2-arm, 4-arm} × {raw, BH}` — run 2,000 seeded A/A experiments through **the real `Measure` path** (not a reimplementation), synthesising exposure and goal events into a memory store:

- **False-positive rate** — share declaring any arm significant. Must be within the binomial 99% CI of nominal α.
- **CI coverage** — share of intervals containing the true effect (0). Must be ≥ nominal.
- **Power** — with a known injected effect at the calculator's `n_planned`, must be ≥ 0.75 for a nominal 0.80 design.
- **Peeking FPR** — the sequential mode peeked every 100 users; must stay ≤ α. Fixed mode is *expected* to blow past it, and the test asserts that it does, so the table shows the honest contrast.

Commit the raw output to `internal/flag/testdata/aa_results.json` on every release and render it at `GET /v1/flags/spec` and in the docs as a table:

| mode | metric | arms | nominal α | measured FPR | CI coverage | power |
|---|---|---|---|---|---|---|
| sequential | binary | 2 | 0.05 | 0.048 | 95.4% | 0.79 |
| fixed (peeked ×20) | binary | 2 | 0.05 | **0.241** | 79.2% | — |

Runtime: gate the full 2,000-rep matrix behind `-tags slow` or `SMOL_AA=1`; run a 200-rep smoke version on every CI run.

**Files:** new `internal/flag/aa_test.go`, `internal/flag/testdata/aa_results.json`, `internal/api/flags_api.go` (spec endpoint serves the table), docs.

**Effort:** M. **This is the highest-leverage marketing artifact in the document and it is written entirely in Go.**

---

# Tier 3 — depth (M/L, ships the remaining category-parity gaps)

---

## 16. Ratio metrics + delta method, with a free cross-check against the cluster-robust estimator

**Why it matters:** today the only metric shape is a binary per-user conversion. Revenue-per-user, events-per-session, and CTR-when-randomising-users are all ratios, and **the naive standard error of a ratio is simply wrong** — dropping the covariance term is the classic silent bug. Separately: whenever the analysis unit is finer than the randomisation unit (sessions inside users), a naive test understates the SE by `sqrt(1 + (m−1)·ICC)` — at m=8, ICC=0.3 that's 1.8×, turning a nominal 5% test into a real 20–25% one. An engine that recomputes from raw events is uniquely positioned to *always* aggregate to the randomisation unit first.

**Algorithm** (`internal/flag/ratio.go`) — Deng/Knoblich/Lu (KDD 2018):

```
per randomisation unit k: S_k = Σ metric,  N_k = count of analysis units
Var(S̄/N̄) ≈ (1/(K·μ_N²)) · [ σ_S² − 2·(μ_S/μ_N)·σ_SN + (μ_S²/μ_N²)·σ_N² ]
```

Percent change vs control:
```
(S̄_t/N̄_t) / (S̄_c/N̄_c) − 1,  with the delta-method variance above on each side
```

**The free correctness check.** The delta method and the cluster-robust (sandwich, HC1 with clusters = randomisation units) variance estimator are **provably equivalent** for clustered randomised experiments (arXiv:2105.14705). So compute **both** on every ratio metric and flag divergence >1% as an implementation or data bug. This is a continuous, zero-cost self-audit no vendor runs.

**Refuse rather than silently compute wrong.** If the declared randomisation unit (`bucket_id`) is absent from the events, or the analysis unit is finer than the randomisation unit with no cluster path available, **return an error naming the estimand problem** instead of a p-value. This is the opposite of every vendor default and it is the single most on-brand behaviour in the whole spec.

Heavy tails: winsorize at p1/p99 by default, **applied identically to both arms**, with the percentiles recorded in the plan hash so capping can't become a researcher degree of freedom.

**Files:** new `internal/flag/ratio.go`; `internal/flag/experiment.go` (`MetricKind{binary|mean|ratio}`, `Numerator`, `Denominator`, `WinsorPct`); `internal/flag/measure.go`.

**Test:** `internal/flag/ratio_test.go`
- `TestDeltaMethodMatchesClusterRobust` — 500 seeded datasets, assert the two variance estimators agree to 1%.
- `TestIgnoringCovarianceIsWrong` — assert the naive-no-covariance variance differs materially, so the term can never be dropped by a future refactor.
- `TestClusteredDataInflatesSE` — synthetic ICC=0.3, m=8; assert the clustered SE ≈ 1.8× the naive one.
- `TestRefusesWhenRandomisationUnitAbsent` — asserts an error, not a number.

**Effort:** M/L.

---

## 17. Mutually exclusive layers

**Why it matters:** Google's 2010 overlapping-experiment paper makes layer-orthogonality a change to the **hash input**: `mod = f(cookie, layer) % 1000`. Statsig's layers are the direct commercial descendant. A solo builder running three experiments at once already has interaction contamination and will never know. Layers are cheap here because the hash is already salted correctly — and GrowthBook gates holdouts behind Enterprise, so shipping this free is upside.

**What to build**

1. `Experiment.Layer string`. When set, the rollout salt becomes `"layer:" + layer + ":rollout:" + key` and the variant salt `"layer:" + layer + ":variant:" + key`. Folding the layer into the hash gives mutual exclusion **within** a layer and independence **across** layers, exactly as the paper specifies.
2. Layer allocation: each experiment in a layer claims a contiguous `[lo, hi)` slice of the layer's `[0,1)` space, computed from `hash("layer:"+layer, bucketID)`. Overlapping claims are rejected at save time with the specific conflict named.
3. **Derive the salt from the experiment key, never from a random seed pool.** With 365 seeds you only need ~23 experiments for a 50% chance of a collision — two experiments sharing a seed get *identical* splits, invisible in the UI and fatal to inference. `Store.Save` already enforces unique keys, so deriving from the key structurally eliminates the birthday problem. Say so in `spec.go`.
4. **Global holdout** — a reserved layer `__holdout` holding N% of users out of every measured flag, so `deploy_impact` can answer "what did everything we shipped this quarter actually do". Both Statsig and Eppo state that without holdouts the sum of individual experiment wins overstates reality.

**Files:** `internal/flag/layer.go`; `internal/flag/flag.go` (salt construction); `internal/flag/store.go` (overlap validation); `internal/mcp/flags.go`; `dashboard.tmpl.html`.

**Test:** `internal/flag/layer_test.go`
- `TestSameLayerIsMutuallyExclusive` — 100k ids, zero users in both experiments of one layer.
- `TestCrossLayerIsIndependent` — φ between assignments across two layers < 0.02 (mirrors the existing `TestParallelExperimentsAreIndependent`).
- `TestOverlappingAllocationRejected`.
- `TestHoldoutExcludesFromAllMeasuredFlags`.

**Effort:** M.

---

## 18. Authorable targeting, weights and arms — close the "engine can, UI can't" gap

**Why it matters:** `Rule.Filters` is evaluated by the engine (`flag.go:60`) using the same `query.Matches` as every report — the headline capability — and it is authorable from **no** first-class surface. `create_flag` has no `filters` parameter (`mcp/flags.go:21-28`); the dashboard creator emits only boolean or a hardcoded 50/50 (`dashboard.tmpl.html:3824`). Raw `POST /v1/flags` JSON is the only path. Per the `shipped-means-reachable` rule, a feature the user cannot find is one they think we never built.

**What to build**

- `create_flag` MCP: add `filters` (array of `query.Filter`), `variants` with arbitrary weights, `layer`, and the whole `experiment` block (goal, control, guardrails, mode, mde_pct).
- Dashboard: extend the create row to the existing filter-builder component (already used by the toolbar) + an N-arm weight editor with a live sample-size readout from item 6 (*"90/10 needs 2.8× the users of 50/50"*).
- Store existing goal on the experiment so the dashboard stops asking the user to retype it on every read (`dashboard.tmpl.html:3661`).

**Files:** `internal/mcp/flags.go:21-28, :101-103`; `internal/api/dashboard.tmpl.html:3810-3830`.

**Test:** `internal/api/dashboard_inventory_test.go` — extend to assert every field on `flag.Flag`/`flag.Experiment` is authorable from at least one non-raw-JSON surface. This is the anti-vapor gate, generalised.

**Effort:** M.

---

## 19. Novelty / primacy detection and winner's-curse shrinkage

**Why it matters:** the two external-validity threats Kohavi names, both detectable from data already on disk, neither shipped anywhere in the small-tool tier. And selection on "significant" makes the reported effect systematically too large — worst exactly where teams are most excited.

**What to build**

- **Per-day effect series** (not cumulative): plot `p_t(d) − p_c(d)` by day since each user's own exposure. Flat = real; decaying toward zero = novelty; rising = primacy. Cross-check by segmenting new vs returning users — novelty typically shows a large decaying effect among returning users and a stable one among new. Emit a one-line verdict, not a chart the user must interpret.
- **Empirical-Bayes shrinkage** from the org's **own** historical experiment effect distribution (we have every past experiment's raw events, which is the whole point). Fit `τ²` across completed experiments, then report `shrunk = observed · τ²/(τ² + se²)` as: *"reported lift +18%, shrunk estimate +6%. Across your last 11 experiments, effects this size have usually been smaller than they first looked."* Only possible for an engine that holds the raw history.
- Refuse to render either below 14 days / 2 business cycles.

**Files:** `internal/flag/novelty.go`, `internal/flag/shrinkage.go`; `measure.go` (`Trend` + `ShrunkPct` on `Report`); dashboard + `flag_impact`.

**Test:** `internal/flag/novelty_test.go` — synthetic decaying/flat/rising effects, assert the correct verdict for each; `TestShrinkageIsIdentityWhenSEIsZero`; `TestShrinkagePullsSmallSampleHarder`.

**Effort:** M.

---

# Cross-cutting determinism fixes (small, do them alongside)

These are not experimentation-specific but they undercut the determinism claim the experiment work rests on. Each is S.

| Fix | Where | Why |
|---|---|---|
| **Wire the sampler exclusion** — `query.NotSampler` has only 2 callers (`ask.go:51`, `insight/insight.go:122`) despite its own doc comment naming retention/stickiness/lifecycle/paths/sessions. Verified live: `/v1/retention` counts the robot, the ask bar doesn't. **And `internal/query/sampler.go` is untracked in git** — the fix was never committed. | `internal/query/sampler.go` + every report handler | This is the exact three-inches-apart disagreement the product claims to have eliminated. Also fix `internal/api/sampler_agreement_test.go:53`, which POSTs a `{"batch":[…]}` body that ingest rejects, so the guard test ingests nothing and currently fails. |
| **XAU window alignment** — `trends.go:695` subtracts calendar days from an *unaligned* `from`, so `?measure=wau&hours=6` returns 6 while `?measure=wau&days=1` returns 7 on identical data. | `internal/trends/trends.go:693-696` | Add `TestXAUIsWindowGrammarInvariant`: for any `from/to` covering the same instant, `ComputeXAU` must return the same current value. The agreement test only exercises `days=7` with no `measure=`, so nothing catches it today. |
| **Timezone is stored and used by nothing** — `settings.Timezone()` exists, is IANA-validated, rendered in settings, returned by `get_settings`, and every bucket boundary is hard-UTC (`trends.go:61, :547`, `retention.go:61`, `engagement.go:12`, `ask.go:1121`). | thread `*time.Location` through every bucket function | A UI that lies about what it changed. Until it's plumbed, the setting should be removed or labelled "display only". |
| **Funnel/paths tie-break** — `sort.SliceStable` on `Timestamp` alone (`funnel.go:283`, `paths.go:50`) falls back to *storage order*; the same two events at the same millisecond give converted=1 or 0 depending on import/compaction order. | add a secondary sort key on `Event.ID` | A funnel result that flips after a segment rewrite with no data change is the exact thing receipts are supposed to make impossible. |
| **Session list vs detail** — list goes through `s.filtered()`, detail reads `store.Range(zero,zero)` raw (`sessions_api.go:24` vs `:39`); a dev-env pageview makes the list emit a `start_unix` the detail 404s on. MCP has the identical bug (`mcp.go:869` vs `:881`) so the agreement test can't catch it. | `internal/api/sessions_api.go`, `internal/mcp/mcp.go` | Two surfaces, one question, two answers — the thing the whole positioning is against. |
| **One window grammar** — `parseTrendWindow` is hand-copied four times (`query_api.go:371`, `mcp.go:457`, `ask.go:1119`, `dashboard.go:1662`) with the invariant held only by enumerated test cases. | extract to `internal/query/window.go`; all four call it | Any new grammar has to be reimplemented correctly in four places today. |
| **Silent data loss** — `SMOLANALYTICS_MAX_EVENTS` drops oldest events (`file.go:228`) and segment prune is whole-segment-granular (`segment.go:303`) with no marker in any payload. | add `truncated_before` to every report's receipt block | "All time" silently means "since the cap kicked in". |

---

# Build order (dependency-correct)

```
1  SRM surfaced (S)              ─┐
2  Render the intervals (S)       │  ship this week — pure honesty, zero new math
3  Dead arms + declared control    │
   + absolute window (S)         ─┘
8  Hash spec + conformance + explain_assignment (S)
10 Flag flip → deploy marker + audit (S)
9  Exposure dedupe per session (S/M)
   ↓
5  Experiment object + plan lock (M)   ← unblocks 4, 6, 7, 11, 12, 14, 17
   ↓
4  Sequential / always-valid (M)
6  Power / MDE / duration (M)
7  Guardrails (M)
11 Benjamini–Hochberg (M)
13 Exposure health + experiment_readiness (M)
   ↓
15 A/A harness (M)                     ← proves 4, 11, 12 empirically; the artifact
14 Receipts + verify (M/L)             ← needs 3's absolute window + event.Received
   ↓
12 CUPED / OLS (M/L)
16 Ratio + delta method (M/L)
17 Layers + holdouts (M)
18 Authorable targeting (M)
19 Novelty + shrinkage (M)
```

Cross-cutting determinism fixes: fold in opportunistically; the sampler one and the XAU one should go with item 1, because they are the same class of defect and the same kind of test.

---

# The five sentences this buys you

1. **"Every number carries a receipt. Paste it into `smolanalytics verify` and either it matches or we tell you exactly what changed."** No product-analytics or experimentation vendor emits a result hash.
2. **"We publish the measured false-positive rate and confidence-interval coverage of our own code, per release, in the repo."** Every incumbent cites papers; none publishes coverage evidence for its implementation.
3. **"You can recompute any user's bucket by hand — here's the spec, here's the fixture, here's the one command."** Statsig, GrowthBook and Optimizely document the algorithm in prose; none makes it one command for a specific user.
4. **"Sequential testing, CUPED, sticky bucketing, holdouts, layers and guardrails are all in the free binary."** GrowthBook paywalls the first three at $40/seat and holdouts at Enterprise.
5. **"When we can't compute it correctly, we refuse and say why."** Ratio metrics without a randomisation unit, CUPED with ρ≈0, heavy-tailed power calculations, guardrails that are merely inconclusive — all return a reason, not a number. That is the opposite of every vendor default and it is the only claim on this list that compounds.