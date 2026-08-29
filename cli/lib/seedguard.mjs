// THE TWO PLACES A SEEDED VALUE STILL ESCAPED: a URL, and a URL-ENCODED URL.
//
// lib/seed.mjs masks a seeded fixture value the way lib/safety.mjs masks a password — the value is
// paired with {{orderid}} and every artefact writes the placeholder. That worked for the two things
// a value was expected to be: text typed into a field, and text quoted back as proof.
//
// It did not work for the third, which is the one the seed module's own header warns about — "an
// order id, and just as easily a session token or a signed magic link". A magic link is a URL, and
// a URL is where a fixture id most naturally lives. MEASURED, on a real browser, before this file:
//
//   1. THE NAVIGATION.  A sentence that says "open order {{orderToken}}" makes the agent call
//                       `goto`, because the tool description tells it to. compile() then wrote
//                          {"kind":"goto","url":"https://app/order?t=tok_9mqax5mbkfq1bdgpb7jq"}
//                       into the recording the CI template caches and users are told to commit,
//                       and describe() printed the same line to the terminal, into the step label,
//                       into the row posted to a project and from there into the pull request
//                       comment. The masked `fill` beside it looked perfect.
//
//   2. THE ENCODING.    maskSecrets is a byte-for-byte substring replace. The instant a browser
//                       puts a value in a query string it is percent-encoded, so a token holding
//                       + / or = — which is every base64 session token ever issued — is invisible
//                       to it. `sk+live+ab/1=` reaches captureEvidence's `URL:` line as
//                       `sk%2Blive%2Bab%2F1%3D`, is not masked, and that file is uploaded as a CI
//                       artifact. The existing leak test could not see this: its fixture token is
//                       [a-z0-9_] and URL encoding leaves it alone, so the scan was for a value
//                       that could not have been transformed. Palindromic data, one more time.
//
// And the same two lines were a COST bug, which is how they would have been noticed eventually and
// expensively. A goto frozen with run one's token replays against run one's fixture forever: the
// proof resolves to run two's id, the page shows run one's, and every replay is `outcome-changed`
// → stale → a full agent run. That is this repository's rebase() incident rebuilt inside --seed.
//
// THE RULE HERE. A pair list is EXPANDED once, at the boundary where lib/seed.mjs hands its pairs
// to the runner, so that every existing maskSecrets/unmaskSecrets call site gains encoded coverage
// without any of them learning about encodings. Each encoding gets its OWN placeholder spelling, so
// masking stays exactly reversible — the whole point of the machinery is that a recording carrying
// no value still replays, and a one-way mask would trade a leak for a broken feature.
//
// WHAT THIS DOES NOT CLAIM. It covers the encodings a browser actually performs on a value on its
// way into a URL. A value the application itself rewrites — hashed, re-signed, base64'd into a
// different string — is a different string, and no substring mask can find it. Said here rather
// than claimed away.

import { maskSecrets, unmaskSecrets } from "./safety.mjs";

/**
 * The suffix an encoded variant's placeholder carries. It is a legal placeholder name
 * ([A-Za-z][A-Za-z0-9_]*), so a recording holding {{ordertoken__urlencoded}} is readable by every
 * token-shaped thing in this codebase, and it is distinct from {{ordertoken}} on both sides — one
 * is not a substring of the other, so mask and unmask cannot cross-contaminate.
 */
export const ENCODED_SUFFIX = "__urlencoded";

/** How a browser puts a value into a URL. Both, because a path and a query escape differently. */
const ENCODERS = [encodeURIComponent, encodeURI];

/**
 * Expand {value, token} pairs with the forms the same value takes inside a URL.
 *
 * Longest value first, for the reason lib/seed.mjs sorts: a short value living inside a long one
 * would otherwise mask the short one first and leave a half-replaced string behind. `%2B` is three
 * characters longer than `+`, so the encoded form usually sorts ABOVE the raw one, which is also
 * the order that is correct when one contains the other.
 *
 * Four characters is maskSecrets's own floor and it is honoured here: an encoded form shorter than
 * that is dropped rather than smuggled past the rule, because masking "%2B" would rewrite every
 * URL on the page that happens to contain a plus.
 */
export function guardPairs(secrets = []) {
  const out = [];
  const seen = new Set();
  const add = (value, token) => {
    if (typeof value !== "string" || value.length < 4 || !token) return;
    if (seen.has(value)) return;
    seen.add(value);
    out.push({ value, token });
  };
  for (const p of secrets) {
    if (!p || typeof p.value !== "string" || !p.token) continue;
    add(p.value, p.token);
    // {{orderid}} -> {{orderid__urlencoded}}. A token that is not brace-shaped (nothing in this
    // codebase produces one, but this function is exported) gets the suffix appended plainly,
    // which is still unique and still reversible.
    const encodedToken = /^\{\{.+\}\}$/.test(p.token)
      ? `${p.token.slice(0, -2)}${ENCODED_SUFFIX}}}`
      : `${p.token}${ENCODED_SUFFIX}`;
    for (const enc of ENCODERS) {
      let e = "";
      try {
        e = enc(p.value);
      } catch {
        continue; // a lone surrogate throws; it is not a URL-shaped value either way
      }
      if (e !== p.value) add(e, encodedToken);
    }
  }
  return out.sort((a, b) => b.value.length - a.value.length);
}

/**
 * The WHATWG URL parser percent-encodes { and } in a PATH — not in a query. So a recording whose
 * masked path is /order/{{ordertoken}} comes back out of rebase()'s URL round-trip as
 * /order/%7B%7Bordertoken%7D%7D, and a plain unmask would then find nothing, navigate to the
 * literal escaped placeholder and report the test stale on every run forever.
 *
 * So the braces are normalised before resolving. Only around a placeholder-shaped name, so a page
 * whose real URL contains %7B is untouched.
 */
export function normalizeTokens(text) {
  return String(text ?? "").replace(/%7B%7B([A-Za-z][A-Za-z0-9_]*)%7D%7D/gi, "{{$1}}");
}

/** Mask a URL: every seeded value in it, in whichever form the browser wrote it. */
export function maskUrl(url, secrets = []) {
  return maskSecrets(url, secrets);
}

/**
 * Resolve a recorded URL back to THIS run's seeded values, at the moment of the navigation and
 * nowhere earlier — the same contract the recorded password fill has.
 *
 * A token this run cannot resolve is left exactly as written, because navigating to the literal
 * text and failing honestly beats navigating to an empty string and reporting that the app 404s.
 */
export function resolveUrl(url, secrets = []) {
  return unmaskSecrets(normalizeTokens(url), secrets);
}
