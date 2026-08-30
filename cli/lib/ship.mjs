// THE SHIP VERDICT — the only question anybody actually asks, and the one nobody answers.
//
// Every tool in this category answers "did the tests pass". The question a person asks on a Friday
// is "can we ship". Those are not the same question, and the gap between them is exactly the work
// this product has not been doing.
//
// WHY NOBODY ELSE PRINTS THIS, AND WHY WE CAN. Answering honestly means listing what you did NOT
// check, and a vendor whose price depends on looking comprehensive will never publish that list.
// Our identity is already built on publishing it: `stale` refuses to claim a pass, `flaky` refuses
// to be counted as one, `errored` says the fault is ours, and `--since` names every test it skipped
// because "a run that quietly checked twelve of fifty tests and printed '12 passed' is a suite lying
// about itself". This is that same rule applied to the release rather than the run.
//
// THE FUNCTION THIS ABSORBS. Research in research/THE_JOB.md scored fourteen recurring QA tasks by
// (pain x frequency) / how well any tool handles them. "Reporting are we safe to ship" scored 10.0
// — joint highest — while "writing the test" scored 1.8 and "running it" 2.0. The two things this
// whole category advertises are the two least valuable rows in the inventory. The corroboration is
// the pricing: QA Wolf ("Investigate every failure", "24-hour investigation") and Bug0
// ("Human-reviewed. Every failure.") charge the most in the field, and neither puts a human on
// writing tests. Both put a human on judging results.
//
// EVERYTHING HERE IS COMPUTED FROM FACTS THE RUN ALREADY HELD. No model call, nothing inferred, no
// score out of ten. A percentage would be the easy thing to print and the wrong one: "94% passed"
// is a number a person cannot act on, and it hides the four percent that matter.

/** A count that reads as English: 1 test, 2 tests. */
const n = (count, word) => `${count} ${word}${count === 1 ? "" : "s"}`;

/**
 * WHAT WAS NOT CHECKED, which is the half of the answer nobody publishes.
 *
 * Each entry is a distinct reason a test's silence proves nothing, kept apart because the reader's
 * next action differs for each. A stale recording needs re-deriving; a skipped test needs the
 * change re-examined; a flaky one needs a decision; a run that errored is ours to fix. Collapsing
 * them into "8 not verified" would be true and useless.
 */
export function unchecked(results = [], selection = null) {
  const by = (s) => results.filter((r) => r && r.status === s);
  const out = [];

  // A FAILURE IS NOT AN UNCHECKED THING. It was checked, and it failed — which is a different
  // sentence with a different next action, and printing it under "what was not checked" was the
  // first bug this file had. It belongs in `broken()`, above this list.
  const flaky = by("flaky");
  if (flaky.length) {
    out.push({
      kind: "flaky",
      count: flaky.length,
      tests: flaky.map((r) => r.name),
      // A retry that passed proves the run can pass, not that the behaviour is sound. Saying so is
      // the whole reason `flaky` exists as its own status rather than being folded into passed.
      why: `${n(flaky.length, "test")} failed and then passed on a retry, so ${flaky.length === 1 ? "it proves" : "they prove"} nothing either way. Nothing about the app changed in between.`,
    });
  }

  const stale = by("stale");
  if (stale.length) {
    out.push({
      kind: "stale",
      count: stale.length,
      tests: stale.map((r) => r.name),
      why: `${n(stale.length, "recording")} stopped fitting the app and ${stale.length === 1 ? "was" : "were"} not re-derived, so ${stale.length === 1 ? "that flow is" : "those flows are"} unverified. A replay cannot tell a rename from a removal.`,
    });
  }

  const errored = by("errored");
  if (errored.length) {
    out.push({
      kind: "errored",
      count: errored.length,
      tests: errored.map((r) => r.name),
      why: `${n(errored.length, "test")} could not run at all. That is this runner, not your application — and it means ${errored.length === 1 ? "that flow was" : "those flows were"} not checked.`,
    });
  }

  const skipped = selection && selection.used && Array.isArray(selection.skipped) ? selection.skipped : [];
  if (skipped.length) {
    out.push({
      kind: "skipped",
      count: skipped.length,
      tests: skipped.map((t) => (typeof t === "string" ? t : t && t.name)).filter(Boolean),
      why: `${n(skipped.length, "test")} did not run: this change touches no file ${skipped.length === 1 ? "it exercises" : "they exercise"} (--since ${selection.since}). That is a judgement about the diff, not a result about the app.`,
    });
  }

  return out;
}

/**
 * WHAT IS BROKEN — checked, and it failed. Kept apart from `unchecked` because the two demand
 * different things of the reader: a failure is a bug to fix, a gap is a question still open.
 */
export function broken(results = []) {
  const failed = results.filter((r) => r && r.status === "failed");
  if (!failed.length) return null;
  return {
    count: failed.length,
    tests: failed.map((r) => r.name),
    why: `${n(failed.length, "test")} did not do what the sentence describes. This is your application, and it is the reason not to ship.`,
  };
}

/**
 * The verdict, and it is deliberately one of four words rather than a number.
 *
 *   no      something failed. There is a known bug in what you are about to ship.
 *   partly  nothing failed, but something was not checked and the gap is material.
 *   yes     everything in the suite ran and passed. Still bounded by what the suite covers.
 *   unknown the run did not produce a usable result at all.
 *
 * `yes` is deliberately hard to reach and never means "safe". It means the suite has nothing
 * against you, which is a smaller claim, and the sentence that follows it says so.
 */
export function verdict(results = [], selection = null) {
  const usable = results.filter((r) => r && typeof r.status === "string");
  if (!usable.length) return "unknown";
  const has = (s) => usable.some((r) => r.status === s);
  if (has("failed")) return "no";
  const gaps = unchecked(usable, selection);
  if (gaps.length) return "partly";
  return "yes";
}

const HEADLINE = {
  no: "Do not ship this.",
  partly: "Nothing failed, and that is not the same as nothing being wrong.",
  yes: "Nothing the suite covers is broken.",
  unknown: "This run produced no usable verdict.",
};

/**
 * The artefact: what ran, what did not, and one sentence a person can act on.
 *
 * Ordered failures first, because the reader has a limited number of lines of attention and the
 * thing that stops a release belongs at the top of them.
 */
export function shipReport(results = [], { selection = null, suite = "tests", url = "" } = {}) {
  const usable = results.filter((r) => r && typeof r.status === "string");
  const v = verdict(usable, selection);
  const passed = usable.filter((r) => r.status === "passed").length;
  const gaps = unchecked(usable, selection);

  const lines = [HEADLINE[v]];

  if (v === "unknown") {
    lines.push("No test produced a status, so there is nothing here to reason about.");
    return { verdict: v, headline: HEADLINE[v], checked: 0, gaps: [], lines };
  }

  lines.push(
    `${n(passed, "test")} of ${usable.length} ran and passed against ${url || "the app"}.`,
  );

  const bad = broken(usable);
  if (bad) {
    lines.push("What is broken:");
    lines.push(`  ${bad.why}`);
    for (const t of bad.tests) lines.push(`    ${t}`);
  }

  if (!gaps.length) {
    // The honest ceiling on a clean run. A suite proves things about the flows somebody wrote down,
    // and saying so is what keeps a green tick from being read as a guarantee.
    lines.push(`Everything in ${suite} was checked. That is a statement about the flows in ${suite}, and nothing about the ones nobody has written yet.`);
  } else {
    lines.push("What was NOT checked:");
    for (const g of gaps) lines.push(`  ${g.why}`);
  }

  return { verdict: v, headline: HEADLINE[v], checked: passed, total: usable.length, broken: bad, gaps, lines };
}

/** The same thing as one block of text, for a terminal or a pull request comment. */
export function shipText(results = [], opts = {}) {
  return shipReport(results, opts).lines.join("\n");
}
