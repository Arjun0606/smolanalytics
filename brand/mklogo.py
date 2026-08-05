#!/usr/bin/env python3
"""Emit the smolanalytics mark as a true pixel-grid SVG.

Hand-authored rather than traced from the generated PNG, because a favicon is read at 16px and a
resampled raster turns to mush at exactly the size that matters most. Every pixel here is an
integer rect on a 16x16 grid, so the mark is crisp at 16, 32, 180 and 512 alike, and it stays a
few hundred bytes instead of a few kilobytes.

Grid legend:
  .  transparent
  K  ink (black)      — border, the S, the ears
  Y  yellow           — the tile
"""

# 16x16. Ears occupy rows 0-1; the bordered tile is rows 2-15.
#
# A YELLOW GAP separates the S from the border on every side. Without it the S's top bar sits
# flush against the frame and the two fuse into one black mass — rendered at 128px, that version
# stopped reading as an S at all. Counter-shapes are what make a letterform legible, so the gap
# is structural, not padding.
#
# The ears get THREE rows and taper 1-3-5, because two rows of straight-sided ear read as a
# calendar tab or a battery terminal — both earlier attempts were rendered at 16px and looked at,
# and both failed exactly that way. An ear is recognised by its taper, so the taper is the part
# that cannot be economised on.
#
# The S is deliberately BLOCKY with a 2px stroke: a thin or curved S disappears at 16px, and this
# mark has to survive being a browser-tab icon before it has to look good on a hero.
GRID = """
...K........K...
..KKK......KKK..
.KKKKK....KKKKK.
KKKKKKKKKKKKKKKK
KYYYYYYYYYYYYYYK
KYYKKKKKKKKKKYYK
KYYKKKKKKKKKKYYK
KYYKKYYYYYYYYYYK
KYYKKKKKKKKKKYYK
KYYKKKKKKKKKKYYK
KYYYYYYYYYYKKYYK
KYYKKKKKKKKKKYYK
KYYKKKKKKKKKKYYK
KYYYYYYYYYYYYYYK
KYYYYYYYYYYYYYYK
KKKKKKKKKKKKKKKK
""".strip().splitlines()

INK = "#17140d"     # warm near-black, shared with the reference sites
YELLOW = "#ffc900"  # the yellow both reference sites use

def runs(row, ch):
    """Merge horizontal runs of one colour into single rects — fewer, larger nodes."""
    out, x = [], 0
    n = len(row)
    while x < n:
        if row[x] == ch:
            start = x
            while x < n and row[x] == ch:
                x += 1
            out.append((start, x - start))
        else:
            x += 1
    return out

def build(size=16):
    parts = []
    for colour, ch in ((YELLOW, "Y"), (INK, "K")):
        d = []
        for y, row in enumerate(GRID):
            for x, w in runs(row, ch):
                d.append(f"M{x} {y}h{w}v1h-{w}z")
        parts.append(f'<path fill="{colour}" d="{"".join(d)}"/>')
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" '
        f'width="{size}" height="{size}" shape-rendering="crispEdges" '
        f'role="img" aria-label="smolanalytics">'
        + "".join(parts) + "</svg>"
    )

if __name__ == "__main__":
    import sys
    assert all(len(r) == 16 for r in GRID), [len(r) for r in GRID]
    assert len(GRID) == 16, len(GRID)
    svg = build()
    sys.stdout.write(svg)
