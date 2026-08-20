// `npx smolanalytics connect [editor]` — wire the smolanalytics MCP server into every
// assistant on this machine, or one named editor.
//
// This is the npm port of the engine's `connect` subcommand, HTTP-remote flavor: the
// hosted product speaks MCP over HTTP (your org endpoint at smolanalytics.com/api/mcp,
// or a single instance's /mcp), so the entry we write is a url + bearer header, never a
// local process. The per-editor mechanics mirror the engine source exactly: most clients
// key the entry under "mcpServers", VS Code uses "servers", and Claude Code is configured
// through its own CLI (`claude mcp add`) because that is its documented path.
//
// Same house rules as init: never touch a file without saying which file and what changes,
// and never leave a half-edit behind. An unparseable existing config is REPORTED and left
// alone — clobbering someone's hand-tuned mcp.json to add ourselves is how trust ends.

import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";

export const DEFAULT_ENDPOINT = "https://smolanalytics.com/api/mcp";

// endpointFrom normalizes what the user typed: a bare instance host gets "/mcp" appended,
// anything already pointing at an mcp path is used verbatim, and no input means the cloud
// org endpoint.
export function endpointFrom(url) {
  const base = (url || "").trim().replace(/\/+$/, "");
  if (!base) return DEFAULT_ENDPOINT;
  if (base.endsWith("/mcp")) return base;
  return base + "/mcp";
}

// entryFor is the one remote-server shape every editor's JSON shares.
export function entryFor(endpoint, key) {
  return { type: "http", url: endpoint, headers: { Authorization: `Bearer ${key}` } };
}

// clients lists every assistant we can auto-configure, with OS-specific config paths.
// `key` is the top-level object key the editor expects.
export function clients(platform = process.platform, home = os.homedir()) {
  let cfg;
  switch (platform) {
    case "darwin":
      cfg = path.join(home, "Library", "Application Support");
      break;
    case "win32":
      cfg = process.env.APPDATA || path.join(home, "AppData", "Roaming");
      break;
    default:
      cfg = process.env.XDG_CONFIG_HOME || path.join(home, ".config");
  }
  return [
    { name: "Claude Desktop", short: "claude-desktop", path: path.join(cfg, "Claude", "claude_desktop_config.json"), key: "mcpServers" },
    { name: "Cursor", short: "cursor", path: path.join(home, ".cursor", "mcp.json"), key: "mcpServers" },
    { name: "Windsurf", short: "windsurf", path: path.join(home, ".codeium", "windsurf", "mcp_config.json"), key: "mcpServers" },
    { name: "VS Code", short: "vscode", path: path.join(cfg, "Code", "User", "mcp.json"), key: "servers" },
    { name: "Cline", short: "cline", path: path.join(cfg, "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"), key: "mcpServers" },
  ];
}

// mergeConfig adds/replaces ONLY the "smolanalytics" entry under the editor's top-level
// key, preserving everything else in the file byte-for-meaning. Returns null on success,
// an error message otherwise. An existing file that does not parse is an error, not a
// license to overwrite.
export function mergeConfig(file, topKey, entry, io = fs) {
  let cfg = {};
  if (io.existsSync(file)) {
    try {
      const raw = io.readFileSync(file, "utf8");
      cfg = raw.trim() ? JSON.parse(raw) : {};
    } catch {
      return `${file} exists but is not valid JSON — fix it (or delete it) and re-run; nothing was changed`;
    }
    if (cfg === null || typeof cfg !== "object" || Array.isArray(cfg)) {
      return `${file} is not a JSON object — nothing was changed`;
    }
  }
  const servers = cfg[topKey] && typeof cfg[topKey] === "object" && !Array.isArray(cfg[topKey]) ? cfg[topKey] : {};
  servers["smolanalytics"] = entry;
  cfg[topKey] = servers;
  io.mkdirSync(path.dirname(file), { recursive: true });
  io.writeFileSync(file, JSON.stringify(cfg, null, 2) + "\n");
  return null;
}

// addClaudeCode registers through Claude Code's own CLI — the documented path — scoped to
// the user so it works in every repo. Returns true if the command ran and succeeded.
export function addClaudeCode(endpoint, key, run = spawnSync) {
  const probe = run("claude", ["--version"], { stdio: "ignore" });
  if (probe.error || probe.status !== 0) return false;
  // remove-then-add so a re-run updates the token instead of failing on "already exists"
  run("claude", ["mcp", "remove", "-s", "user", "smolanalytics"], { stdio: "ignore" });
  const res = run(
    "claude",
    ["mcp", "add", "-s", "user", "--transport", "http", "smolanalytics", endpoint, "--header", `Authorization: Bearer ${key}`],
    { stdio: "ignore" },
  );
  return !res.error && res.status === 0;
}

export function connectCmd({ url, key, target, log = console.log, io = fs, run = spawnSync, platform = process.platform }) {
  if (!key) {
    log("");
    log("connect needs your MCP token:");
    log("  npx smolanalytics connect --key sa_org_...        (org API token: smolanalytics.com → Settings)");
    log("  npx smolanalytics connect --key ... --url https://YOUR-INSTANCE   (one instance instead of the org)");
    log("");
    log("One connection operates every project: pass project=\"the-name\" to any tool.");
    return 1;
  }
  const endpoint = endpointFrom(url);
  const entry = entryFor(endpoint, key);
  const explicit = target && target !== "all";

  let wrote = 0;
  let failed = 0;
  const skipped = [];

  for (const c of clients(platform)) {
    if (explicit && target.toLowerCase() !== c.short && target.toLowerCase() !== c.name.toLowerCase()) continue;
    // on auto-detect, only touch assistants that are actually installed (their config dir
    // exists) — and SAY when we skip one, so "my editor wasn't mentioned" never happens.
    if (!explicit && !io.existsSync(path.dirname(c.path))) {
      skipped.push(c);
      continue;
    }
    const err = mergeConfig(c.path, c.key, entry, io);
    if (err) {
      log(`  x ${c.name}: ${err}`);
      failed++;
    } else {
      log(`  + ${c.name}`);
      log(`      ${c.path}`);
      wrote++;
    }
  }

  if (!explicit || target === "claude-code") {
    if (addClaudeCode(endpoint, key, run)) {
      log("  + Claude Code  (added via `claude mcp add -s user`)");
      wrote++;
    } else if (explicit) {
      log("  x Claude Code: the `claude` CLI was not found on PATH");
      failed++;
    }
  }

  if (skipped.length > 0) {
    log("");
    log("Not configured (no config folder yet, so the app looks uninstalled):");
    for (const c of skipped) log(`  - ${c.name.padEnd(16)} expected ${c.path}`);
    log("");
    log("If one of those IS installed, open it once so it creates its config, or force it:");
    log(`  npx smolanalytics connect ${skipped[0].short} --key ...`);
  }

  if (wrote === 0) {
    log("");
    log('Nothing was configured automatically. Add this by hand (VS Code uses "servers" instead of "mcpServers"):');
    log("");
    log(JSON.stringify({ mcpServers: { smolanalytics: entry } }, null, 2));
    return failed > 0 ? 1 : 0;
  }

  log("");
  log("Done. Now FULLY restart the assistant, not just its window:");
  log("  Claude Desktop   quit with Cmd-Q (closing the window is not enough), then reopen");
  log("  Cursor/Windsurf  reload the window, or restart the app");
  log("");
  log('Then ask: "investigate — what needs me today?"');
  return failed > 0 ? 1 : 0;
}
