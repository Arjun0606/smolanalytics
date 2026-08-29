// WHAT THE UPLOAD CONTROL ACTUALLY IS, DECIDED IN BOUNDED TIME.
//
// lib/upload.mjs fabricates a file and hands it to a control. Deciding WHICH control it is holding
// turned out to be where three measured defects lived, all of them the same shape: our runner's own
// problem arriving at the agent as a sentence about the application under test.
//
//   1. setInputFiles does NOT accept a <label> or a wrapper.
//      lib/upload.mjs said, in a comment with no test behind it, that "three shapes count as
//      direct, because Playwright's setInputFiles accepts all three: the input itself, a <label>
//      whose control is one, and an element wrapping one." Measured on Playwright 1.52, Chromium,
//      Firefox and WebKit alike, two of those three throw:
//
//        <label role=button>Pick a doc<input type=file hidden></label>
//        <div role=button><input type=file hidden></div>
//            -> locator.setInputFiles: Error: Node is not an HTMLInputElement
//
//      Those two are the ordinary shape of every styled uploader on the web — the visible control
//      is a label or a div and the input is display:none behind it — and because the old probe
//      returned non-null for them, the file-chooser fallback that WOULD have carried the label was
//      never reached. The agent was handed "Node is not an HTMLInputElement" as its evidence and
//      asked to decide whether the customer's upload feature works.
//
//      So the probe now reports WHERE the input is, and the caller aims at the input rather than at
//      the thing that merely contains it. A label whose control lives elsewhere (`for=`) has no
//      descendant input and falls through to the click-and-catch-the-picker path, which is correct:
//      clicking a label opens the picker in all three engines.
//
//   2. The probe ignored the caller's timeout, so a control that is not there costs 40 seconds.
//      `locator.evaluate()` carries Playwright's own 30s default. Every other action in this runner
//      is capped at 10s. Measured, against a control that no longer exists — which is exactly what
//      a stale recording replays into — the old code spent 30s in the probe and then 10s in the
//      click, for 40.0s of dead time before an honest "stale". A fifty-test suite whose upload
//      controls were renamed pays that fifty times. The timeout the caller passed is now the
//      timeout the probe uses.
//
//   3. A DISABLED file input was uploaded to, and reported as done.
//      Playwright's setInputFiles skips the actionability checks that stop click() and fill() dead
//      on a disabled control. Measured: an input inside <fieldset disabled> accepted the file and
//      the page's own change handler fired. An app that disables its uploader until the terms are
//      accepted, or until the user is logged in, therefore got a green upload step for something no
//      user of it could ever do. That is a false green, so it is refused — and refused as a FAILED
//      step, in a sentence about the control, which the agent then judges. No status is decided
//      here.
//
// Nothing in this file chooses a verdict, an exit code or a status. It returns facts about a DOM
// node and one sentence, and lib/upload.mjs keeps its {ok, detail} contract exactly as it was.

/** How the file input relates to the element the agent named. */
export const VIA_INPUT = "input"; // the element IS the file input
export const VIA_DESCENDANT = "descendant"; // a file input is inside it (a label, or a styled wrapper)

/**
 * Read the file input at or inside `locator`, or null when there is none.
 *
 * Never throws and never waits longer than `timeout`: a control that is absent, detached mid-run or
 * inside a document that navigated must cost the caller its own timeout and not Playwright's
 * default, because the caller then still has a click to try and a sentence to write.
 */
export async function probeControl(locator, timeout = 10_000) {
  if (!locator || typeof locator.evaluate !== "function") return null;
  const ms = Number.isFinite(timeout) && timeout > 0 ? timeout : 10_000;
  const found = await locator
    .evaluate(
      (el) => {
        const isFile = (n) => !!n && n.tagName === "INPUT" && n.type === "file";
        let target = null;
        let via = "";
        if (isFile(el)) {
          target = el;
          via = "input";
        } else {
          // A <label> that wraps its control and a <div> that wraps a hidden input are the same
          // case here, and both are reachable by aiming at the input itself.
          const inner = typeof el.querySelector === "function" ? el.querySelector("input[type=file]") : null;
          if (isFile(inner)) {
            target = inner;
            via = "descendant";
          }
        }
        if (!target) return null;
        // `:disabled` and not `.disabled`: an input inside <fieldset disabled> reports
        // disabled === false on the IDL attribute while being unusable to every real user.
        let disabled = false;
        try {
          disabled = typeof target.matches === "function" && target.matches(":disabled");
        } catch {
          disabled = false;
        }
        return { accept: String(target.accept || ""), via, disabled: Boolean(disabled) };
      },
      undefined,
      { timeout: ms },
    )
    .catch(() => null);
  return found && typeof found === "object" ? found : null;
}

/**
 * The locator to hand the file to, given what the probe found.
 *
 * `.first()`, and the probe read `accept` off `querySelector("input[type=file]")` — the same first
 * input in document order — so the file that was fabricated and the input it lands in are the same
 * control even on a wrapper holding several.
 */
export function fileSink(locator, via) {
  return via === VIA_DESCENDANT ? locator.locator("input[type=file]").first() : locator;
}

/**
 * Why a disabled control was not uploaded to.
 *
 * Says the control is disabled and says who observed it, because the agent's next decision is
 * whether the APPLICATION is broken, and "the runner refused" and "the app rejected the file" are
 * different findings. No status, no exit code: this is a failed step, and the agent judges it.
 */
export function disabledDetail() {
  return (
    "the file input is disabled, so nothing was attached — a real user could not upload here either. " +
    "If something on the page is supposed to enable it first, do that and try again."
  );
}
