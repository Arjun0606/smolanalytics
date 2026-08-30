// WHY IT WAS FLAKY — the question a person answers by shrugging.
//
// A test goes red, somebody re-runs it, it goes green, they merge. Google's own published number
// says they are right to shrug about 84% of the time, and wrong the other 16% — and that 16% is
// where an intermittent bug lives until it reaches a customer.
//
// `flaky` today is an honest refusal to say "pass". This carries that honesty one step further: we
// are already holding both runs — every step, every target, every step's duration, and both
// verdicts — and the difference between "this test is unreliable" and "your app has a race" is very
// often sitting in that material. Nobody has to guess at it, so nothing here guesses.
//
// THE RULE, INHERITED FROM lib/suspect.mjs WITHOUT EXCEPTION: no diagnosis without named evidence
// from BOTH runs, and no confident wording when the two runs cannot be told apart. A wrong
// diagnosis here is worse than none — it either sends somebody hunting a race that does not exist,
// or tells them to ignore one that does. "I cannot tell these two runs apart" is a legitimate and
// useful thing to say, and it is what this returns whenever that is the truth.
//
// NO MODEL CALL. Everything below is arithmetic and set comparison over two records we already
// hold. A model could speculate more fluently and would be believed more than it deserves.

/** How much slower one number is than another, as a plain multiple. */
const times = (a, b) => (b > 0 ? a / b : 0);

/** A step's identity: what it did, to what. Not its index — a run that took one extra step would
 *  otherwise make every later step look different. */
const key = (s) => {
  const a = (s && s.action) || {};
  const t = (s && s.target) || {};
  return `${a.kind || "?"}:${t.role || ""}:${t.name || a.url || a.key || a.direction || ""}`;
};

/** Only the steps that worked. A failed attempt's dead ends are the agent hunting, not the app. */
const done = (att) => ((att && Array.isArray(att.steps) ? att.steps : []).filter((s) => s && s.ok));

/**
 * MATERIALLY SLOWER, and the two thresholds are both required on purpose.
 *
 * A ratio alone calls 8ms→40ms a five-fold slowdown, which is true and meaningless. An absolute
 * alone flags every step on a slow morning. Together they mean "long enough for a person to notice
 * and different enough not to be noise", which is the only version worth printing.
 */
const SLOWER_RATIO = 2.5;
const SLOWER_MS = 750;

/**
 * What actually differed between the failing run and the passing one.
 *
 * Returns facts, not conclusions. Every field is something a reader could verify by looking at the
 * two runs themselves, which is the property that makes the sentence built from it trustworthy.
 */
export function compare(first, last) {
  const a = done(first);
  const b = done(last);
  const aKeys = a.map(key);
  const bKeys = b.map(key);

  // Steps the passing run took that the failing one never reached, and vice versa.
  const onlyInPass = bKeys.filter((k) => !aKeys.includes(k));
  const onlyInFail = aKeys.filter((k) => !bKeys.includes(k));

  // The same step, materially slower in one run than the other. Matched by identity rather than
  // position so an extra step early on does not misalign everything after it.
  const slower = [];
  for (const s of b) {
    const k = key(s);
    const twin = a.find((x) => key(x) === k);
    if (!twin) continue;
    const ratio = times(s.ms, twin.ms);
    if (s.ms - twin.ms >= SLOWER_MS && ratio >= SLOWER_RATIO) {
      slower.push({ step: k, name: (s.target && s.target.name) || "", was: twin.ms, now: s.ms });
    }
  }

  const failMs = Number(first && first.ms) || 0;
  const passMs = Number(last && last.ms) || 0;
  // A slowdown spread evenly across every step shows up nowhere per-step and clearly here. Computed
  // before `indistinguishable` because it is one of the things that distinguishes them — the first
  // version of this file computed it after, so a run that took four times as long with no single
  // step standing out was reported as "the same in every way", which is the exact opposite of what
  // the evidence said.
  const runSlower = passMs - failMs >= SLOWER_MS && times(passMs, failMs) >= SLOWER_RATIO;

  return {
    steps: { fail: a.length, pass: b.length },
    onlyInPass,
    onlyInFail,
    slower,
    // The whole-run figures, which catch a slowdown spread across every step rather than one.
    ms: { fail: failMs, pass: passMs },
    runSlower,
    // Nothing distinguished them. Stated as its own fact so the caller cannot mistake an empty
    // comparison for a comparison that was never run.
    indistinguishable:
      !onlyInPass.length && !onlyInFail.length && !slower.length && !runSlower && a.length === b.length,
  };
}

const secs = (ms) => `${(ms / 1000).toFixed(1)}s`;

/**
 * One sentence, and only when the evidence supports one.
 *
 * Returns "" rather than a hedge when the two runs cannot be told apart: a reader who gets no
 * sentence learns something true, and a reader who gets a confident guess learns something false.
 */
export function diagnose(cmp) {
  if (!cmp || cmp.indistinguishable) return "";

  // Timing first, because it is the signal that actually distinguishes a racing app from an
  // unreliable test, and the one a person cannot easily see for themselves.
  if (cmp.slower.length) {
    const worst = cmp.slower.slice().sort((x, y) => y.now - y.was - (x.now - x.was))[0];
    const what = worst.name ? `"${worst.name}"` : worst.step;
    return (
      `The retry did the same thing and ${what} took ${secs(worst.was)} the first time and ${secs(worst.now)} the second. ` +
      `A step that slow on one run and not the other is usually the application waiting on something, not this test being unreliable.`
    );
  }

  if (cmp.runSlower) {
    return (
      `The retry took ${secs(cmp.ms.pass)} against ${secs(cmp.ms.fail)} for the run that failed, with no step standing out. ` +
      `That shape is the whole app being slower on one run — a cold start, a cold cache, or something contended — rather than one broken control.`
    );
  }

  if (cmp.onlyInPass.length) {
    return (
      `The retry reached ${cmp.onlyInPass.length} step${cmp.onlyInPass.length === 1 ? "" : "s"} the failing run never did, starting at ${cmp.onlyInPass[0]}. ` +
      `The first run stopped earlier, so the two runs did not test the same thing and the pass does not clear the failure.`
    );
  }

  if (cmp.onlyInFail.length) {
    return (
      `The failing run did ${cmp.onlyInFail.length} thing${cmp.onlyInFail.length === 1 ? "" : "s"} the retry did not, starting at ${cmp.onlyInFail[0]}. ` +
      `The agent took a different route the second time, so this is at least partly the test finding its own way rather than the app changing.`
    );
  }

  if (cmp.steps.fail !== cmp.steps.pass) {
    return `The two runs took a different number of steps (${cmp.steps.fail} then ${cmp.steps.pass}), so they did not do the same thing twice.`;
  }

  return "";
}

/**
 * The line printed under a flaky verdict, or nothing.
 *
 * When there is no diagnosis it says so explicitly rather than staying silent, because silence
 * reads as "we did not look" and the honest message is "we looked and could not tell".
 */
export function flakeNote(first, last) {
  const cmp = compare(first, last);
  const said = diagnose(cmp);
  if (said) return said;
  return "The two runs are indistinguishable from here: the same steps, in the same order, at the same speed. Nothing in this run says which of the app or the test is unreliable.";
}
