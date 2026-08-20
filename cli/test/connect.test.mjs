// The connect subcommand's contract: merge, never clobber; VS Code's odd key; refuse to
// touch a config we cannot parse; auto-detect skips-and-says. Each case was watched fail
// (by breaking the code under test) before it was trusted.

import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { mergeConfig, entryFor, endpointFrom, clients, connectCmd } from "../lib/connect.mjs";

const tmp = () => fs.mkdtempSync(path.join(os.tmpdir(), "smol-connect-"));

test("endpointFrom: bare host gains /mcp, mcp paths pass through, empty means the org endpoint", () => {
  assert.equal(endpointFrom("https://x.fly.dev"), "https://x.fly.dev/mcp");
  assert.equal(endpointFrom("https://x.fly.dev/"), "https://x.fly.dev/mcp");
  assert.equal(endpointFrom("https://x.fly.dev/mcp"), "https://x.fly.dev/mcp");
  assert.equal(endpointFrom("https://smolanalytics.com/api/mcp"), "https://smolanalytics.com/api/mcp");
  assert.equal(endpointFrom(""), "https://smolanalytics.com/api/mcp");
});

test("mergeConfig preserves every other server and every unrelated key", () => {
  const dir = tmp();
  const file = path.join(dir, "mcp.json");
  fs.writeFileSync(file, JSON.stringify({ mcpServers: { other: { command: "x" } }, theme: "dark" }));
  const err = mergeConfig(file, "mcpServers", entryFor("https://a/mcp", "k1"));
  assert.equal(err, null);
  const got = JSON.parse(fs.readFileSync(file, "utf8"));
  assert.deepEqual(got.mcpServers.other, { command: "x" }, "the neighbouring server was lost");
  assert.equal(got.theme, "dark", "an unrelated key was lost");
  assert.equal(got.mcpServers.smolanalytics.url, "https://a/mcp");
  assert.equal(got.mcpServers.smolanalytics.headers.Authorization, "Bearer k1");
});

test("mergeConfig is idempotent and a re-run updates the token in place", () => {
  const dir = tmp();
  const file = path.join(dir, "mcp.json");
  assert.equal(mergeConfig(file, "mcpServers", entryFor("https://a/mcp", "old")), null);
  assert.equal(mergeConfig(file, "mcpServers", entryFor("https://a/mcp", "new")), null);
  const got = JSON.parse(fs.readFileSync(file, "utf8"));
  assert.equal(Object.keys(got.mcpServers).length, 1);
  assert.equal(got.mcpServers.smolanalytics.headers.Authorization, "Bearer new");
});

test("an unparseable existing config is refused, reported, and left byte-identical", () => {
  const dir = tmp();
  const file = path.join(dir, "mcp.json");
  fs.writeFileSync(file, "{ not json");
  const err = mergeConfig(file, "mcpServers", entryFor("https://a/mcp", "k"));
  assert.ok(err && err.includes("not valid JSON"), "the refusal must say why");
  assert.equal(fs.readFileSync(file, "utf8"), "{ not json", "the broken file was modified");
});

test("VS Code is keyed under servers, everyone else under mcpServers", () => {
  const byShort = Object.fromEntries(clients("darwin", "/home/u").map((c) => [c.short, c.key]));
  assert.equal(byShort.vscode, "servers");
  for (const s of ["cursor", "windsurf", "claude-desktop", "cline"]) assert.equal(byShort[s], "mcpServers");
});

test("connectCmd without a key stops with instructions and edits nothing", () => {
  const lines = [];
  const code = connectCmd({ url: "", key: "", target: "", log: (l) => lines.push(l) });
  assert.equal(code, 1);
  assert.ok(lines.join("\n").includes("Settings"), "must say where the token lives");
});

test("auto-detect skips uninstalled assistants and says so by name", () => {
  const dir = tmp();
  // a fake fs where ONLY cursor's config dir exists
  const home = path.join(dir, "home");
  fs.mkdirSync(path.join(home, ".cursor"), { recursive: true });
  const io = {
    existsSync: (p) => fs.existsSync(p.replace(os.homedir(), home)),
    readFileSync: (p, e) => fs.readFileSync(p.replace(os.homedir(), home), e),
    writeFileSync: (p, c) => fs.writeFileSync(p.replace(os.homedir(), home), c),
    mkdirSync: (p, o) => fs.mkdirSync(p.replace(os.homedir(), home), o),
  };
  const lines = [];
  const run = () => ({ status: 1 }); // claude CLI "not installed"
  const code = connectCmd({ url: "https://x.fly.dev", key: "sa_k", target: "", log: (l) => lines.push(l), io, run });
  const out = lines.join("\n");
  assert.equal(code, 0);
  assert.ok(out.includes("+ Cursor"), "the installed editor was configured");
  assert.ok(out.includes("Not configured"), "skips must be reported");
  assert.ok(out.includes("Windsurf"), "the skipped editor must be named");
  const written = JSON.parse(fs.readFileSync(path.join(home, ".cursor", "mcp.json"), "utf8"));
  assert.equal(written.mcpServers.smolanalytics.url, "https://x.fly.dev/mcp");
});
