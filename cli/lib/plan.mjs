// `npx smolanalytics plan check` — fail CI when an event your tracking plan expects stops firing.
//
// The gate existed only as a Go-binary subcommand, while the site told hosted customers to run it in
// their pipeline. A hosted customer has the npm package and nothing else, so the most-quoted CI claim
// on the site named a command they could not run. Rather than retreat the claim, this makes it true.
//
// It is a thin client on purpose: the Go binary's `plan check` calls the instrumentation_health MCP
// tool and gates on the result, so this calls the same tool over the same HTTP transport and applies
// the same exit-code contract. One computation, one verdict, two front doors — the report can never
// say healthy in CI and broken in the editor.

const RPC_ID = 1;

// callTool issues one MCP tools/call and returns the tool's text payload.
export async function callTool(endpoint, key, name, args = {}, fetchImpl = fetch) {
  const res = await fetchImpl(endpoint, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
      ...(key ? { Authorization: `Bearer ${key}` } : {}),
    },
    body: JSON.stringify({ jsonrpc: "2.0", id: RPC_ID, method: "tools/call", params: { name, arguments: args } }),
  });
  const raw = await res.text();
  if (!res.ok) throw new Error(`${res.status} from ${endpoint}: ${raw.slice(0, 200)}`);
  return parseToolText(raw);
}

// parseToolText handles both plain JSON-RPC and the SSE framing an MCP server may reply with.
export function parseToolText(raw) {
  let body = raw.trim();
  if (body.startsWith("event:") || body.startsWith("data:")) {
    const line = body.split("\n").find((l) => l.startsWith("data: "));
    if (!line) throw new Error("empty SSE response");
    body = line.slice(6);
  }
  const msg = JSON.parse(body);
  if (msg.error) throw new Error(msg.error.message || "tool call failed");
  const content = msg.result?.content;
  const text = Array.isArray(content) ? content.map((c) => c.text || "").join("") : "";
  if (!text) throw new Error("tool returned no content");
  // An MCP tool signals a refusal with isError plus a human sentence, not a JSON payload. Surface
  // that sentence: the server already knows exactly what is wrong ("no tracking plan declared yet"),
  // and turning it into "unreadable payload" throws away the one actionable thing in the response.
  if (msg.result?.isError) throw Object.assign(new Error(text), { refusal: true });
  return text;
}

// gate renders the health report and decides the exit code. Same contract as the binary: any planned
// event that is missing or partial fails the build. Unplanned events are reported, never fatal —
// tracking something you did not write down is untidy, not broken, and failing on it would train
// people to delete the plan.
export function gate(payload, log = console.log) {
  let h;
  try {
    h = JSON.parse(payload);
  } catch {
    log("unreadable health payload from the server");
    return 1;
  }
  const planned = h.planned || [];
  if (planned.length === 0) {
    log("no tracking plan on this instance. Push one first: set_tracking_plan over MCP, or `smolanalytics plan push`.");
    return 1;
  }
  let broken = 0;
  for (const p of planned) {
    const ok = p.status === "ok" || p.status === "healthy";
    const miss = (p.missing_properties || []).length;
    if (!ok || miss > 0) broken++;
    const mark = ok && miss === 0 ? "ok  " : "FAIL";
    let line = `  ${mark} ${p.event}  ${p.count ?? 0} seen`;
    if (p.last_seen) line += `, last ${p.last_seen}`;
    if (miss > 0) line += `  missing properties: ${p.missing_properties.join(", ")}`;
    else if (!ok) line += `  (${p.status})`;
    log(line);
  }
  const unplanned = h.unplanned_events || [];
  if (unplanned.length > 0) {
    log(`\n  ${unplanned.length} event(s) firing but not in the plan: ${unplanned.slice(0, 8).join(", ")}${unplanned.length > 8 ? " …" : ""}`);
  }
  if (broken > 0) {
    log(`\n${broken} of ${planned.length} planned events are broken. Failing the build.`);
    return 1;
  }
  log(`\nAll ${planned.length} planned events are firing.`);
  return 0;
}

export function endpointFor(url) {
  const base = (url || "").trim().replace(/\/+$/, "");
  if (!base) return "https://smolanalytics.com/api/mcp";
  return base.endsWith("/mcp") ? base : base + "/mcp";
}

export async function planCheckCmd({ url, key, project, windowHours, log = console.log, fetchImpl = fetch }) {
  if (!key) {
    log("");
    log("plan check needs a key:");
    log("  npx smolanalytics plan check --key sa_... --url https://YOUR-INSTANCE");
    log("  npx smolanalytics plan check --key sa_org_... --project my-app     (cloud org token)");
    log("");
    log("In CI, put it in SMOLANALYTICS_KEY and it is picked up automatically.");
    return 1;
  }
  const args = {};
  if (windowHours > 0) args.window_hours = windowHours;
  if (project) args.project = project;
  try {
    const text = await callTool(endpointFor(url), key, "instrumentation_health", args, fetchImpl);
    return gate(text, log);
  } catch (err) {
    // Both cases fail the build, but they are different problems and the message must not confuse
    // them: a refusal means we reached the server and it told us what is wrong, while anything else
    // means the pipe is broken. Reporting "could not reach your instance" for a refusal sends the
    // reader to check DNS when the answer was in the response.
    log(err.refusal ? `plan check: ${err.message}` : `plan check could not reach your instance: ${err.message}`);
    return 1;
  }
}
