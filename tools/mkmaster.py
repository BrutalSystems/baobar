#!/usr/bin/env python3
"""Render assets/icon/baobar-*.png from assets/icon/baobar.svg.

One-time asset tooling, NOT part of the build. Run it only when the SVG
changes; the PNGs it writes are committed and are what tools/genappicon
consumes. It needs Google Chrome and Pillow, neither of which is a project
dependency — that is the whole point of committing the output.

Chrome does the rasterising because the SVG carries its fills in a CSS
<style> block, which the pure-Go rasterisers ignore: they would render an
unfilled shape and look like a bug in the artwork rather than in the tool.

Three files are written, not one. The mark is inset from the tile edge, and a
single inset cannot serve every size: 10% reads well at 1024 and wastes pixels
at 16, where the bun ends up small inside its own tile. genappicon prefers an
explicit baobar-<n>.png when one exists and downscales the master otherwise.

    python3 tools/mkmaster.py
"""

import base64
import pathlib
import subprocess
import sys
import tempfile

from PIL import Image, ImageDraw

CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
ROOT = pathlib.Path(__file__).resolve().parent.parent
ICON = ROOT / "assets" / "icon"

GROUND = (0xF2, 0xE3, 0xC6)   # cream
RADIUS_PCT = 0.18             # tile corner radius
SS = 4096                     # supersample before downsampling

# size -> fraction of the tile the mark spans. Smaller tiles give the mark
# more room because there are fewer pixels to spend on empty ground.
TARGETS = {1024: 0.80, 32: 0.88, 16: 0.92}


def rasterise(svg: pathlib.Path, px: int) -> Image.Image:
    """Render svg at px square on a transparent ground, via headless Chrome."""
    with tempfile.TemporaryDirectory() as tmp:
        tmp = pathlib.Path(tmp)
        html = tmp / "wrap.html"
        out = tmp / "raw.png"
        body = svg.read_text().split("\n", 1)[1]  # drop the <?xml?> declaration
        html.write_text(
            "<style>html,body{margin:0;padding:0;background:transparent}"
            f"svg{{display:block;width:{px}px;height:{px}px}}</style>\n{body}"
        )
        subprocess.run(
            [CHROME, "--headless", "--disable-gpu", "--hide-scrollbars",
             "--default-background-color=00000000",
             f"--window-size={px},{px}", f"--screenshot={out}", f"file://{html}"],
            check=True, capture_output=True,
        )
        return Image.open(out).convert("RGBA").copy()


def compose(mark: Image.Image, size: int, span: float) -> Image.Image:
    """Centre mark on a rounded cream tile and return it at size square."""
    tile = Image.new("RGBA", (SS, SS), (0, 0, 0, 0))
    ImageDraw.Draw(tile).rounded_rectangle(
        [0, 0, SS - 1, SS - 1], radius=int(SS * RADIUS_PCT), fill=GROUND + (255,))
    w = int(SS * span)
    h = int(w / (mark.width / mark.height))
    tile.alpha_composite(mark.resize((w, h), Image.LANCZOS),
                         ((SS - w) // 2, (SS - h) // 2))
    return tile.resize((size, size), Image.LANCZOS)


def main() -> int:
    svg = ICON / "baobar.svg"
    if not svg.exists():
        print(f"no such file: {svg}", file=sys.stderr)
        return 1
    if not pathlib.Path(CHROME).exists():
        print(f"Chrome not found at {CHROME}", file=sys.stderr)
        return 1

    raw = rasterise(svg, 2048)
    mark = raw.crop(raw.getbbox())   # trim the SVG's own generous margins
    print(f"mark {mark.width}x{mark.height} (aspect {mark.width / mark.height:.3f})")

    for size, span in sorted(TARGETS.items(), reverse=True):
        path = ICON / f"baobar-{size}.png"
        compose(mark, size, span).save(path)
        print(f"wrote {path.relative_to(ROOT)}  mark spans {span:.0%} of the tile")
    return 0


if __name__ == "__main__":
    sys.exit(main())
