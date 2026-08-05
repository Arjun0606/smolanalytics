#!/usr/bin/env python3
"""Rasterise the mark to PNG at the sizes browsers and app stores actually ask for.

Written by hand against the same 16x16 grid the SVG comes from, with no rasteriser in the loop.
Every output size is an integer multiple of 16, so each source pixel becomes an exact NxN block —
no antialiasing, no resampling, no half-pixel fringe. Running the SVG through a general rasteriser
would soften the edges at precisely the sizes (16, 32) where softness is the whole problem.

No third-party dependencies: PNG is just zlib-compressed scanlines plus a CRC, and taking on Pillow
for that would be a dependency in a repo whose entire pitch is a single static binary.
"""

import binascii
import re
import struct
import sys
import zlib
from pathlib import Path

HERE = Path(__file__).parent
INK = (0x17, 0x14, 0x0D)
YELLOW = (0xFF, 0xC9, 0x00)


def grid():
    src = (HERE / "mklogo.py").read_text()
    g = re.search(r'GRID = """\n(.*?)\n"""', src, re.S).group(1).splitlines()
    assert len(g) == 16 and all(len(r) == 16 for r in g), "the grid drifted from 16x16"
    return g


def chunk(tag, data):
    return (struct.pack(">I", len(data)) + tag + data
            + struct.pack(">I", binascii.crc32(tag + data) & 0xFFFFFFFF))


def png(rows, w, h):
    """rows: list of h rows, each a list of w (r,g,b,a) tuples."""
    raw = b"".join(b"\x00" + b"".join(bytes(px) for px in row) for row in rows)
    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 6, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw, 9))
            + chunk(b"IEND", b""))


def render(size, pad_bg=None):
    """size must be a multiple of 16. pad_bg fills transparent pixels (for maskable/apple icons,
    which get a rounded-rect mask applied and look broken with transparency)."""
    assert size % 16 == 0, f"{size} is not a multiple of 16 — that would resample"
    g, scale = grid(), size // 16
    lut = {"K": (*INK, 255), "Y": (*YELLOW, 255),
           ".": ((*pad_bg, 255) if pad_bg else (0, 0, 0, 0))}
    rows = []
    for gy in range(16):
        row = []
        for gx in range(16):
            row.extend([lut[g[gy][gx]]] * scale)
        rows.extend([row] * scale)
    return png(rows, size, size)


def ico(sizes=(16, 32, 48)):
    """A real multi-size .ico, because some crawlers and older browsers still fetch /favicon.ico
    directly and ignore the <link> tags entirely."""
    imgs = [render(s) for s in sizes]
    out = struct.pack("<HHH", 0, 1, len(imgs))
    offset = 6 + 16 * len(imgs)
    for s, data in zip(sizes, imgs):
        out += struct.pack("<BBBBHHII", s % 256, s % 256, 0, 0, 1, 32, len(data), offset)
        offset += len(data)
    return out + b"".join(imgs)


TARGETS = {
    "favicon-16.png": (16, None),
    "favicon-32.png": (32, None),
    "favicon-48.png": (48, None),
    "icon-192.png": (192, None),
    "icon-512.png": (512, None),
    # Apple applies a rounded-rect mask and composites on WHITE, so a transparent-cornered icon
    # gets black fringing on some iOS versions. Fill the corners with the yellow instead.
    "apple-touch-icon.png": (192, YELLOW),
}

if __name__ == "__main__":
    for name, (size, bg) in TARGETS.items():
        (HERE / name).write_bytes(render(size, bg))
        print(f"{name:26} {size}x{size}")
    (HERE / "favicon.ico").write_bytes(ico())
    print(f"{'favicon.ico':26} 16+32+48")
