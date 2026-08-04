# dashboard presentation: what to fix, from looking at it

Written after opening the live instance in a browser at 1440px, not from reading templates. Each
item is something visible in that screenshot, with the reason it matters and where it lives.

Two of these are already fixed and shipped in v0.28.1 — recorded here so nobody re-derives them.

---

## fixed in v0.28.1

**Signed-in routes in the crawl-gap report.** The page led with *"No AI crawler has read
/dashboard … list it in your sitemap"*. Wrong advice and a privacy problem in one sentence.
robots.txt could not catch it, because nobody disallows a route that already needs a session.
Now excluded by a first-segment heuristic (`internal/aicrawl`, `privateRoute`).

**Four-figure percentages.** The pageview tile read `+6875% vs prior` — correct off a prior of 8,
and unreadable. Anything at or above 10x now prints as `70x` (`internal/api`, `deltaStr`).

---

## 1. the findings block — the highest-value fix

Four bullets of undifferentiated prose, each ending `why? → brief →`, one of them three lines
long. The block's entire job is triage and nothing in it ranks.

- **Severity has no visual weight.** "Day-1 retention 4%" and "page views up 249%" get identical
  treatment. One is a problem, one is good news.
- **Every line ends in the same two links**, so the eye has nothing to fix on.
- **The third finding is a paragraph.** Long-form prose in a scannable list stops the scan.

**Do:** one line per finding, severity as a leading marker (not colour alone), the number
promoted out of the sentence, and prose collapsed behind the existing `why?`. The findings are
already sorted warnings-first in `insight.GenerateForFunnel` — the sort exists and the rendering
throws it away.

## 2. sparklines with no scale

Six KPI tiles, each with a shape and no y-reference. They read as decoration, and a chart that
cannot be read is worse than no chart because it occupies the space where a number would go.

**Do:** either a min/max endpoint label, or drop them and give the number the room. Prefer the
label — the shape carries real information once it has a scale.

## 3. `Bounce Rate 44%` / `1 pageview, <10s`

Every other tile puts a *comparison* under the number. This one puts a *definition*, in the same
position and style, so it reads as a value and parses as nonsense.

**Do:** definitions belong in the `why?` affordance, not the delta slot. If a tile has no
comparison, leave the slot empty — the template already handles that case.

## 4. the left navigation

25+ items, one flat list, small mono type, group headers at the same weight as the items under
them. Finding "AI crawlers" means reading the whole list.

**Do:** the groups already exist in the markup (`DID IT CONVERT`, `WHERE THEY CAME FROM`, …) —
give them real hierarchy, and collapse groups that are empty for this instance.

## 5. the dev-data banner takes the top of the page

`8 events from localhost/development are hidden` sits above the verdict card. It is true and
worth saying, but it is a footnote occupying the most valuable space on the page, above the one
thing the page exists to tell you.

**Do:** move it below the verdict, or into the scope bar where the filter lives.

## 6. product copy inside the product

`window 10d · recomputed from raw events on every load · the API and your agent get
byte-identical answers` sits under the tiles. That sentence sells the product to someone who has
already bought it, in the space where a reader is trying to read numbers.

**Do:** keep it on the marketing site and in the tool descriptions. In the dashboard it is noise.

---

## how to do this safely

The lesson from this whole session, twice over: **these bugs were invisible in tests and obvious
on screen.** The memory profiling, the coverage noise, the double-counting and both items above
were all found by running the thing, never by reading it.

So: change one zone, render it, look at it. `internal/api/dashboard_legibility_test.go` already
locks contrast, font-size tokens and text equivalents, so run it after each change — it catches
regressions in the ladder but it cannot tell you a block is unreadable.

**Order:** 1, then 3, then 5 — they are small, independent, and together fix most of what makes
the page feel dense. 2 and 4 are bigger. 6 is a deletion.
