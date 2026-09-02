// The shipped templates are the product's front door: a customer copies github-action.yml into
// .github/workflows/ and never reads it again. A typo there is not a broken test, it is a workflow
// that does not start, on somebody else's repository, on the day they were deciding whether to
// trust this. So the file is parsed and its contents asserted, here, on every run.
//
// The parser below is deliberately small and knows only the YAML that Actions workflows are written
// in: block mappings, block sequences, block scalars, comments. It is not a YAML implementation and
// is not exported anywhere near lib/ — its job is to fail loudly if the template stops being
// well-formed, and to let the assertions ask real questions about the tree.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseSuite } from "../lib/suite.mjs";

const dir = path.dirname(fileURLToPath(import.meta.url));
const templates = path.join(dir, "..", "templates");
const workflowPath = path.join(templates, "github-action.yml");
const workflow = readFileSync(workflowPath, "utf8");
const exampleTest = readFileSync(path.join(templates, "example-test.md"), "utf8");
const readme = readFileSync(path.join(dir, "..", "README.md"), "utf8");

// The workflow's comments, as the reader reads them: `#` gone and the wrapping undone, so a
// sentence that spans three commented lines is one sentence to match against. Asserting on the raw
// file would mean every reflow silently retires the assertion.
const prose = workflow.split(/\r?\n/).map((l) => l.replace(/^\s*#\s?/, "")).join(" ").replace(/\s+/g, " ");

/** Drop a trailing `# comment`, without touching a `#` inside a quoted string. */
function stripComment(line) {
  let quote = null;
  let out = "";
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (quote) {
      out += c;
      if (c === quote) quote = null;
      continue;
    }
    if (c === '"' || c === "'") quote = c;
    else if (c === "#" && (i === 0 || /\s/.test(line[i - 1]))) break;
    out += c;
  }
  return out.replace(/\s+$/, "");
}

function scalar(v) {
  const s = v.replace(/^["']|["']$/g, "");
  if (s === "true") return true;
  if (s === "false") return false;
  if (/^-?\d+$/.test(s)) return Number(s);
  return s;
}

function parseYaml(text) {
  const lines = [];
  text.split(/\r?\n/).forEach((raw, i) => {
    const l = stripComment(raw);
    if (l.trim()) lines.push({ n: i + 1, indent: l.length - l.trimStart().length, text: l.trim() });
  });
  let pos = 0;

  const blockScalar = (indent) => {
    const parts = [];
    while (pos < lines.length && lines[pos].indent > indent) parts.push(lines[pos++].text);
    return parts.join("\n");
  };

  function node(indent) {
    return lines[pos] && /^-(\s|$)/.test(lines[pos].text) ? seq(indent) : map(indent);
  }

  function map(indent) {
    const obj = {};
    while (pos < lines.length) {
      const l = lines[pos];
      if (l.indent < indent) break;
      assert.equal(l.indent, indent, `${workflowPath}:${l.n}: indented ${l.indent}, expected ${indent} — ${l.text}`);
      const m = /^([^:]+):(?:\s+(.*))?$/.exec(l.text);
      assert.ok(m, `${workflowPath}:${l.n}: not a key: ${l.text}`);
      const key = m[1].trim().replace(/^["']|["']$/g, "");
      const val = (m[2] || "").trim();
      pos++;
      if (/^[|>][-+]?$/.test(val)) obj[key] = blockScalar(indent);
      else if (val) obj[key] = scalar(val);
      else {
        const next = lines[pos];
        if (next && next.indent > indent) obj[key] = node(next.indent);
        else if (next && next.indent === indent && /^-(\s|$)/.test(next.text)) obj[key] = seq(indent);
        else obj[key] = "";
      }
    }
    return obj;
  }

  function seq(indent) {
    const arr = [];
    while (pos < lines.length) {
      const l = lines[pos];
      if (l.indent < indent || !/^-(\s|$)/.test(l.text)) break;
      assert.equal(l.indent, indent, `${workflowPath}:${l.n}: list item indented ${l.indent}, expected ${indent}`);
      const rest = l.text.replace(/^-\s*/, "");
      if (!rest) {
        pos++;
        const next = lines[pos];
        arr.push(next && next.indent > indent ? node(next.indent) : null);
        continue;
      }
      // `- uses: x` is a mapping that starts on the dash line; its siblings sit under the dash.
      const childIndent = l.indent + (l.text.length - rest.length);
      lines[pos] = { n: l.n, indent: childIndent, text: rest };
      arr.push(rest.includes(":") ? map(childIndent) : scalar(rest));
    }
    return arr;
  }

  const doc = node(lines.length ? lines[0].indent : 0);
  assert.equal(pos, lines.length, `${workflowPath}: stopped parsing at line ${lines[pos]?.n}`);
  return doc;
}

const wf = parseYaml(workflow);
const job = wf.jobs?.e2e;
const steps = job?.steps || [];
const runStep = steps.find((s) => typeof s?.run === "string" && s.run.includes("smolanalytics"));

describe("github-action.yml is well-formed", () => {
  test("no tabs", () => {
    // A tab in a YAML file is a parse error in Actions, and it is invisible in a diff.
    const bad = workflow.split("\n").map((l, i) => (l.includes("\t") ? i + 1 : 0)).filter(Boolean);
    assert.deepEqual(bad, [], `tabs on line(s) ${bad.join(", ")}`);
  });

  test("every indent is a multiple of two, and no line has trailing whitespace", () => {
    workflow.split("\n").forEach((l, i) => {
      if (!l.trim()) return;
      const indent = l.length - l.trimStart().length;
      assert.equal(indent % 2, 0, `line ${i + 1} is indented ${indent}`);
      assert.equal(l, l.replace(/\s+$/, ""), `line ${i + 1} has trailing whitespace`);
    });
  });

  test("it parses, and the tree has the keys Actions requires", () => {
    assert.equal(wf.name, "e2e");
    // `on:` — quoted here because a YAML 1.1 parser reads a bare `on` as the boolean true, and the
    // key must survive whichever parser reads this file.
    assert.ok(Object.prototype.hasOwnProperty.call(wf, "on") || Object.prototype.hasOwnProperty.call(wf, "true"), "no trigger");
    const on = wf.on ?? wf.true;
    assert.ok(Object.prototype.hasOwnProperty.call(on, "pull_request"), "must trigger on pull_request");
    assert.ok(job, "a job named e2e");
    assert.equal(job["runs-on"], "ubuntu-latest");
    assert.ok(steps.length >= 3, "checkout, node, and a run step at minimum");
  });
});

describe("the workflow does what the README promises", () => {
  test("exactly the two permissions it needs, and nothing more", () => {
    assert.deepEqual(wf.permissions, { contents: "read", "pull-requests": "write" });
  });

  test("the run line is the documented one", () => {
    const line = 'npx smolanalytics@latest test --suite tests/ --url "$URL" --comment';
    assert.ok(runStep, "no step runs smolanalytics");
    assert.equal(runStep.run.trim(), line);
    // Drift guard: the README teaches this exact line. Two copies of a command is one copy too
    // many, so if they ever disagree this fails rather than shipping instructions that do not run.
    assert.ok(readme.includes(line), "README no longer shows the same command");
  });

  test("the URL is passed as an environment variable, never interpolated into the shell", () => {
    assert.match(String(runStep.env.URL), /steps\.preview\.outputs\.url/);
    assert.ok(!runStep.run.includes("${{"), "a value from another action must not be pasted into a shell command");
  });

  test("both secrets reach the step", () => {
    assert.match(String(runStep.env.ANTHROPIC_API_KEY), /secrets\.ANTHROPIC_API_KEY/);
    assert.match(String(runStep.env.GITHUB_TOKEN), /secrets\.GITHUB_TOKEN/);
  });

  test("it does not block a merge on day one", () => {
    assert.equal(runStep["continue-on-error"], true);
  });

  test("the recordings are cached, or the economic argument evaporates", () => {
    const restore = steps.find((s) => String(s?.uses || "").startsWith("actions/cache/restore@"));
    assert.ok(restore, "nothing restores the recordings: every CI run would be a fresh agent run");
    assert.equal(restore.with.path, ".smolanalytics/recordings", "must cache the directory the CLI writes recordings to");
    assert.match(String(restore.with["restore-keys"]), /smolanalytics-recordings-/, "without restore-keys the cache never hits");
  });

  // THE COMMENTS ARE THE DOCUMENTATION. A stranger copies this file once and never opens it again,
  // so a sentence in it that is not true is worse than a missing one: it is the answer they will
  // trust while they debug the wrong thing.
  //
  // MEASURED against how Actions actually scopes a cache — to the branch that wrote it, readable
  // only from that branch and the default branch. This workflow triggers on `pull_request` and
  // nothing else, so no run ever writes a cache on the default branch and pull request #2 cannot
  // see what #1 recorded. The file used to promise the opposite in as many words: "a pull request
  // falls back to the cache from the default branch." Every new pull request starts cold, the
  // comment says so on every run, and the reader had been told in advance that meant these steps
  // were broken.
  test("it does not promise a cache fallback its own triggers make impossible", () => {
    const triggers = typeof wf.on === "string" ? [wf.on] : Object.keys(wf.on ?? wf.true ?? {});
    // The guard is conditional on the shape that causes it, so adding a default-branch trigger
    // later legitimately retires this rather than leaving a stale rule to be worked around.
    if (triggers.some((t) => t === "push" || t === "schedule" || t === "workflow_dispatch")) return;
    assert.ok(!/falls back to the cache from the default branch/i.test(prose),
      "the template promises a default-branch cache that nothing in it ever writes");
    assert.match(prose, /first run of every new pull request is a full agent run/i,
      "the reader is never told that a new pull request starts cold, so the first comment reads as a broken cache");
  });

  test("the comment does not send the reader to debug a cache that is working", () => {
    // The same file tells the reader that a comment which keeps saying nothing replayed means the
    // cache steps are not doing their job. Two things make that false with a perfect cache: a
    // brand-new pull request, and a test that passes by only reading a page — it performs no step,
    // so compile() records nothing for it and it wakes the agent on every run, forever.
    assert.match(prose, /only reading a page/i,
      "a test that can never be recorded is not named, so its cost reads as a caching fault");
  });

  test("the recordings are saved even when a test fails", () => {
    // The all-in-one actions/cache saves in a post step declared `post-if: success()`. With
    // continue-on-error deleted — which this file tells the reader to do — a run with one failing
    // test would drop every recording the agent repaired in it, and that is the run that repaired
    // the most. The cache would then quietly stop paying for itself.
    const save = steps.find((s) => String(s?.uses || "").startsWith("actions/cache/save@"));
    assert.ok(save, "nothing saves the recordings");
    assert.equal(save.if, "always()", "a save that only runs on success loses exactly the runs worth saving");
    assert.equal(save.with.path, ".smolanalytics/recordings");
    const all = steps.map((s) => String(s?.uses || s?.name || ""));
    assert.ok(all.indexOf("actions/cache/save@v4") > all.findIndex((u) => u === "e2e" || u.includes("cache/restore")), "the save has to come after the run");
    assert.ok(!steps.some((s) => /^actions\/cache@/.test(String(s?.uses || ""))), "the all-in-one cache action is the one that does not save on failure");
  });

  test("the failure evidence is uploaded, because a screenshot on a recycled runner is not evidence", () => {
    const up = steps.find((s) => String(s?.uses || "").startsWith("actions/upload-artifact@"));
    assert.ok(up, "nothing uploads .smolanalytics/evidence: the screenshots die with the runner");
    assert.equal(up.with.path, ".smolanalytics/evidence", "must upload the directory the CLI writes evidence to");
    // With continue-on-error deleted — which this file tells the reader to do — a default-condition
    // upload is skipped on exactly the runs that produced evidence. Same trap as the cache save.
    assert.equal(up.if, "always()");
    assert.equal(up.with["if-no-files-found"], "ignore", "a green run writes no evidence, and that is not a warning");
  });

  test("it skips the pull requests that cannot be tested, rather than failing them", () => {
    // Both of these run with an empty ANTHROPIC_API_KEY and a read-only GITHUB_TOKEN, so every test
    // would error and then the comment would 403. A tool that red-Xs every outside contribution and
    // every dependency bump is a tool somebody deletes on the Friday.
    const cond = String(job.if || "").replace(/\s+/g, " ");
    assert.match(cond, /head\.repo\.full_name == github\.repository/, "a fork's pull request gets no secrets");
    assert.match(cond, /dependabot\[bot\]/, "Actions withholds repository secrets from dependabot");
  });

  test("a hung browser cannot bill six hours of somebody's minutes", () => {
    assert.ok(Number.isInteger(job["timeout-minutes"]), "no job timeout: the Actions default is six hours");
    assert.ok(job["timeout-minutes"] <= 60, `${job["timeout-minutes"]} minutes is not a safeguard`);
  });

  test("every action is pinned", () => {
    for (const s of steps) {
      if (!s?.uses) continue;
      assert.match(s.uses, /@v\d/, `${s.uses} is not pinned to a version`);
    }
  });

  test("the three ways to get a URL all feed the same step id", () => {
    // All three ship commented out. They are still checked, because a customer uncommenting one of
    // them must not have to fix an id to make the run step work.
    const offered = workflow.match(/^\s*#?\s*id: preview$/gm) || [];
    assert.equal(offered.length, 3, "three preview options, one id");
  });

  test("the shipped default asks nobody where their preview is", () => {
    // WALKED: with no --url inside Actions on a pull request, the CLI asks the deployments API for
    // this pull request's own preview, and it worked end to end against a real deployments API.
    // Shipping a preview step ON meant the one required decision in the whole install was one we
    // could make ourselves — pick your host, wire its action, keep the id agreeing.
    assert.ok(!steps.some((s) => s?.id === "preview"),
      "a preview step is enabled by default, so the install asks for a decision the CLI can make itself");
    // And uncommenting one must stay a ONE-line edit: the env line and the flag both stay put,
    // because `steps.preview.outputs.url` resolves to the empty string when no such step ran and an
    // empty --url is read as no --url. Verified by running it.
    assert.match(String(runStep.env.URL), /steps\.preview\.outputs\.url/,
      "the URL plumbing was removed, so turning a preview step back on is now two edits, not one");
    assert.match(runStep.run, /--url "\$URL"/);
  });

  test("nothing third-party runs inside a job that can write to pull requests", () => {
    // This job holds `pull-requests: write`. Every action it runs by default is one of GitHub's
    // own; the host-specific wait action is offered, commented, with that trade-off written beside
    // it. A default that hands a random action write access is a default nobody audited.
    const foreign = steps.map((s) => String(s?.uses || "")).filter((u) => u && !u.startsWith("actions/"));
    assert.deepEqual(foreign, [], `third-party action(s) enabled by default: ${foreign.join(", ")}`);
  });

  test("it points at a starting set of tests the reader can actually produce", () => {
    // `see example-test.md` is a path inside an npm package a reader copying one YAML file off the
    // web has not checked out. `suggest` is a command they can run right now.
    assert.match(workflow, /smolanalytics suggest/,
      "the front-door file names no way to get from an empty repository to a tests/ folder");
  });
});

// The README carries a shortened copy of the same workflow. Two copies of a workflow is one copy
// too many: the run line already has a drift guard, and so does everything else a reader would be
// burned by if only one of the two files was updated.
describe("the README's copy of the workflow is the same workflow", () => {
  const block = /```yaml\n([\s\S]*?)```/.exec(readme);
  const doc = block ? parseYaml(block[1]) : null;
  const rsteps = doc?.jobs?.e2e?.steps || [];

  // `on: pull_request` and `on:\n  pull_request:` are the same trigger; the README uses the short
  // one because it is a starter block, and comparing them has to know that.
  const triggers = (d) => {
    const on = d.on ?? d.true ?? {};
    return typeof on === "string" ? [on] : Object.keys(on);
  };

  test("it parses, and triggers and permits the same things", () => {
    assert.ok(doc, "the README no longer shows a workflow at all");
    assert.deepEqual(doc.permissions, wf.permissions);
    assert.deepEqual(triggers(doc), triggers(wf));
  });

  test("it skips the same pull requests and stops running at the same point", () => {
    const job2 = doc.jobs.e2e;
    assert.equal(String(job2.if || "").replace(/\s+/g, " "), String(job.if || "").replace(/\s+/g, " "));
    assert.equal(job2["timeout-minutes"], job["timeout-minutes"]);
  });

  test("it uploads the failure evidence the same way", () => {
    const up = rsteps.find((s) => String(s?.uses || "").startsWith("actions/upload-artifact@"));
    assert.ok(up, "the README's workflow leaves failure evidence to die with the runner");
    assert.equal(up.with.path, ".smolanalytics/evidence");
    assert.equal(up.if, "always()");
  });

  test("it restores and saves the recordings the same way", () => {
    const pick = (list, prefix) => list.find((s) => String(s?.uses || "").startsWith(prefix));
    for (const prefix of ["actions/cache/restore@", "actions/cache/save@"]) {
      const mine = pick(rsteps, prefix);
      const theirs = pick(steps, prefix);
      assert.ok(mine, `the README's workflow has no ${prefix} step`);
      assert.equal(mine.with.path, theirs.with.path);
      assert.equal(String(mine.with.key), String(theirs.with.key));
    }
    assert.equal(pick(rsteps, "actions/cache/save@").if, "always()");
  });
});

describe("example-test.md", () => {
  const tests = parseSuite("templates/example-test.md", exampleTest);

  test("our own parser finds the tests in our own example", () => {
    assert.ok(tests.length >= 3, `found ${tests.length}`);
    for (const t of tests) {
      assert.ok(t.test.length > 30, `"${t.name}" has no sentence under it`);
      assert.ok(!t.test.includes("<!--"), "the instructions to the reader leaked into a test");
    }
  });

  test("the title is not itself a test", () => {
    assert.ok(!tests.some((t) => t.name === "Checkout"), "the h1 is the file's title, not a test that runs every flow at once");
  });

  test("no two tests share a name, because the name is the recording's filename", () => {
    assert.equal(new Set(tests.map((t) => t.name)).size, tests.length);
  });
});

// ── THE README MAY NOT SEND THE READER SOMEWHERE THEY CANNOT GO ─────────────────────────────────
//
// lib/suite.mjs writes the rule down where it was first learned: "NOT `templates/example-test.md`:
// that is a path inside an npm package the reader has not checked out, and pointing somebody at a
// file they cannot open is the same as pointing them nowhere."
//
// The README then did it twice. Both files really do ship, but grep over bin/ and lib/ finds no
// code that reads, prints or copies either, so no command surfaces them — and on the documented
// `npx smolanalytics` path they land in an unguessable ~/.npm/_npx/<hash>/ directory.
describe("the README's pointers are ones a reader can follow", () => {
  test("no package-relative path is offered as the way to get a file", () => {
    const pointed = [...readme.matchAll(/`(templates\/[\w.-]+)`/g)].map((m) => m[1]);
    assert.deepEqual(pointed, [], `the README names ${pointed.join(", ")} as somewhere to look`);
  });

  test("the example suite is in the README itself, not named and withheld", () => {
    // Every heading of templates/example-test.md, so the two cannot drift into different suites.
    const headings = [...exampleTest.matchAll(/^##\s+(.+)$/gm)].map((m) => m[1].trim());
    assert.ok(headings.length >= 5, `only ${headings.length} headings in the example file`);
    for (const h of headings) {
      assert.ok(readme.includes(h), `the README's inlined suite is missing "${h}"`);
    }
  });

  test("the workflow is linked by a URL, since 13KB of YAML does not belong in a flag table", () => {
    assert.match(readme, /https:\/\/github\.com\/[\w.-]+\/[\w.-]+\/blob\/[\w.-]+\/cli\/templates\/github-action\.yml/,
      "the longer workflow is mentioned with no way to open it");
  });
});

// A paragraph printed twice is a paragraph nobody proofread, and this one ships to npmjs.com.
test("no paragraph in the README is printed twice", () => {
  const paras = readme
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter((p) => p.length > 80 && !p.startsWith("|") && !p.startsWith("```") && !p.startsWith("#"));
  const seen = new Map();
  for (const p of paras) seen.set(p, (seen.get(p) || 0) + 1);
  const twice = [...seen].filter(([, n]) => n > 1).map(([p]) => p.slice(0, 70));
  assert.deepEqual(twice, [], `duplicated: ${twice.join(" | ")}`);
});

// Every flag `test` takes is in the README's table except the ones nothing documented anywhere.
// --share is the sharpest case: it is the only flag in this CLI that sends anything off the
// machine, in a README whose selling point is "No account. No GitHub app. Nothing written to your
// repo." A reader auditing that claim could not find the one flag that qualifies it.
test("every flag the CLI's help lists under `test` is in the README's flag table", () => {
  const help = spawnSync(process.execPath, [path.join(dir, "..", "bin", "smolanalytics.mjs"), "--help"], { encoding: "utf8" })
    .stdout.replace(/\x1b\[[0-9;]*m/g, "");
  const section = help.slice(help.indexOf("npx smolanalytics test"), help.indexOf("npx smolanalytics suggest"));
  const flags = [...new Set([...section.matchAll(/(?:^|\s)(--[a-z][a-z-]*)/g)].map((m) => m[1]))];
  assert.ok(flags.length > 15, `only ${flags.length} flags parsed out of the help`);
  const missing = flags.filter((f) => !readme.includes(`\`${f}`));
  assert.deepEqual(missing, [], "in the CLI and not in the README, so nobody reading the docs can find it");
});

test("and the README says what --share actually transmits", () => {
  const row = readme.split("\n").find((l) => l.startsWith("| `--share`"));
  assert.ok(row, "no --share row in the flag table");
  assert.match(row, /off unless you ask/i, "a flag that publishes must say it is opt-in");
  for (const thing of ["screenshot", "sentence", "commit"]) {
    assert.ok(row.includes(thing), `the row never mentions the ${thing} it uploads`);
  }
});
