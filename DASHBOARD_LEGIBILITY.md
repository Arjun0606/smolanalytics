# Dashboard legibility — #pane-aivis / #pane-aicrawl

Resolves four independent critiques into one pass. Apply top to bottom.

**Baseline warning.** The four critiques were written against `HEAD` (580698d). The working
copy has since been edited by a concurrent process and roughly half of what they proposed is
**already applied**. Everything below is verified against the *working copy*, not HEAD. In
particular these findings are **stale and must not be re-applied**:

| Stale finding | Reality in the working copy |
|---|---|
| "`table.tbl` has no CSS rule" | All three `.tbl` tables were converted to `.ctab`. `class="tbl"` count is now **0**. |
| "`.num` is undefined" | All `class="num"` converted to `class="n"`. `table.ctab .n{text-align:right}` applies. |
| "`.zonelbl` is 11px/--mut2, same as `.agmeta`" | Already 13px/600/--mut, with a new `.zsep` border-top class and the inline margins already replaced. |
| "`.zonelbl .sub` sits inline at the same size" | Already `flex-basis:100%`, 12px, 400, --mut2. |
| "`.agmeta` is 11px/--mut2" | Already 13px/--mut/80ch; `.agmeta b` already --fg. |
| "`.pane h3` is 12px" | `.pane h3` was raised to 15px — but see §3.1: it **never reaches these panes**. |
| "`.sk`/`.skv`/`.skl` are too small" | Already 14px 20px padding / 26px value / 11px label. |

What the concurrent process did **not** fix, and what this spec is actually for, is listed in
§0.

---

## 0. What is still wrong, measured

1. **Every table row rule in these panes is invisible.** `table.ctab td` has
   `border-bottom:1px solid var(--s2)` and `.deck .pane` has `background:var(--s2)`. Five
   tables, zero row separation. This is the single largest contributor to "data vomit" and it
   is a one-token bug from a surface re-base, not a taste call.
2. **The card title is smaller than its own body text.** `.deck .pane h3` (L637) sets 12px and
   outranks `.pane h3` (L332, 15px) on specificity *and* source order. So the concurrent
   process's title fix does not apply to any deck pane — including the one the founder was
   looking at. Title 12px, `.agmeta` 13px: the hierarchy is inverted at the very top.
3. **`.acc` renders as plain text.** `.acc` is only ever defined scoped (`.kpi .kv.acc`,
   `.skv.acc`). Inside the converted tables, `<span class="acc">answering someone now</span>`
   and `<span class="acc">Npp better</span>` render at full `--fg`. The amber signal the markup
   asks for never appears.
4. **The type scale is now more violated than before.** `:root` was bumped
   11/13/15/24 → 12/14/16/26. But the tokens are consumed by **11 declarations** against
   **133 hardcoded `font-size:Npx`**. The bump moved almost nothing while making `--fs-lbl`
   (12px) disagree with 53 hardcoded 11px labels. These two panes still render **six** sizes:
   9, 10, 11, 12, 13, 26.
5. **More invisible `--s2`-on-`--s2` fills**: `.seg:hover` (row hover does nothing),
   `.sentbar` (the unfilled remainder of the sentiment track is indistinguishable from the card).
6. **`.gvmeta` is `--dis` #5F5F5F on `--s2` = 2.83:1** — below AA at every size on this page.
   It carries the model id and run date, which the file's own comment calls the difference
   between a quote and an unfalsifiable claim.
7. **Width.** Both panes are `.wide` = `span 2` ≈ 737px at 1440, while `#pane-agent`,
   `#pane-sessions`, `#pane-retention`, `#pane-events`, `#pane-paths` already take `span 3`
   ≈ 1113px. `#pane-aivis` carries a 6-column table, a 7-column table, an SVG chart and 5+
   verbatims in the *medium* column. That is the founder's "unused horizontal space".

---

## 1. `:root` — revert the type bump, keep four sizes

**L24-25**

```css
/* OLD */
--fs-lbl:12px; --lh-lbl:18px; --fs-body:14px; --lh-body:22px;
--fs-lead:16px; --lh-lead:24px; --fs-num:26px; --lh-num:32px;

/* NEW */
--fs-lbl:11px; --lh-lbl:16px; --fs-body:13px; --lh-body:20px;
--fs-lead:15px; --lh-lead:22px; --fs-num:26px; --lh-num:32px;
```

**Why the bump loses.** It is a near-no-op that manufactures sizes instead of removing them.
Only 11 declarations in the whole file read these tokens; 133 hardcode a literal. So the bump
changed almost nothing on screen, while pushing `--fs-lbl` to 12px against 53 hardcoded 11px
labels — a fifth size, created by the change that was supposed to enforce four.

**Why reverting costs nothing.** The concurrent process's own hand-tuned literals land exactly
on the *original* token values: `.zonelbl` 13, `.agmeta` 13, `table.ctab` 13, `table.ctab th`
11, `.skl` 11. Reverting the tokens and then pointing those literals at the tokens (§2, §3)
produces **zero** size change for any of them. `--fs-num` stays at 26px because `.skv` already
ships 26px and the focal number is not the problem — the floor is.

"Make it bigger" is delivered in §3.1 (title 12 → 15), §3.5 (floor 9/10 → 11), §3.6 (quotes
13 → 15) and §4 (pane 737px → 1113px) — not by inflating the body step.

---

## 2. Shared component CSS

These are global fixes for global bugs. Do not scope them to the two panes; they are broken in
every deck pane.

### 2.1 Table row rules — the biggest single win

**L243**
```css
/* OLD */ table.ctab td{padding:8px 14px 8px 0;border-bottom:1px solid var(--s2)}
/* NEW */ table.ctab td{padding:8px 14px 8px 0;border-bottom:1px solid var(--line)}
```
`--s2` #161616 **is** `.deck .pane`'s background. Every `.ctab` in every deck pane currently has
no row separation at all. `--line` #262626 is the intended value.

Add immediately after:
```css
table.ctab tr:last-child td{border-bottom:none}
```

### 2.2 `.acc` inside tables

**After L245** (`table.ctab .mut{color:var(--mut2)}`), add:
```css
table.ctab .acc{color:var(--accent)}
```
Makes "answering someone now" and "Npp better" amber, as the markup already asks. No markup
change, so `dashboard_aicrawl_test.go:62` (`Contains(body, "answering someone now")`) is
unaffected.

### 2.3 Collapse the duplicate `.n` rule

**L244-245**
```css
/* OLD */
table.ctab .n{font-variant-numeric:tabular-nums}
table.ctab .n{text-align:right}
/* NEW */
table.ctab .n{text-align:right;font-variant-numeric:tabular-nums}
```
Cosmetic only; two rules for one selector is how the next person misses one.

### 2.4 The other invisible `--s2` fills

**L340**
```css
/* OLD */ .seg:hover{background:var(--s2)}
/* NEW */ .seg:hover{background:var(--s1)}
```

**L850**
```css
/* OLD */ .sentbar{...;background:var(--s2);margin:6px 0 4px}
/* NEW */ .sentbar{...;background:var(--s1);margin:6px 0 4px}
```
`--s1` #111111 is darker than both the resting (`--s2`) and hovered (`--s3`) pane surface, so it
stays visible in both states. A lighter tint cannot do that without a fifth surface.

### 2.5 Snap shared literals onto the reverted tokens

```css
/* L241  OLD */ table.ctab{...font-size:13px;margin-top:10px}
/* L241  NEW */ table.ctab{...font-size:var(--fs-body);line-height:var(--lh-body);margin-top:10px}

/* L242  OLD */ table.ctab th{...font-size:11px;...}
/* L242  NEW */ table.ctab th{...font-size:var(--fs-lbl);line-height:var(--lh-lbl);...}

/* L231  OLD */ .zonelbl{...font-size:13px;...}
/* L231  NEW */ .zonelbl{...font-size:var(--fs-body);line-height:var(--lh-body);...}

/* L234  OLD */ .zonelbl .sub{...font-size:12px;font-weight:400;line-height:1.55;max-width:76ch;...}
/* L234  NEW */ .zonelbl .sub{...font-size:var(--fs-lbl);font-weight:400;line-height:var(--lh-lbl);max-width:72ch;...}

/* L753  OLD */ .agmeta{font-family:var(--mono);font-size:13px;line-height:1.65;color:var(--mut);max-width:80ch}
/* L753  NEW */ .agmeta{font-family:var(--mono);font-size:var(--fs-body);line-height:var(--lh-lead);color:var(--mut);max-width:68ch}

/* .skv  OLD */ .skv{...font-size:26px;...}
/* .skv  NEW */ .skv{...font-size:var(--fs-num);line-height:var(--lh-num);...}

/* .skl  OLD */ .skl{...font-size:11px;color:var(--mut);...}
/* .skl  NEW */ .skl{...font-size:var(--fs-lbl);line-height:var(--lh-lbl);color:var(--mut);...}

/* L412  OLD */ .exsummary{...font-size:13px;line-height:1.65;...max-width:80ch}
/* L412  NEW */ .exsummary{...font-size:var(--fs-body);line-height:var(--lh-lead);...max-width:68ch}

/* L740  OLD */ .inote{...font-size:13px;line-height:1.6;...max-width:80ch}
/* L740  NEW */ .inote{...font-size:var(--fs-body);line-height:var(--lh-lead);...max-width:68ch}
```

Net visual delta: `.zonelbl .sub` 12 → 11 (deliberate — widens the gap to its 13px heading),
measures 80ch → 68ch (at 13px mono, 68ch ≈ 530px; 80ch would run ~100 characters in the wider
pane from §4). Everything else is byte-identical in size and now reads from the ladder.

---

## 3. Per-pane CSS

Add one block at **L856**, after the `.gverb .gvmeta` rule and before `</style>`.

### 3.1 The card title — fix at the source

The problem is the shared override, so fix it there rather than adding an id-scoped patch.

**L637-638**
```css
/* OLD */
.deck .pane h3{margin:0 0 12px;font-size:12px;line-height:18px;text-transform:none;
  letter-spacing:0;color:var(--fg)}

/* NEW */
.deck>.pane>h3{margin:0 0 14px;padding-bottom:12px;border-bottom:1px solid var(--line2);
  font-size:var(--fs-lead);line-height:var(--lh-lead);text-transform:none;
  letter-spacing:-.01em;color:var(--fg)}
```

Three things happen. (a) 12px → 15px, so the card's name is finally larger than its own body
text. (b) `>` on both combinators is load-bearing: it stops this rule reaching the nested
`<h3>`s inside `.webcols` (L1527, L1532), which would otherwise render "prompts tracked" at
card-title size. (c) A closing hairline separates the title from the body. `font-family:mono`
and `font-weight:600` are inherited from `.pane h3` (L332) and do not need restating.

Both panes are direct children of `.deck` — confirmed by the fact that `.deck>.pane.wide`
currently gives them `span 2`.

### 3.2 The `.webcols` column heads

They now fall through to `.pane h3` = 15px **uppercase** mono, which is louder than the card
title. Give them the label tier:

```css
.deck .webcols h3{font-size:var(--fs-lbl);line-height:var(--lh-lbl);font-weight:500;
  text-transform:uppercase;letter-spacing:var(--track-lbl);color:var(--mut2);margin:0 0 10px}
```
Reading order becomes: title 15px --fg → section 13px --mut → column 11px --mut2 → rows.

### 3.3 The caveat prose — demote by shape, not by size

This is the sentence the founder quoted. It is not shrunk, not moved, not hidden. It gets a
measure and a hairline so it reads as an aside attached to the table above it rather than
another row of data.

```css
#pane-aivis div.agmeta,#pane-aicrawl div.agmeta{
  padding-left:14px;border-left:1px solid var(--line2);margin-top:14px}
```

`div.agmeta` is deliberate: the `<span class="agmeta">` inside `.agrow` (L1411, L1592) is an
inline provenance run and must **not** get a left rule. The element qualifier separates them
with no markup change.

Size and colour are already correct after §2.5 (13px, `--mut` = 5.24:1, `.agmeta b` at `--fg`).
The caveat ends up *more* legible than it is today, and out of the numbers' way.

### 3.4 The `--dis` AA failure

**L855**
```css
/* OLD */ .gverb .gvmeta{display:block;margin-top:5px;font:400 10px/1.4 var(--mono);color:var(--dis);...}
/* NEW */ .gverb .gvmeta{display:block;margin-top:8px;font-family:var(--mono);font-weight:400;
            font-size:var(--fs-lbl);line-height:var(--lh-lbl);color:var(--mut2);...}
```
10px → 11px, and 2.83:1 → 4.22:1. Note the `font:` shorthand is replaced with longhands on
purpose: `font:` resets `font-variant-numeric`, silently discarding the `tabular-nums` declared
on `:root`. Keep `text-transform:uppercase;letter-spacing:.06em` as they are.

### 3.5 Kill the 9px and 10px floor

**L838-840**
```css
/* OLD */ .sovax{...font-size:9px;color:var(--mut2);padding:4px 0 22px;text-align:right;width:22px}
/* NEW */ .sovax{...font-size:var(--fs-lbl);color:var(--mut2);padding:4px 0 22px;text-align:right;width:28px}
```
**L819** — the axis gutter must widen with it or "100" clips:
```css
/* OLD */ .sovwrap{position:relative;margin:10px 0 4px;padding-left:26px}
/* NEW */ .sovwrap{position:relative;margin:10px 0 4px;padding-left:34px}
```
**L834 / L845** — already 11px; change to `var(--fs-lbl)` for consistency, no visual delta:
```css
.crawlax{...font-size:var(--fs-lbl);...}
.sovleg span{...font-size:var(--fs-lbl);...}
```

After §2.5 and §3.5 these two panes use exactly **11 / 13 / 15 / 26** and nothing else.

### 3.6 The engine quotes

**L853-854**
```css
/* OLD */ .gverb{margin:0 0 8px;padding:8px 12px;...font:400 13px/1.5 var(--sans);color:var(--fg)}
/* NEW */ .gverb{margin:0 0 10px;padding:14px 16px;border-left:2px solid var(--a45);
            background:var(--well);border-radius:0 var(--r-ctl) var(--r-ctl) 0;
            font-family:var(--sans);font-weight:400;font-size:var(--fs-lead);
            line-height:var(--lh-lead);color:var(--fg);max-width:76ch}
```
15px is the declared "sentences" step and this is the only prose on the card that an engine
wrote rather than something we computed. Keep the amber left border and `--well` background —
that is the frozen provenance vocabulary, and `gverb` is in `behaviourClasses`.

---

## 4. Width — the "make it bigger" that actually matters

Add both ids to the existing `span 3` list **and to both clamps**. All three edits ship together.

**L617-618**
```css
/* OLD */
.deck>#pane-agent,.deck>#pane-sessions,.deck>#pane-retention,
.deck>#pane-events,.deck>#pane-paths{grid-column:span 3}
/* NEW */
.deck>#pane-agent,.deck>#pane-sessions,.deck>#pane-retention,
.deck>#pane-events,.deck>#pane-paths,
.deck>#pane-aivis,.deck>#pane-aicrawl{grid-column:span 3}
```

**L622-624** — the 1240px clamp, add the same two ids (falls back to `span 2`).

**L626-629** — the 640px clamp, add the same two ids (falls back to `1/-1`).

Leave `class="wide"` on both elements in the markup: the id rule (1,1,0) outranks
`.deck>.pane.wide` (0,2,0), and stripping the class is a pure-risk edit that buys nothing.

**Risk.** The comment at L619-621 states the failure mode: an item spanning more tracks than
exist creates an implicit column and side-scrolls the whole page. Missing either clamp
reproduces the previously-fixed horizontal-overflow bug. Verify `document.documentElement.scrollWidth
=== window.innerWidth` at 1440, 1240, 1060, 900, 640 and 390.

`.deck #pane-aivis,.deck #pane-aicrawl{overflow-x:auto}` (L852) **stays** — it is the inner
safety net that keeps the 7-column crawler table off the body at 390px.

Effect at 1440: pane content 701px → 1075px. The four `.sk` tiles fit one row instead of
wrapping, `.webcols` goes 2×340 → 2×527, and the pane gets materially shorter — which pays back
the vertical cost of the section rules.

---

## 5. Markup

Only one change, in `#pane-aicrawl`. Everything above is CSS.

`#pane-aicrawl` has **no section headings at all** — it is a title, four tiles, a chart, a
table, a paragraph and a table, every block at the same level. The `.zonelbl`/`.zsep` treatment
that makes `#pane-aivis` readable cannot apply to a pane with no `.zonelbl` in it.

**Before the crawlers table (L1605)**, add:
```html
<div class="zonelbl zsep" style="margin:0 0 8px">who fetched you
  <span class="sub">one row per crawler over the whole window · last seen is the most recent path it took</span></div>
```
and drop that table's inline `style="margin-top:14px"`.

**At L1619**, promote the bolded lead-in that is already doing a heading's job:
```html
<!-- OLD -->
<div class="agmeta" style="margin-top:14px"><b>Read by people, never read by an assistant.</b> No AI can cite a page it has never fetched, and these have proven human demand. Link to them from a page the crawlers do reach, or list them in your sitemap.</div>

<!-- NEW -->
<div class="zonelbl zsep" style="margin:0 0 8px">read by people, never read by an assistant</div>
<div class="agmeta">No AI can cite a page it has never fetched, and these have proven human demand. Link to them from a page the crawlers do reach, or list them in your sitemap.</div>
```
No word is deleted — the heading text *is* the sentence, and the remaining two sentences are the
action, which stays visible.

Keep both new elements **inside** the existing `data-empty="0"` wrapper. The quiet-card detector
(~L2400) inspects direct children of the pane; a new direct child with no `data-empty` would
break its emptiness inference.

---

## 6. Rejected

**Progressive disclosure of the caveats.** Three lenses proposed folding between two and six
caveat paragraphs into `<details>`. **Rejected.** After §3.3 the caveats have a measure, a
hairline and a colour step; they no longer compete with the numbers, which was the actual
complaint. Six new `<details>` would add six click targets and a new interaction idiom to save
roughly six lines, and every one of them sits one slip away from the dark pattern the brief
rules out. The founder asked for legibility, not fewer words. If the pane is still too long
after this ships, the one legitimate candidate is the **verbatims list** — repetitive data, not
load-bearing prose — behind a summary naming the exact remaining count. Not the caveats.

**A new lead sentence (`.agled`) restating the headline numbers as prose.** Rejected: it
duplicates the `.sk` tiles in words, invents copy no test covers, and is a product change
wearing a legibility costume.

**Raising `--mut2` (#7A7A7A, 4.22:1 on `--s2`) in `:root`.** Two lenses proposed #858585,
#9A9A9A. Rejected here: ~40 site-wide consumers, it collapses the gap to `--mut` (#8A8A8A) to
under half a stop, and the select-chevron data-URI hardcodes `fill='%237A7A7A'` and would
silently not follow. The one AA failure *in these panes* is `--dis` on `.gvmeta`, fixed by usage
in §3.4. Log the token separately.

**`.tscroll` wrappers with `tabindex="0"` on all five tables.** A real WCAG 2.1.1 finding — both
panes are horizontal scroll containers with zero focusable elements, so hidden columns are
unreachable by keyboard. But it is markup surgery on five tables plus five new tab stops, and
§4 removes most of the overflow it addresses. Deferred, listed in §8.

**Per-section bordered sub-cards, zebra striping, sticky sub-headings, hover tooltips for the
caveats.** All rejected. The comment at L606 records that "twenty-two identically-outlined
boxes WAS the clutter"; striping needs long lists and the only tint dark enough is `--s3`, which
is the pane's own hover colour; sticky cannot work inside an `overflow-x:auto` container; and
hover-only is unreachable on touch, which is exactly the failure the brief forbids for
load-bearing honesty text.

---

## 7. Test surface

`dashboard_inventory_test.go` is a `strings.Contains` check on the raw template, so **additions
are always safe** — only renames and removals fail. Nothing in this spec renames anything.

Frozen and untouched:

- `id="pane-aivis"` (in both `panes` and `jsWiredIds`), `id="pane-aicrawl"`, `data-empty`.
  Note `pane-aicrawl` is **not** in `dashboard_inventory.json` — it is the newer pane. Do not
  read that as licence to rename it; better, add it to `panes` in the same commit.
- `behaviourClasses` present in these panes: `gverb`, `seg`, `sentbar`, `sovleg`, `sovline`,
  `sovwrap`, `inote`, `agprov`, `aginf`, `agerr`, `agtool`, `col`, `chip`. All restyled by
  existing selector; none renamed. `zonelbl`, `agmeta`, `zsep`, `ctab`, `sk`/`skv`/`skl` are
  **not** frozen.
- `MENT of RUNS <span class="mut">(PCT%)</span>` — `dashboard_aivis_test.go:71-74` rebuilds this
  character-for-character from `/v1/ai-visibility`. Style the cell; never restructure it, never
  add a class, never reflow the whitespace.
- `>#3</div>` and `>#1</div><div class="skl">of 3 brands named` —
  `dashboard_aivis_test.go:217,220`. No element and no whitespace may be inserted between the
  `.skv` div and the `.skl` div.
- All empty-state copy: `$geo_check`, `/v1/events`, `model_version`, `claude-grounded`,
  `User-Agent`, `data-empty="1"`, "has recorded `<b>$geo_check</b>` events, but none in this
  window", "it carries no `<b>site</b>` or `<b>path</b>`", "a single bar is a reading, not a
  direction", "This starts by itself", "run no JavaScript", "$ai_crawl", "answering someone
  now", GPTBot / ChatGPT-User / OpenAI / Anthropic. Nothing in this spec touches any
  `data-empty="1"` branch.

The §5 markup change adds new copy. Run `go test ./internal/api/...` after; no current
assertion greps "who fetched you" or the split "Read by people" paragraph, and the sentences
that follow it are preserved verbatim.

`TestDashboardTemplateHygiene` fails on any `{{` inside an HTML comment — if you annotate the
edits, keep template actions out of comments.

---

## 8. What this does not fix

- **The panes are still long.** `#pane-aivis` measures ~2100px tall today. §4 shortens it
  materially (wider tracks, `.webcols` and `.stickrow` stop wrapping) but it will still be
  roughly two screens. Nothing here deletes a block, because nothing may be deleted. If length
  is the next complaint, the answer is the verbatims disclosure in §6 — not the caveats.
- **Keyboard-unreachable table columns.** Both panes remain `overflow-x:auto` containers with
  no focusable child, so at 390px the crawler table's `errors` and `last seen` columns cannot be
  reached without a pointer. WCAG 2.1.1. Deferred deliberately; needs the `.tscroll` wrapper
  work as its own change.
- **Charts have no text equivalent.** `.bars` puts its whole reading in a `.tip` span that is
  `display:none` until hover (removed from the a11y tree entirely), and `.crawlday` puts its
  reading in a bare `title=` on a role-less div. The file's own idiom for this is the visible
  `<details class="charttable">` at L1076. Not addressed here.
- **`--mut2` is 4.22:1 on `--s2` and 3.97:1 on the hovered `--s3`**, so every 11px caption on
  the page — not just in these panes — is below AA. The `:root` comment claiming "every one AA
  on its surface" is false. Needs its own pass (§6).
- **`.bars .ghost`** (`--ghost` #3A3A3A at `opacity:.45`) composites to ~1.20:1 on `--s2`. In
  `#pane-aivis` that ghost bar *is* the "recommended as a pick" series and the legend names it
  in words. Half the trend chart is effectively invisible to everyone. Out of scope here, but
  it is the highest-value next item.
- **The 133 hardcoded `font-size` declarations elsewhere in the file** still do not read the
  scale. §1 and §2.5 fix the tokens and these two panes; the rest of the dashboard still
  renders 9, 10 and 12px strays.
