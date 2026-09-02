// The one number a bug report needs, and until this existed there was no way to ask for it.
//
// MEASURED, against the published binary: `--version`, `-v`, `-V` and `version` each printed
// "unknown command <arg>" followed by the whole eighty-line help and exited 2. The MCP server had
// the same hole from the other side — serverInfo was the hardcoded string "local", so an editor
// showed "smolanalytics local" and a person asking which build they were on got an answer that is
// the same on every machine that ever ran it.
//
// Read from package.json rather than written down twice: a version constant in a source file is a
// second place to bump, and the one that gets forgotten is always the one somebody reads.
// package.json is in every npm tarball whatever `files` says, so this resolves the same way from a
// checkout, a global install and an npx cache.

import { readFileSync } from "node:fs";

/** The published version of this package. "unknown" only if package.json is unreadable. */
export function packageVersion(read = (p) => readFileSync(p, "utf8")) {
  try {
    const v = JSON.parse(read(new URL("../package.json", import.meta.url))).version;
    return typeof v === "string" && v ? v : "unknown";
  } catch {
    // Never throws. `--version` is the cheapest command here and the one most likely to be run by
    // a script; a crash reading our own manifest must not be the answer it gets.
    return "unknown";
  }
}
