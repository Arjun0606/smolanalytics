# UI CRAFT SPEC (2026-07-15)

## token block

```css
:root{
  /* ============ SURFACES — keep the ladder (audit: "essentially correct"), name the strays ============ */
  --bg:#0A0A0A;        /* page canvas — never pure #000 (black crush) */
  --well:#0D0D0D;      /* code wells (pre.code, .ccode) — was a rogue hex used twice, now a token */
  --s1:#111111;        /* card / control fill (default raised surface) */
  --s2:#161616;        /* hover fill, +1 step */
  --s3:#1C1C1C;        /* popover / tooltip fill, +2 steps */
  --line:#262626;      /* decorative hairline (cards, zone rules) — 1.31:1, matches Linear's 1.36:1 */
  --line2:#2E2E2E;     /* control border (affordance carried by FILL+border, per WCAG 1.4.11 pattern) */
  --line3:#3E3E3E;     /* hover border (Geist rule: default border -> hover border is one ladder step) */

  /* ============ TEXT — 3 reading tones + 1 disabled; every one AA on its surface ============ */
  --fg:#EDEDED;        /* primary — 16.91:1 on bg (never #FFF: halation next to amber) */
  --mut:#8A8A8A;       /* secondary — 5.73:1 on bg, 5.47:1 on s1: safe on cards AND canvas */
  --mut2:#7A7A7A;      /* meta/labels ON CANVAS ONLY — 4.61:1 on bg (AA). WAS #5F5F5F = 3.10:1 FAIL */
  --dis:#5F5F5F;       /* genuinely disabled elements only — never data, never labels */

  /* ============ AMBER — one accent, a real ladder instead of 10 hand-picked alphas ============ */
  --accent:#F5A623;     /* base — 9.77:1 on bg (AAA as text; the identity) */
  --accent-hi:#FFC24D;  /* hover/bright — 12.33:1 */
  --accent-dim:#C77F0A; /* pressed — 6.10:1 */
  --accent-bar:#C98A1E; /* chart bar fill, SOLID — 6.73:1; kills the opacity:.5 muddy-olive composite */
  --a14:rgba(245,166,35,.14);  /* focus halo + row washes (the one alpha the audit praised) */
  --a25:rgba(245,166,35,.25);  /* share-bar fills (segbar/exfill/cbar) */
  --a45:rgba(245,166,35,.45);  /* share-bar leading edge + chip borders */
  --warn:#E5544B;      /* status only — 5.38:1 */
  --ok:#3FB27F;        /* status only — 7.43:1 */
  --ghost:#3A3A3A;     /* prior-period comparison bars, SOLID — was #5F5F5F@.35 = invisible 1.3:1 */
  --grid:#1C1C1C;      /* chart gridlines, 1px SOLID — one step off canvas, replaces #161616@1.13:1 */

  /* ============ TYPE — exactly FOUR sizes (the file's own covenant, now enforced).
     Kills 10/12/14/20/22/30px. Weights 400/500/600 only (no 700/800/900 anywhere). ============ */
  --fs-lbl:11px;  --lh-lbl:16px;   /* uppercase micro-labels, ticks, meta — floor size, never 10px */
  --fs-body:13px; --lh-body:20px;  /* rows, cells, sentences — dashboard consensus size */
  --fs-lead:15px; --lh-lead:22px;  /* ask input, verdict lines, answers, card questions (merges 14+15) */
  --fs-num:24px;  --lh-num:30px;   /* ALL big numbers: glance tiles, goals, DAU/MAU (merges 20/22/30) */
  --track-lbl:.06em;   /* uppercase 11px labels get positive tracking */
  --track-num:-.02em;  /* 24px numerals tighten (Linear rule: tracking deepens as size grows) */
  --track-body:-.01em; /* sans body only; mono stays 0 */

  /* ============ SPACE — 4px base; allowed steps 4/8/12/16/24/32/48. Kills 14/18/26/34. ============ */
  --sp1:4px; --sp2:8px; --sp3:12px; --sp4:16px; --sp6:24px; --sp8:32px;
  --zone:24px;      /* THE section gap — one value between every zone (was 14/16/18/20/26) */
  --card-pad:16px;  /* THE card padding (was 16x18 / 20 / 12x16 / 10x16 / 10x14 / 8x11 / 13) */
  --gap:16px;       /* THE grid gap (deck, board, webcols) */

  /* ============ RADII — three, nested always smaller than parent. Kills 3/5/6. ============ */
  --r-ctl:4px;   /* controls, kbd, table bars, tooltips, row hovers */
  --r-card:8px;  /* cards, ask input, code wells */
  --r-pill:999px;/* chips, pills, scrollbar thumbs */

  /* ============ CONTROL METRICS — four heights replace eleven (17/20/21/22/23/25/27/29/32/50) ============ */
  --chip-h:24px; /* pills/chips — WCAG 2.5.8 minimum target */
  --ctl-h:28px;  /* every select, input, button */
  --tab-h:32px;  /* tab buttons */
  --ask-h:46px;  /* the one exempt hero control */
  --row-h:32px;  /* data rows (.seg, table cells) — Carbon "short", Plausible ROW_HEIGHT */

  /* ============ MOTION — two speeds + one slow, ease-out only, nothing >200ms in-app ============ */
  --t-fast:100ms;  /* hover micro-feedback (bar fills, color) */
  --t-base:150ms;  /* borders, reveals, tab fades (the file's .15s was right — now universal) */
  --t-slow:200ms;  /* journey drawer slide only */
  --ease:cubic-bezier(.4,0,.2,1);        /* Geist default */
  --ease-out:cubic-bezier(.215,.61,.355,1); /* Linear ease-out-cubic — entrances */

  /* ============ FOCUS ============ */
  --ring:0 0 0 3px var(--a14); /* soft halo (ask bar keeps it) */
  /* keyboard ring is outline-based — see companion rule below */

  /* ============ FONTS — only names that actually resolve; Windows fallback added.
     (Inter + JetBrains Mono were referenced but never loaded — deleted for determinism.) ============ */
  --mono:ui-monospace,"SF Mono",SFMono-Regular,Menlo,Consolas,"Cascadia Mono",monospace;
  --sans:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;
  font-variant-numeric:tabular-nums;
}

/* ---- companion globals (ship with the token block) ---- */
:where(a,button,select,input,summary,[tabindex]):focus-visible{outline:2px solid var(--accent);outline-offset:2px}
:where(a,button,select,input,summary):focus:not(:focus-visible){outline:none}
.ask input:focus-visible{outline:none} /* the halo (--ring) is its indicator */
@media (prefers-reduced-motion:reduce){*,*::before,*::after{animation-duration:.01ms!important;animation-iteration-count:1!important;transition-duration:.01ms!important}}
@keyframes rise{from{opacity:0;transform:translateY(2px)}to{opacity:1;transform:none}}
@keyframes fadein{from{opacity:0}}
@keyframes slidein{from{transform:translateX(24px);opacity:0}}
```

## zone changes

- ROOT/BODY — body font stays 13px but gets letter-spacing:var(--track-body) (-0.01em); line-height:1.5 -> 20px via --lh-body on rows; DELETE body{overflow-x:hidden} (it masks bugs — overflow now scrolls inside units: .dbody, .pane .live, .ccode, pre.code). Font stacks: delete never-loaded 'Inter'/'JetBrains Mono', add Consolas/'Cascadia Mono' so Windows doesn't render Courier New.

- HEADER — .logo: sans 800/900 -> font-family:var(--mono);font-weight:600 (mono IS the brand; 800+900 on adjacent glyphs was two fake hierarchy levels); .hlink: 17px-tall bare links -> padding:6px 4px;margin:-6px -4px (28px hit target, header stays 48px); .livepill/.agentpill: 23px -> height:var(--chip-h) 24px, padding:0 10px; delete .bar{overflow:hidden} (was silently clipping) -> @media(max-width:880px){.bar{flex-wrap:wrap;padding:6px 0}}; .siteselect joins the control kit (28px). Drop the '⚡' from the Cursor button and replace obstatus '○' with a styled .dot span — no emoji/dingbats in chrome.

- ASK HERO — .hero padding 18px 0 4px -> 16px 0 0; .ask{max-width:760px;margin:0 auto} -> max-width:none (THE alignment fix: hero joins the single 214px left edge — page no longer switches alignment systems mid-scroll); input: 50px tall/8px radius -> height:var(--ask-h) 46px, padding:0 84px 0 40px, font:400 15px/22px var(--mono), border-radius:var(--r-card), keep border #2E2E2E + focus halo var(--ring) (the audit's one praised focus style).

- SUGGESTION CHIPS — three centered wrap-rows (~120px, pushing chart below fold) -> ONE left-aligned rail: merge the 3 .askchips divs into one #askchips with flex-wrap:nowrap;overflow-x:auto;scrollbar-width:none;justify-content:flex-start;gap:8px;padding:12px 0 0. Chips become <button type="button" class="chip" data-q> (JS uses closest('.chip[data-q]') — unchanged) so they're finally Tab-focusable.

- ANSWER CARD — .answer max-width:760px centered -> max-width:none (alignment edge); padding 16px 18px -> var(--card-pad) 16px; reveal snap -> animation:rise var(--t-base) var(--ease-out); add min-height:64px while 'computing…' so the reveal never jumps (CLS guard).

- TOOLBAR — becomes the STICKY control bar from MATRIX: position:sticky;top:0;z-index:30;background:var(--bg); margin:16px 0 0;padding:8px 0;gap:8px 12px; MERGE .chiprail's children (fchips, #fmode, #fadd, #fbuilder) into the same row and delete the second row (saves ~45px of fold). All controls inside snap to the kit: rangebtn/date inputs/go were 25/22/20px tall with 11/10px text -> all 28px/12px. .ranges wrapper: border var(--line2), radius var(--r-ctl), height 28px.

- VERDICT — .verdict max-width:860px centered -> max-width:none;margin:var(--zone) 0 0; .vline 14px -> font-size:var(--fs-lead) 15px/22px (kills the 14-vs-15 twin lead sizes); .vwhy 12px -> 11px mono + tabindex="0" role="button" (was mouse-only); .vdot pulse gated by prefers-reduced-motion; .vsep/.weekline summary #5F5F5F -> var(--mut2) #7A7A7A (AA).

- GLANCE — grid minmax(150px,1fr) (7 tiles orphan-wrapped into 6 cols with 843px of dead black) -> repeat(auto-fit,minmax(126px,1fr)) — all 7 fit one row at 1012px; .gtile .v 30px/700 -> font:600 var(--fs-num)/var(--lh-num) var(--mono);letter-spacing:var(--track-num) (uniform 24px; DELETE the Engaged tile's inline style="font-size:22px;padding-top:6px" — '42s' now fits the shared token); .skv 20px and .gnum 22px -> same 24px/600 token (one 'big number' everywhere); labels stay 11px uppercase but #5F5F5F -> var(--mut2); amber rule: only the conversion RATE keeps .acc — 'From AI' count -> fg (counts are neutral, rates earn accent); margin 18px -> var(--zone).

- CHART 1 — bars var(--accent)@opacity:.5 (composites to olive #805817) -> background:var(--accent-bar) #C98A1E SOLID, hover -> background:var(--accent) via transition:background var(--t-fast) (kills the 2x luminance snap); ghost bars #5F5F5F@.35 (1.3:1 invisible) -> var(--ghost) #3A3A3A solid; gridlines border-top #161616 (1.13:1) -> 1px solid var(--grid) #1C1C1C; .yaxis: width:36px fixed, font 10px #5F5F5F -> 11px var(--mut2), and Go emits the middle tick (add TrendMid = TrendMax/2 in dashboard.go near :714 — the empty .ymid was a three-line scale with an unlabeled middle); x ticks 10px -> 11px var(--mut2); DELETE title="click: who is behind this bar" from .col -> aria-label (was double-tooltipping over the custom .tip); .tip: radius 5 -> var(--r-ctl) 4px, bg var(--s3), 11px, value-first ordering.

- HOUR CHART — currently bars start at x=214 while chart 1's start at 252 (no shared edge) and its axis is a fully inline-styled div -> wrap in the same .chartwrap with a real .yaxis (add HoursMax to the Go viewmodel; 36px gutter shared), give the axis div class .xaxis, delete every inline style, margin-top inline 18px -> var(--zone) via .chartzone+.chartzone{margin-top:var(--zone)}.

- CHART TABLE — table.ctab td/th get height:var(--row-h) via padding:6px 12px 6px 0 + 20px line-height; th 10px #5F5F5F -> 11px var(--mut2) 500; .cbar div fill rgba(245,166,35,.35) -> var(--a25), radius 2 -> var(--r-ctl) 4px; numerics already right-aligned — keep.

- BOARD — .boardgrid gap 14px -> var(--gap) 16px; .bcard padding 16x18 -> var(--card-pad); .bq stays 15px = --fs-lead; .bx gets padding:4px 6px (hit target) ; .bbody min-height:96px so pin/unpin never reflows neighbours.

- BREAKDOWNS -> DECK — see tab_rail_verdict for the full layout; margins 18px -> var(--zone); .seg rows: keep 32px height + 4px gap (already the Plausible recipe) but border:1px var(--line) + bg var(--s1) -> border:none;background:transparent;border-radius:var(--r-ctl) with .seg:hover{background:var(--s2)} (row wash, not border flash); .segbar rgba washes -> var(--a14) fill + 2px var(--a45) edge; .seg[data-fp] gets tabindex="0" role="button" + one delegated Enter handler (drill-down was mouse-only); .segnums right-aligned mono 12 -> 13px to match --fs-body? NO — segnums stay mono 12px is dead: unify to var(--fs-body) 13px mono (body size, mono voice).

- FUNNEL — .fbarwrap height 40px keep (funnel steps may breathe wider than rows); .fbar gradient -> keep but tokenize: linear-gradient(90deg,var(--a25),var(--a14)), edge 2px var(--accent); .fdrop 11px stays but #E5544B on bg fine; title attr -> aria-label; radius 5 -> var(--r-ctl).

- RETENTION HEAT — THE broken-by-construction fix, server-side in /Users/arjun/smolanalytics/internal/api/dashboard.go:670: Style becomes fmt.Sprintf("background:rgba(245,166,35,%.2f);color:%s", a, ternary(a>=0.45, "#0A0A0A", "#EDEDED")) — light text below the 0.45-alpha flip point (measured: 27% cell was 1.92:1, unreadable); CSS default table.heat td{color:var(--fg)}; th 11px var(--mut2).

- LIVE + EVENTS — the 6,282px events pane (footer teleported 6,160px on tab switch) -> .pane .live{max-height:400px;overflow-y:auto;scrollbar-width:thin;scrollbar-color:var(--line2) transparent} + panes get min-height:220px; tab switch snap -> .pane.on{animation:rise var(--t-base) var(--ease-out)}; row font stays 12px mono -> 13px (--fs-body).

- POWER TOOLS (explore/cohorts/defined) — .ptool padding 16x18 -> var(--card-pad); all .exrow controls join the kit (were 32px/12px — now 28px/12px); primary Run/Create buttons: font-weight 700 -> 600, bg var(--accent), color #0A0A0A, hover bg var(--accent-hi), active bg var(--accent-dim); .exrow input/select focus outline:none -> DELETED (global :focus-visible ring takes over); margin 26px -> var(--zone).

- CONNECT SHEET — .cbtn 29px -> 28px kit; .copy 25px -> 28px kit; .ccode bg #0D0D0D -> var(--well), radius 5 -> var(--r-ctl), padding 8x11 -> 6px 12px; .cnote #5F5F5F -> var(--mut2); margin 26px -> var(--zone).

- ONBOARDING — .obcard padding 20px -> var(--card-pad); .obh 15px = --fs-lead ✓; pre.code bg -> var(--well), radius 6 -> var(--r-card) 8px inside 8px card? NO — nested radius must be smaller: var(--r-ctl) 4px; .obsub #5F5F5F -> var(--mut2).

- DEVNOTE — padding 10x14 -> 12px 16px; radius 0 6 6 0 -> 0 var(--r-card) var(--r-card) 0; keep the 3px amber left bar (good pattern).

- FOOTER — text-align:center (the last centered orphan) -> text-align:left on the shared 214px edge; padding 34/28 -> 32px 0 40px; add border-top:1px solid var(--line); color #5F5F5F -> var(--mut2) #7A7A7A (was 3.10:1, now 4.61:1); line-height 2 -> 1.8. Keep 'updated {{.Updated}} · page computed in {{.ComputeMS}}ms' — it is the freshness/provenance trust cue, now legible.

- JOURNEY DRAWER — appears with animation:slidein var(--t-slow) var(--ease-out); .jshade{animation:fadein var(--t-base) var(--ease)}; padding 20 -> var(--card-pad); .jclose gets padding:4px 6px target; row meta #5F5F5F -> var(--mut2).

- GO-SIDE DATA POLISH (/Users/arjun/smolanalytics/internal/api/dashboard.go) — (a) thousands separators: format Visitors/Pageviews/TotalUsers/Signups and table counts with comma grouping via a 6-line comma(int) helper (1379 -> 1,379; the footer already formats ms correctly); (b) TrendMid for the y-axis middle tick; (c) HoursMax for the hour chart y-label; (d) retention cell color threshold from the heat fix.

- INLINE-STYLE PURGE — delete all ~20 style="" attributes and cover each with kit/zone rules: #chartBreak's from-scratch inline select -> class="zonesel" (id kept for JS); chart2 margin-top:18px -> CSS sibling rule; h3 style="margin-top:16px" -> .dcard h3:not(:first-child){margin-top:16px}; zonelbl inline sub spans -> .sub class; exrow margin-bottom inline -> .mb0 utility; Engaged tile font-size inline -> deleted (shared 24px token). Only Go-computed widths/heights (segbar width, bar height, heat cell style) remain inline — they are data, not style.


## control kit

```css
/* =====================================================================
   CONTROL KIT — one spec per species, mapped onto EXISTING classes/ids
   (nothing renamed: every selector below is already in the template/JS).
   Four heights total: 24 chip · 28 control · 32 tab · 46 ask.
   ===================================================================== */

/* ---------- 1 · FIELD: every select + text/date input except the ask hero ---------- */
.siteselect,.zonesel,#chartBreak,
.fbuilder input,.fbuilder select,
.customrange input,
.exrow input,.exrow select{
  -webkit-appearance:none;appearance:none;
  height:var(--ctl-h);padding:0 10px;
  background:var(--s1);border:1px solid var(--line2);border-radius:var(--r-ctl);
  color:var(--fg);font:400 12px/26px var(--mono);letter-spacing:0;
  color-scheme:dark;min-width:0;max-width:none;margin:0;
  transition:border-color var(--t-base) var(--ease),background var(--t-base) var(--ease);
}
.siteselect:hover,.zonesel:hover,#chartBreak:hover,
.fbuilder input:hover,.fbuilder select:hover,
.customrange input:hover,
.exrow input:hover,.exrow select:hover{border-color:var(--line3)}
::placeholder{color:var(--mut2)}

/* selects get one shared chevron (appearance:none removed the native one) */
.siteselect,.zonesel,#chartBreak,.fbuilder select,.exrow select{
  padding-right:26px;
  background-image:url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%238A8A8A' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'><path d='m6 9 6 6 6-6'/></svg>");
  background-repeat:no-repeat;background-position:right 8px center;background-size:12px 12px;
}

/* ---------- 2 · BUTTONS: one secondary, one primary ---------- */
/* secondary (default): filled one surface step up — the fill, not the border, is the affordance */
.rangego,.abtn,.copy,.cbtn,.exrow button.btn2{
  height:var(--ctl-h);padding:0 12px;display:inline-flex;align-items:center;gap:6px;
  background:var(--s2);border:1px solid var(--line2);border-radius:var(--r-ctl);
  color:var(--fg);font:500 12px/1 var(--mono);letter-spacing:0;cursor:pointer;margin:0;
  text-decoration:none;
  transition:background var(--t-base) var(--ease),border-color var(--t-base) var(--ease);
}
.rangego:hover,.abtn:hover,.copy:hover,.cbtn:hover,.exrow button.btn2:hover{
  background:var(--s3);border-color:var(--line3);color:var(--fg)}
.rangego:active,.abtn:active,.copy:active,.cbtn:active,.exrow button.btn2:active{background:var(--s1)}
.abtn.pinned,.cbtn.primary{border-color:var(--a45);color:var(--accent);background:var(--s1)}

/* primary (amber — exactly one per row, ever) */
.exrow button{ /* #exRun,#gCreate,#coCreate,#deCreate — .btn2 above overrides the save/secondary */
  height:var(--ctl-h);padding:0 14px;display:inline-flex;align-items:center;
  background:var(--accent);border:1px solid var(--accent);border-radius:var(--r-ctl);
  color:#0A0A0A;font:600 12px/1 var(--mono);cursor:pointer;margin:0;
  transition:background var(--t-base) var(--ease);
}
.exrow button:hover{background:var(--accent-hi);border-color:var(--accent-hi)}
.exrow button:active{background:var(--accent-dim);border-color:var(--accent-dim)}
.exrow button[disabled]{opacity:.65;cursor:default}

/* ---------- 3 · CHIP / PILL: ask chips, filter chips, live + agent pills, proof ---------- */
.chip,.fchip,.livepill,.agentpill,.proof{
  height:var(--chip-h);display:inline-flex;align-items:center;gap:6px;
  padding:0 10px;border-radius:var(--r-pill);
  font:400 11px/1 var(--mono);letter-spacing:0;cursor:pointer;margin:0;
  background:transparent;border:1px solid var(--line);color:var(--mut);
  text-decoration:none;white-space:nowrap;
  transition:color var(--t-base) var(--ease),border-color var(--t-base) var(--ease),background var(--t-base) var(--ease);
}
.chip:hover{color:var(--accent);border-color:var(--a45)}
.fchip{border-color:var(--a45);color:var(--accent)}          /* active filters read amber */
.fchip.fmode{border-color:var(--line);color:var(--mut)}
.fchip.fmode:hover,.agentpill:hover{color:var(--accent);border-color:var(--a45)}
.livepill.on .dot,.agentpill.on::before{background:var(--accent)}
.proof{border-color:var(--a45);color:var(--accent);font-size:10px;text-transform:uppercase;letter-spacing:.05em;padding:0 8px}
.proof:hover{background:var(--accent);color:var(--bg)}
.saved .chip{border-radius:var(--r-pill)}                    /* kill the 6px variant — one chip, one radius */
.saved .chip .x{padding:4px}                                 /* removable-x hit area */

/* ---------- 4 · TAB ---------- */
.tabbtn{
  height:var(--tab-h);padding:0 12px;background:transparent;border:0;
  border-bottom:2px solid transparent;margin-bottom:-1px;border-radius:0;
  color:var(--mut);font:500 12px/32px var(--mono);letter-spacing:.01em;cursor:pointer;
  transition:color var(--t-base) var(--ease);
}
.tabbtn:hover{color:var(--fg)}
.tabbtn.on{color:var(--fg);border-bottom-color:var(--accent)}

/* ---------- 5 · THE ONE EXEMPT CONTROL: the ask hero ---------- */
.ask input{
  width:100%;height:var(--ask-h);padding:0 84px 0 40px;
  background:var(--s1);border:1px solid var(--line2);border-radius:var(--r-card);
  color:var(--fg);font:400 var(--fs-lead)/22px var(--mono);outline:none;
  transition:border-color var(--t-base) var(--ease),box-shadow var(--t-base) var(--ease);
}
.ask input:focus{border-color:var(--accent);box-shadow:var(--ring)}

/* ---------- 6 · kbd (rides along with the kit) ---------- */
kbd{border:1px solid var(--line2);border-bottom-width:2px;border-radius:var(--r-ctl);
  padding:0 5px;font:400 11px/16px var(--mono);color:var(--mut)}
```

## tab rail verdict

VERDICT: KILL the 13-tab rail as the access path; a 2-tab remnant survives only for the two stream panes. Rationale from the measurements: (a) the rail sits at y=1023 — every breakdown, the funnel disciplines, and the retention modes occupy ZERO above-fold pixels, which is exactly the evaluator's 'underwhelming despite deep functionality' complaint; (b) a FULL tile grid is impossible — pane-events measures 6,282px, so 'always visible everything' would detonate the page; (c) the JS makes a partial kill free: show() only toggles panes whose <button data-tab> exists inside #tabrail (panes are fetched globally by id), so any pane whose button is removed becomes inert static markup — promotion to always-visible costs ZERO JS changes.

REPLACEMENT LAYOUT — the breakdown deck:
.deck{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:var(--gap);margin:var(--zone) 0 0}
@media(max-width:820px){.deck{grid-template-columns:1fr}}
.dcard{background:var(--s1);border:1px solid var(--line);border-radius:var(--r-card);padding:var(--card-pad);height:344px;display:flex;flex-direction:column;min-width:0}
.dcard.tall{height:auto;min-height:344px}   /* content-sized: funnel, retention, stream */
.dcard.wide{grid-column:1/-1}
.dh{display:flex;align-items:baseline;justify-content:space-between;gap:8px;margin:0 0 12px;font:500 var(--fs-lbl)/var(--lh-lbl) var(--mono);text-transform:uppercase;letter-spacing:var(--track-lbl);color:var(--mut);flex:none}
.dbody{flex:1;min-height:0;overflow-y:auto;scrollbar-width:thin;scrollbar-color:var(--line2) transparent}
.dbody::-webkit-scrollbar{width:8px}.dbody::-webkit-scrollbar-thumb{background:var(--line2);border-radius:var(--r-pill)}
.dcard .webcols{grid-template-columns:1fr;gap:var(--card-pad)}  /* half-width cards stack their columns */

CARD MAP (each former pane KEEPS its id, swaps class \"pane\" -> \"dcard\"; its <h3> content moves into .dh; rows into .dbody):
Row 1: #pane-pages (top pages + entry pages stacked, scrolls) | #pane-referrers — the two highest-value breakdowns land directly under the chart, first row peeks above the 900px fold.
Row 2: #pane-geo | #pane-devices (device/os/browser stacked).
Row 3 (conditional, render only when data exists): #pane-campaigns | #pane-sources | #pane-search — grid auto-flows; an odd remainder card just fills its cell.
Row 4: #pane-funnel (.tall — 40px steps breathe) | #pane-conversion, or #pane-goals when conversion is absent.
Row 5: #pane-retention (.wide.tall — the heat table needs full 1012px width at 30/90 periods).
Row 6 — THE STREAM (.wide.tall): one card whose .dh contains the surviving <div class=\"tabrail\" id=\"tabrail\"> with exactly two buttons — data-tab=\"live\" and data-tab=\"events\" — followed by #pane-live and #pane-events which KEEP class=\"pane\" (they alone still toggle). Their bodies get max-height:400px;overflow-y:auto — the 6,160px footer-teleport is gone; 32px rows show ~12 events with the rest scrolling in-unit.

COMPAT PROOF: #tabrail, .tabbtn, pane-* ids, hash #tab=, and the livepill handler (show('live') + scrollIntoView(#pane-live)) all still resolve; btns[0]='live' becomes the default pane; legacy #tab=pages URLs fall back to 'live' while pages is permanently visible anyway. Density: ~1,100px of formerly hidden breakdown content becomes always-visible inside uniform card chrome — density up, and calmer because every card shares one header style, one row spec, one radius.

## build order

1. 1 · TOKENS + GUARDS (dashboard.tmpl.html <style> lines 13-19): replace :root with the token block, add the focus-visible pair, reduced-motion guard, and the three keyframes (rise/fadein/slidein). Nothing visible breaks — old hexes still parse; this is the foundation commit.
1. 2 · CONTROL KIT (same <style> block + light template edits): paste the kit CSS AFTER the existing component rules so it wins the cascade, then delete the superseded per-component control declarations (.siteselect, .zonesel, .customrange input/.rangego, .fbuilder input/select, .exrow input/select/button, .abtn, .copy, .cbtn, .chip, .fchip, .livepill, .agentpill, .proof, .tabbtn, kbd, .ask input); convert ask chips to <button type="button">; strip #chartBreak's inline style to class="zonesel". 27 control specs -> 5.
1. 3 · TYPE + SPACE NORMALIZATION: apply the four-size ladder (glance .v/.skv/.gnum -> 24px/600; .vline -> 15px; all 10px -> 11px; all 12px meta -> 11px or 13px per zone list), weights 700/800/900 -> 600, every zone margin -> var(--zone) 24px, every card padding -> var(--card-pad) 16px, radii -> 4/8/999. Delete the Engaged tile's inline font-size.
1. 4 · COLOR + CONTRAST (CSS + /Users/arjun/smolanalytics/internal/api/dashboard.go): --mut2 consumers audit (on-card small text -> var(--mut)); chart bars -> solid var(--accent-bar) with --t-fast hover to var(--accent); ghost -> var(--ghost); gridlines -> var(--grid); heat-cell text flip at alpha 0.45 (dashboard.go:670); thousands-comma helper for glance/table counts; TrendMid + HoursMax viewmodel fields for the axis ticks.
1. 5 · ALIGNMENT + FOLD: left-align the hero stack (.ask/.askchips/.answer/.verdict max-width:none), merge the three chip rails into one scrollable row, merge .chiprail into the now-sticky .toolbar, glance grid -> minmax(126px,1fr) (orphan fix), hour chart wrapped in .chartwrap with the shared 36px y-gutter. Verify: one 214px left edge from logo to footer; chart bottom lands ~640px so deck row 1 peeks above the 900px fold.
1. 6 · THE DECK (template restructure, zero JS edits): move the eleven promoted pane divs out of the tab cycle into <div class="deck"> per the card map; delete their buttons from #tabrail leaving live+events; add .dh/.dbody wrappers; retention + stream cards get .wide.tall; stream bodies capped at 400px scroll.
1. 7 · INTERACTION PASS: remove native title attrs that doubled the custom tooltips (.col, .fbarwrap, .seg -> aria-label); add tabindex="0" role="button" to .seg[data-fp] and .vwhy plus ONE delegated Enter-key handler (additive JS, ~4 lines); pane/answer rise animation; journey drawer slidein; custom thin scrollbars on every in-unit scroller; hit-target paddings (.hlink, .bx, .jclose, .saved .chip .x).
1. 8 · VERIFY + SHIP: flyctl deploy, then screenshot https://smolanalytics-demo.fly.dev at 1440x900 against the checklist — exactly 4 font sizes / 4 control heights / 3 radii in computed styles, one left edge, WCAG spot-checks (mut2 4.61:1, heat cells >=4.5:1, bars 6.73:1), full keyboard walk (Tab shows a ring on every control, Enter drills a row), legacy deep-link #tab=live still lands, and the footer no longer moves when switching live<->events.