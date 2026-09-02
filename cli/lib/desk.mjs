// `npx smolanalytics desk` — the whole desk, in the terminal.
//
// The dashboard was the only place you could see what the product had found, which meant the answer
// lived in a browser tab while the work lives in a terminal. This prints the same composition the web
// desk renders, from the same endpoint (/v1/investigate composes it once so the two cannot drift),
// so the loop closes without leaving the shell.
//
// It reads. It never writes, never opens a browser, never asks for anything but a read key. A command
// that surprises you the first time you run it does not get run twice.

import { callTool, endpointFor } from "./plan.mjs";
import { redact } from "./auth.mjs";

const C = {
  b: (s) => `\x1b[1m${s}\x1b[0m`,
  dim: (s) => `\x1b[2m${s}\x1b[0m`,
  y: (s) => `\x1b[33m${s}\x1b[0m`,
  g: (s) => `\x1b[32m${s}\x1b[0m`,
  r: (s) => `\x1b[31m${s}\x1b[0m`,
};

// The chip decides the colour, exactly as the web desk decides it, so someone reading both does not
// have to learn two vocabularies.
function chip(f) {
  if (f.recovered && f.acted_at && !String(f.acted_at).startsWith("0001")) return C.g("verified ");
  if (f.recovered) return C.g("recovered");
  if (f.acted_at && !String(f.acted_at).startsWith("0001")) return C.dim("acted    ");
  if (f.needs_you) return C.y("needs you");
  return C.dim("watch    ");
}

export function render(doc, log = console.log) {
  const inv = doc.investigation || doc;
  const led = doc.ledger || {};
  const findings = inv.findings || [];

  log("");
  const armed = led.armed_count ?? 0;
  const acts = led.acted_count ?? 0;
  log(
    C.dim("the ledger · ") +
      C.b(String(armed)) +
      C.dim(` standing order${armed === 1 ? "" : "s"} · `) +
      C.b(String(acts)) +
      C.dim(` act${acts === 1 ? "" : "s"}`),
  );

  if (findings.length === 0) {
    // Silence is a real answer and the desk says so rather than showing an empty box. What it must
    // NOT do is imply nothing is being watched.
    log("");
    log("Nothing needs you.");
    const scanned = inv.scanned || [];
    if (scanned.length) {
      log(C.dim(`${scanned.length} metric${scanned.length === 1 ? "" : "s"} swept: ${scanned.slice(0, 6).join(", ")}${scanned.length > 6 ? ", …" : ""}`));
    }
  } else {
    log("");
    for (const f of findings) {
      log(`  ${chip(f)}  ${C.b(f.headline || "")}`);
      const size = (f.cost && (f.cost.size_text || f.cost.SizeText)) || "";
      if (size) log(`             ${C.dim(size)}`);
      if (f.cause) log(`             ${C.dim(f.cause)}`);
      if (f.next_move) log(`             ${f.next_move}`);
      log("");
    }
  }

  // Standing orders: what is armed, and what it would take to arm the rest. This is the part that
  // makes an empty desk worth reading, and it is the same list the web desk shows.
  const standing = led.standing || [];
  const armedRows = standing.filter((w) => w.armed);
  const coldRows = standing.filter((w) => !w.armed);
  if (armedRows.length) {
    log(C.b("armed"));
    for (const w of armedRows) log(`  ${C.g("•")} ${w.subject}${w.trigger ? C.dim(" — " + w.trigger) : ""}`);
    log("");
  }
  if (coldRows.length) {
    log(C.b("not armed"));
    for (const w of coldRows) log(`  ${C.dim("•")} ${C.dim(w.subject + (w.trigger ? " — " + w.trigger : ""))}`);
    log("");
  }
  return 0;
}

export async function deskCmd({ url, key, project, log = console.log, fetchImpl = fetch }) {
  if (!key) {
    log("");
    log("desk needs a read key:");
    log("  npx smolanalytics desk --key sa_... --url https://YOUR-INSTANCE");
    log("  npx smolanalytics desk --key sa_org_... --project my-app     (cloud org token)");
    log("");
    // 2, NOT 1. This CLI's exit codes carry one meaning across every command: 1 says a test failed,
    // which is a statement about the CUSTOMER'S application, and 2 says this runner could not
    // finish. The README sells that split — "a pipeline that gates on 1 alone never reddens a build
    // because our side had an outage" — and a missing key reddening a build on 1 is exactly the
    // promise it breaks.
    return 2;
  }
  const args = {};
  if (project) args.project = project;
  try {
    // Through the MCP tool rather than the HTTP route, because that is the transport `connect`
    // already wires and the one an org token is scoped for.
    const text = await callTool(endpointFor(url), key, "investigate", args, fetchImpl);
    let doc;
    try {
      doc = JSON.parse(text);
    } catch {
      log(text);
      return 0;
    }
    return render(doc, log);
  } catch (err) {
    // THE KEY MUST NOT SURVIVE INTO THE MESSAGE.
    //
    // Measured while writing this file's first test: an error whose text happened to echo the
    // request — "fetch failed for https://…?key=sa_readkey_…" — was printed verbatim. We send the
    // key as a header, but we do not control what an error carries: a proxy, a redirect, a DNS
    // layer or a future refactor can each put it back in a URL, and the place it lands is a CI log,
    // which is forever. So the failure path redacts rather than trusting the shape of somebody
    // else's error string.
    // `reached` is an HTTP status: the host answered, so "could not reach your instance" would
    // send the reader to check DNS for an answer that was in the response (lib/plan.mjs).
    const why = err && (err.refusal || err.reached) ? `desk: ${err.message}` : `desk could not reach your instance: ${err && err.message}`;
    log(redact(why, [key]));
    // Same contract as above: an unreachable instance is our side of the fence, never a verdict
    // about anybody's product.
    return 2;
  }
}
