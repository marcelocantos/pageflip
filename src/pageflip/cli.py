# Copyright 2026 Marcelo Cantos
# SPDX-License-Identifier: Apache-2.0
"""pageflip — poll a screen region and save a frame whenever it changes."""

from __future__ import annotations

import argparse
import sys
import time
import tkinter as tk
from datetime import datetime
from pathlib import Path

import imagehash
import mss
from PIL import Image


def pick_region() -> tuple[int, int, int, int]:
    """Show a fullscreen transparent overlay; return (left, top, width, height) in screen pixels."""
    root = tk.Tk()
    root.attributes("-fullscreen", True)
    root.attributes("-alpha", 0.3)
    root.attributes("-topmost", True)
    root.configure(bg="black")

    # Tk reports coordinates in points; mss wants pixels. On Retina displays
    # these differ by a factor of 2 (or more on scaled resolutions).
    scale = root.winfo_fpixels("1i") / 72.0

    canvas = tk.Canvas(root, cursor="crosshair", bg="black", highlightthickness=0)
    canvas.pack(fill="both", expand=True)

    state: dict = {"rect": None, "x0": 0, "y0": 0, "x1": 0, "y1": 0, "done": False}

    def on_down(e: tk.Event) -> None:
        state["x0"], state["y0"] = e.x, e.y
        state["rect"] = canvas.create_rectangle(e.x, e.y, e.x, e.y, outline="red", width=2)

    def on_drag(e: tk.Event) -> None:
        if state["rect"] is not None:
            canvas.coords(state["rect"], state["x0"], state["y0"], e.x, e.y)

    def on_up(e: tk.Event) -> None:
        state["x1"], state["y1"] = e.x, e.y
        state["done"] = True
        root.destroy()

    def on_cancel(_e: tk.Event) -> None:
        root.destroy()

    canvas.bind("<Button-1>", on_down)
    canvas.bind("<B1-Motion>", on_drag)
    canvas.bind("<ButtonRelease-1>", on_up)
    root.bind("<Escape>", on_cancel)

    root.mainloop()

    if not state["done"]:
        print("Region selection cancelled.", file=sys.stderr)
        sys.exit(1)

    left = int(min(state["x0"], state["x1"]) * scale)
    top = int(min(state["y0"], state["y1"]) * scale)
    width = int(abs(state["x1"] - state["x0"]) * scale)
    height = int(abs(state["y1"] - state["y0"]) * scale)
    return left, top, width, height


def parse_region(s: str) -> tuple[int, int, int, int]:
    parts = s.split(",")
    if len(parts) != 4:
        raise argparse.ArgumentTypeError("region must be 'x,y,w,h'")
    try:
        x, y, w, h = (int(p) for p in parts)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("region values must be integers") from exc
    return x, y, w, h


def main() -> None:
    p = argparse.ArgumentParser(
        prog="pageflip",
        description=__doc__,
    )
    p.add_argument(
        "-i", "--interval", type=float, default=2.0,
        help="seconds between captures (default: 2.0)",
    )
    p.add_argument(
        "-t", "--threshold", type=int, default=10,
        help="hamming-distance threshold above which a frame counts as changed (default: 10)",
    )
    p.add_argument(
        "-r", "--region", type=parse_region, default=None,
        metavar="X,Y,W,H",
        help="region in screen pixels; omit for interactive picker",
    )
    p.add_argument(
        "-o", "--output", type=Path, default=None,
        help="output directory (default: ./pageflip-YYYYMMDD-HHMMSS)",
    )
    p.add_argument(
        "--hash-size", type=int, default=16,
        help="pHash size; 16 → 256-bit hash (default: 16)",
    )
    args = p.parse_args()

    if args.region is None:
        print("Draw a rectangle around the slide area. Esc to cancel.", file=sys.stderr)
        # Give the terminal a moment so the overlay appears on top cleanly.
        time.sleep(0.3)
        left, top, width, height = pick_region()
    else:
        left, top, width, height = args.region

    if width <= 0 or height <= 0:
        print("Empty region.", file=sys.stderr)
        sys.exit(1)

    out = args.output or Path(f"pageflip-{datetime.now():%Y%m%d-%H%M%S}")
    out.mkdir(parents=True, exist_ok=True)

    bbox = {"left": left, "top": top, "width": width, "height": height}
    print(f"Region:    {width}x{height} @ ({left},{top})", file=sys.stderr)
    print(f"Output:    {out}", file=sys.stderr)
    print(f"Interval:  {args.interval}s", file=sys.stderr)
    print(f"Threshold: {args.threshold} bits", file=sys.stderr)
    print("Ctrl-C to stop.\n", file=sys.stderr)

    last_hash = None
    saved = 0
    try:
        with mss.mss() as sct:
            while True:
                loop_start = time.monotonic()
                shot = sct.grab(bbox)
                img = Image.frombytes("RGB", shot.size, shot.rgb)
                h = imagehash.phash(img, hash_size=args.hash_size)

                if last_hash is None:
                    dist_str = "   —"
                    changed = True
                else:
                    dist = h - last_hash
                    dist_str = f"{dist:4d}"
                    changed = dist >= args.threshold

                ts_now = datetime.now()
                if changed:
                    saved += 1
                    fname = f"slide-{saved:03d}-{ts_now:%Y%m%d-%H%M%S}.png"
                    img.save(out / fname)
                    last_hash = h
                    print(f"★ {ts_now:%H:%M:%S}  Δ={dist_str}  {fname}", file=sys.stderr, flush=True)
                else:
                    print(f"· {ts_now:%H:%M:%S}  Δ={dist_str}", file=sys.stderr, flush=True)

                # Sleep for the remainder of the interval so capture cost doesn't drift the cadence.
                elapsed = time.monotonic() - loop_start
                time.sleep(max(0.0, args.interval - elapsed))
    except KeyboardInterrupt:
        print(f"\nCaptured {saved} slide(s) to {out}", file=sys.stderr)


if __name__ == "__main__":
    main()
