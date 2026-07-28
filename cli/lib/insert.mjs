// Snippet generation and insertion.
//
// This edits files in someone else's repository, so the two rules it must never break are:
// it is idempotent (running init twice does not produce two trackers), and it either makes
// the exact edit or makes none at all. A partial edit to a layout file is worse than no edit,
// because the person then has a broken app AND has to work out what we did.

/** Marker used to recognise our own previous edit. Also what makes re-running safe. */
export const MARKER = "smolanalytics";

export function snippetHtml(host, writeKey, indent = "    ") {
  return (
    `${indent}<script src="${host}/sdk.js"></script>\n` +
    `${indent}<script>smolanalytics.init("${writeKey}", { host: "${host}" });</script>`
  );
}

export function snippetNextApp(host, writeKey, indent = "        ") {
  const i = indent;
  return (
    `${i}<script src="${host}/sdk.js" async />\n` +
    `${i}<script\n` +
    `${i}  dangerouslySetInnerHTML={{\n` +
    `${i}    __html: 'smolanalytics.init("${writeKey}", { host: "${host}" });',\n` +
    `${i}  }}\n` +
    `${i}/>`
  );
}

/** Already wired? Checked on the raw file so a hand-placed snippet counts too. */
export const alreadyInstalled = (src) => src.includes(`${MARKER}.init(`);

/**
 * @typedef {Object} InsertResult
 * @property {"inserted"|"already"|"no-anchor"} status
 * @property {string} [content]  the new file content, only when status is "inserted"
 * @property {string} [reason]
 */

/**
 * Insert into an HTML shell, immediately before </head>.
 *
 * Case-insensitive on the tag because plenty of real templates use `</HEAD>`, and matching
 * the LAST occurrence rather than the first, since a commented-out or nested example earlier
 * in the file would otherwise capture the insert.
 *
 * @returns {InsertResult}
 */
export function insertIntoHtml(src, host, writeKey) {
  if (alreadyInstalled(src)) return { status: "already" };
  const m = [...src.matchAll(/<\/head>/gi)].pop();
  if (!m) return { status: "no-anchor", reason: "no </head> tag found" };
  const at = m.index;
  // Indent to match the closing tag, one level deeper. The naive version appended a
  // fixed-indent snippet straight after whatever whitespace preceded </head>, which added
  // the two together on the first line and produced a snippet indented inconsistently
  // with itself.
  const lineStart = src.lastIndexOf("\n", at) + 1;
  const closeIndent = (src.slice(lineStart, at).match(/^\s*/) || [""])[0];
  const indent = `${closeIndent}  `;
  const before = src.slice(0, lineStart);
  const content = `${before}${snippetHtml(host, writeKey, indent)}\n${closeIndent}${src.slice(at)}`;
  return { status: "inserted", content };
}

/**
 * Insert into a Next.js App Router layout, immediately before </head> if the layout declares
 * one, otherwise immediately after the opening <body> tag.
 *
 * Most App Router layouts have no explicit <head>, because Next builds it from the metadata
 * export. Falling back to <body> is what makes this work on a default `create-next-app`
 * instead of failing on the majority case.
 *
 * @returns {InsertResult}
 */
export function insertIntoNextLayout(src, host, writeKey) {
  if (alreadyInstalled(src)) return { status: "already" };

  const head = [...src.matchAll(/<\/head>/gi)].pop();
  if (head) {
    const snippet = snippetNextApp(host, writeKey);
    return { status: "inserted", content: `${src.slice(0, head.index)}${snippet}\n      ${src.slice(head.index)}` };
  }

  // Opening <body ...> tag, not a self-closing or closing one.
  const body = [...src.matchAll(/<body(\s[^>]*)?>/gi)].pop();
  if (body) {
    const at = body.index + body[0].length;
    // Match the surrounding indentation and put whatever followed <body> back on its own
    // line. Without this the insert produces `/>{children}</body>`, which is valid JSX and
    // looks like we don't know what we're doing in a file the person is about to read.
    const lineStart = src.lastIndexOf("\n", body.index) + 1;
    const indent = (src.slice(lineStart, body.index).match(/^\s*/) || [""])[0] + "  ";
    const snippet = snippetNextApp(host, writeKey, indent);
    const rest = src.slice(at);
    const reindented = rest.startsWith("\n") ? rest : `\n${indent}${rest.trimStart()}`;
    return { status: "inserted", content: `${src.slice(0, at)}\n${snippet}${reindented}` };
  }
  return { status: "no-anchor", reason: "no </head> or <body> found in the layout" };
}

// Frameworks we deliberately do NOT edit, because the correct install is not a script tag
// in an HTML file and pretending otherwise produces confident, wrong advice. Each returns
// the install that actually works for that framework.
export const MANUAL_SNIPPETS = {
  nuxt: (host, key) =>
    `// nuxt.config.ts\nexport default defineNuxtConfig({\n  app: {\n    head: {\n      script: [\n        { src: "${host}/sdk.js" },\n        { innerHTML: 'smolanalytics.init("${key}", { host: "${host}" });' },\n      ],\n    },\n  },\n});`,
  astro: (host, key) =>
    `<!-- in your layout's <head> -->\n<script is:inline src="${host}/sdk.js"></script>\n<script is:inline>smolanalytics.init("${key}", { host: "${host}" });</script>\n\n<!-- is:inline is required. Without it Astro bundles the script and the\n     init call runs before the SDK has defined smolanalytics. -->`,
};

/** Dispatch on the detection strategy. */
export function applyStrategy(strategy, src, host, writeKey) {
  if (strategy === "html-head") return insertIntoHtml(src, host, writeKey);
  if (strategy === "next-app" || strategy === "next-pages") {
    return insertIntoNextLayout(src, host, writeKey);
  }
  if (strategy === "manual") return { status: "manual" };
  return { status: "no-anchor", reason: `unknown strategy ${strategy}` };
}

/**
 * Add or update a key in a dotenv file without disturbing anything else in it.
 * People keep secrets in these; rewriting one wholesale is a good way to destroy a day.
 */
export function upsertEnv(src, key, value) {
  const line = `${key}=${value}`;
  const re = new RegExp(`^${key}=.*$`, "m");
  if (re.test(src)) return src.replace(re, line);
  const sep = src.length === 0 || src.endsWith("\n") ? "" : "\n";
  return `${src}${sep}${line}\n`;
}
