// Framework detection for `npx smolanalytics init`.
//
// Deliberately a pure function over a small filesystem interface rather than something that
// reaches for `fs` itself. Detection is the part that decides which of a stranger's source
// files we are about to edit, so it has to be testable against fixtures without a temp dir.
//
// Order matters. A Next.js repo also has a package.json with "react" in it and may well have
// an index.html lying around, so the most specific signal has to win. Everything here is
// ordered most-specific first and the first match returns.

/**
 * @typedef {Object} FsLike
 * @property {(p: string) => boolean} exists
 * @property {(p: string) => string} read
 */

/**
 * @typedef {Object} Detection
 * @property {string} framework   human-readable name, used in the CLI's output
 * @property {string} file        the file we would edit
 * @property {"html-head"|"next-app"|"next-pages"} strategy
 * @property {string} [note]      anything the user should know before we touch the file
 */

const has = (deps, name) => Boolean(deps && deps[name]);

/** Read and parse package.json deps, tolerating a missing or malformed file. */
export function readDeps(fs, dir = ".") {
  const p = `${dir}/package.json`.replace(/^\.\//, "");
  if (!fs.exists(p)) return {};
  try {
    const pkg = JSON.parse(fs.read(p));
    return { ...(pkg.dependencies || {}), ...(pkg.devDependencies || {}) };
  } catch {
    return {};
  }
}

/**
 * Work out which file to instrument.
 * Returns null when nothing is recognised — the CLI then prints the snippet and lets the
 * person place it, which is a far better outcome than guessing and editing the wrong file.
 *
 * @param {FsLike} fs
 * @returns {Detection|null}
 */
export function detect(fs) {
  const deps = readDeps(fs);

  // Next.js first: it owns its HTML and has two very different router layouts.
  if (has(deps, "next")) {
    for (const f of ["app/layout.tsx", "app/layout.jsx", "src/app/layout.tsx", "src/app/layout.jsx"]) {
      if (fs.exists(f)) {
        return { framework: "Next.js (App Router)", file: f, strategy: "next-app" };
      }
    }
    for (const f of ["pages/_document.tsx", "pages/_document.jsx", "src/pages/_document.tsx"]) {
      if (fs.exists(f)) {
        return { framework: "Next.js (Pages Router)", file: f, strategy: "next-pages" };
      }
    }
    return {
      framework: "Next.js",
      file: "app/layout.tsx",
      strategy: "next-app",
      note: "no layout or _document found, so nothing was edited",
    };
  }

  // SvelteKit has exactly one HTML shell and it is always this path.
  if (has(deps, "@sveltejs/kit") && fs.exists("src/app.html")) {
    return { framework: "SvelteKit", file: "src/app.html", strategy: "html-head" };
  }

  // Nuxt and Astro own their HTML too; both are better served by their own config, so we
  // say so rather than editing a generated file that will be overwritten.
  if (has(deps, "nuxt")) {
    return {
      framework: "Nuxt",
      file: "nuxt.config.ts",
      strategy: "html-head",
      note: "add the tags via app.head.script in nuxt.config, shown below",
    };
  }
  if (has(deps, "astro")) {
    return {
      framework: "Astro",
      file: "src/layouts/Layout.astro",
      strategy: "html-head",
      note: "Astro strips inline scripts unless they are marked is:inline",
    };
  }

  // Vite and friends: a real index.html at the root that ships to the browser.
  if (fs.exists("index.html")) {
    return { framework: has(deps, "vite") ? "Vite" : "static HTML", file: "index.html", strategy: "html-head" };
  }
  if (fs.exists("public/index.html")) {
    return { framework: "Create React App", file: "public/index.html", strategy: "html-head" };
  }

  return null;
}
