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
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseSuite } from "../lib/suite.mjs";

const dir = path.dirname(fileURLToPath(import.meta.url));
const templates = path.join(dir, "..", "templates");
const workflowPath = path.join(templates, "github-action.yml");
const workflow = readFileSync(workflowPath, "utf8");
const exampleTest = readFileSync(path.join(templates, "example-test.md"), "utf8");
const readme = readFileSync(path.join(dir, "..", "README.md"), "utf8");

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
    const cache = steps.find((s) => String(s?.uses || "").startsWith("actions/cache@"));
    assert.ok(cache, "no actions/cache step: every CI run would be a fresh agent run");
    assert.equal(cache.with.path, ".smolanalytics/recordings", "must cache the directory the CLI writes recordings to");
    assert.match(String(cache.with["restore-keys"]), /smolanalytics-recordings-/, "without restore-keys the cache never hits");
  });

  test("every action is pinned", () => {
    for (const s of steps) {
      if (!s?.uses) continue;
      assert.match(s.uses, /@v\d/, `${s.uses} is not pinned to a version`);
    }
  });

  test("the three ways to get a URL all feed the same step id", () => {
    // (b) and (c) ship commented out. They are still checked, because a customer deleting (a) and
    // uncommenting one of them must not have to fix an id to make the run step work.
    const offered = workflow.match(/^\s*#?\s*id: preview$/gm) || [];
    assert.equal(offered.length, 3, "three preview options, one id");
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
