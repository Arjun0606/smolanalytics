// Help that survives an eighty-column terminal.
//
// MEASURED against the published binary, with ANSI stripped: the longest help line is 153
// characters (--since), then 149 (--workers), then 135 (--share), and 31 of the 80 lines are over
// 80 columns. Folded by the terminal itself, the flag column collapses — the continuation starts
// in column 1 and runs under the next flag's name:
//
//   --workers <n>         with --suite: run this many tests at once (default: meas
//   ured from cores, memory and whether a key is set; 1 is one at a time)
//
// Nothing is lost, and that is exactly why it never got fixed: it is only ever wrong on the
// screen of the person reading the flags for the first time. So the fold happens here, where the
// description column is known, and continuation lines are indented to it.

const ANSI = /\x1b\[[0-9;]*m/g;

/** What the terminal actually shows, with the colour codes taken out. */
export const visible = (s) => String(s).replace(ANSI, "");

/**
 * Fold help text to the terminal's width, hanging continuations under the description column.
 *
 * `columns` is process.stdout.columns, which is undefined when stdout is a pipe — a redirected
 * `--help > flags.txt` gets the 80-column shape, which is the one that reads correctly anywhere.
 * Capped at 100 because a 200-column window turns a flag list into a paragraph nobody scans.
 */
export function wrapHelp(text, columns) {
  const width = Math.max(Math.min(Number(columns) || 80, 100), 40);
  return String(text)
    .split("\n")
    .map((line) => foldLine(line, width))
    .join("\n");
}

function foldLine(line, width) {
  const stripped = visible(line);
  if (stripped.length <= width) return line;

  // The column the description starts in: the LAST run of two or more spaces that still begins in
  // the left half of the screen. Not the first — `  --url  <url>          staging, …` has a gap
  // inside the flag itself, and hanging at that one indents the continuation under `<url>`.
  // A line with no such gap — a full-width sentence — hangs at its own indent, since there is no
  // column to line up under, and so does a flag column past half the screen, which would leave
  // two words per line.
  const indent = /^\s*/.exec(stripped)[0].length;
  const limit = Math.floor(width / 2);
  let hang = indent;
  for (const m of stripped.matchAll(/\s{2,}(?=\S)/g)) {
    if (m.index === 0) continue; // the leading indent, not a gap between columns
    const col = m.index + m[0].length;
    if (col > limit) break;
    hang = col;
  }
  const pad = " ".repeat(hang);

  const out = [];
  let cur = null;
  let len = 0;
  // Split on single spaces so runs of spaces survive as empty tokens and the line rebuilds byte
  // for byte where it is not folded. Colour codes ride along on the token they were attached to.
  for (const tok of line.split(" ")) {
    const w = visible(tok).length;
    if (cur === null) {
      cur = tok;
      len = w;
      continue;
    }
    if (len + 1 + w > width && visible(cur).trim()) {
      out.push(cur.replace(/\s+$/, ""));
      cur = pad + tok;
      len = hang + w;
      continue;
    }
    cur = `${cur} ${tok}`;
    len = len + 1 + w;
  }
  if (cur !== null && visible(cur).trim()) out.push(cur.replace(/\s+$/, ""));
  return out.join("\n");
}
