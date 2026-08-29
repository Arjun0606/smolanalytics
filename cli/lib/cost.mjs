// WHAT A RUN COST, AND A CEILING ON WHAT THE NEXT ONE CAN.
//
// This is the objection a buyer actually has. Not "will this company exist next year" — nobody
// researches a cap table before paying $19 — but "I am handing my Anthropic key to a loop that
// drives a browser on every pull request across a team, and I have no idea whether that is five
// dollars a month or five hundred, and no way to stop it."
//
// The data was already there and being thrown away: every /v1/messages response carries a `usage`
// block with the exact token counts, and `think()` returned the whole body while its caller read
// only `content` and `stop_reason`. So this costs nothing to collect and is never an estimate.
//
// TOKENS ARE REPORTED ALWAYS. DOLLARS ONLY WHEN THE PRICE IS KNOWN.
//
// Token counts come from the API and cannot be wrong. A dollar figure needs a per-model price, and
// prices change, models get added, and a number this file invented would be printed next to real
// measurements as though it were one of them — on the exact screen someone uses to decide whether
// they can afford this. A wrong bill estimate is worse than no bill estimate, because it is
// believed. So a price lives here only if it is supplied, and `SMOLANALYTICS_PRICE_IN` /
// `SMOLANALYTICS_PRICE_OUT` (US dollars per million tokens, from the customer's own pricing page)
// turn tokens into money for whoever wants that.
//
// THE CAP IS ON CALLS, NOT ON DOLLARS, and that is deliberate. A dollar cap is only as good as the
// price table behind it; a call cap is exact, needs no pricing, and maps to the thing that actually
// runs away — an agent looping on a page it cannot work out. `--max-calls` stops cleanly, reports
// why, and exits 2, because a budget we enforced is our decision and never a verdict about the
// customer's application.

/** Zero. A run that never reaches the model still reports, so a replay can say "no model calls". */
export function newLedger() {
  return { calls: 0, input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
}

/**
 * Fold one API response's usage into the ledger.
 *
 * Every field is read defensively. A response missing `usage`, or carrying a shape a later API
 * version introduces, must not crash a run that has already produced a verdict — the ledger is
 * bookkeeping, and bookkeeping never decides whether somebody's checkout works.
 */
export function record(ledger, res) {
  const u = res && typeof res === "object" ? res.usage : null;
  const n = (v) => (Number.isFinite(v) && v > 0 ? Math.floor(v) : 0);
  ledger.calls += 1;
  if (!u || typeof u !== "object") return ledger;
  ledger.input += n(u.input_tokens);
  ledger.output += n(u.output_tokens);
  ledger.cacheRead += n(u.cache_read_input_tokens);
  ledger.cacheWrite += n(u.cache_creation_input_tokens);
  return ledger;
}

/** Two ledgers into one, so a suite can total its tests without knowing how they ran. */
export function merge(a, b) {
  const out = newLedger();
  for (const k of Object.keys(out)) out[k] = (a?.[k] || 0) + (b?.[k] || 0);
  return out;
}

/**
 * The prices, if the operator supplied them. US dollars per MILLION tokens, which is how every
 * model vendor quotes them, so a value is copied from a pricing page rather than converted.
 *
 * Returns null when either side is missing: half a price is not a price, and multiplying by a
 * default would produce a plausible number nobody chose.
 */
export function priceFrom(env = process.env) {
  const num = (v) => {
    // AN ABSENT VALUE IS NOT ZERO. Number("") is 0, which is finite and non-negative, so an earlier
    // version of this returned {input: 15, output: 0} when only one side was set — a bill estimate
    // in which output tokens are free, printed next to real measurements as though it were one.
    const raw = String(v ?? "").trim();
    if (!raw) return null;
    const x = Number(raw);
    return Number.isFinite(x) && x >= 0 ? x : null;
  };
  const input = num(env.SMOLANALYTICS_PRICE_IN);
  const output = num(env.SMOLANALYTICS_PRICE_OUT);
  if (input === null || output === null) return null;
  return { input, output };
}

/** Dollars, or null when no price was supplied. Cache reads are billed as input here — the
 *  conservative direction: it can only ever over-state, never quietly under-state a bill. */
export function dollars(ledger, price) {
  if (!price) return null;
  const inTok = (ledger.input || 0) + (ledger.cacheRead || 0) + (ledger.cacheWrite || 0);
  return (inTok / 1_000_000) * price.input + ((ledger.output || 0) / 1_000_000) * price.output;
}

const thousands = (n) => Number(n || 0).toLocaleString("en-US");

/** `$0.0834` — four places, because a per-run figure here is usually cents and rounding it to two
 *  turns most real runs into "$0.00", which reads as free and is the wrong thing to believe. */
export function money(usd) {
  if (usd === null || usd === undefined) return "";
  return `$${usd < 1 ? usd.toFixed(4) : usd.toFixed(2)}`;
}

/**
 * One line, and it says nothing it cannot prove.
 *
 *   no model calls                            a replay: the whole economic argument, stated
 *   4 model calls · 21,430 in / 1,205 out     tokens, exact, from the API
 *   … · $0.0834                               only with a price supplied
 */
export function costLine(ledger, price = null) {
  if (!ledger || !ledger.calls) return "no model calls";
  const parts = [
    `${thousands(ledger.calls)} model call${ledger.calls === 1 ? "" : "s"}`,
    `${thousands(ledger.input + ledger.cacheRead + ledger.cacheWrite)} in / ${thousands(ledger.output)} out`,
  ];
  const usd = dollars(ledger, price);
  if (usd !== null) parts.push(money(usd));
  return parts.join(" · ");
}

/** The nudge, printed once when tokens were spent and no price was configured. Never on a replay,
 *  because a run that called no model has no bill to explain. */
export function priceHint(ledger, price = null) {
  if (!ledger || !ledger.calls || price) return "";
  return "set SMOLANALYTICS_PRICE_IN and SMOLANALYTICS_PRICE_OUT (dollars per million tokens, from your model's pricing page) to see this in money";
}

/**
 * The ceiling. Returns the reason to stop, or "" to carry on.
 *
 * Checked BEFORE a call rather than after, so the cap is a limit on what gets spent and not a
 * report on what already was.
 */
export function overBudget(ledger, maxCalls) {
  if (!Number.isFinite(maxCalls) || maxCalls <= 0) return "";
  if ((ledger?.calls || 0) < maxCalls) return "";
  return `stopped at the --max-calls ceiling of ${maxCalls} model call${maxCalls === 1 ? "" : "s"}. Nothing is known about whether the app works; raise the ceiling or run this test on its own.`;
}

/** `--max-calls 40`. Refused out loud rather than coerced, exactly like --retries: Number("lots")
 *  is NaN, and any silent fallback picks a ceiling the person did not ask for. */
export function parseMaxCalls(raw) {
  if (raw === undefined || raw === null || raw === "") return { value: 0, problem: "" };
  const n = Number(String(raw).trim());
  if (!Number.isInteger(n) || n < 0) {
    return { value: 0, problem: `--max-calls wants a whole number of model calls, got ${JSON.stringify(String(raw))}. Use 0 for no ceiling.` };
  }
  return { value: n, problem: "" };
}
