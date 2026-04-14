# pageflip

Poll a screen region every few seconds and save a PNG whenever the image
changes meaningfully. Designed for capturing slides from live screen-shared
meetings (Teams, Zoom, Meet) without having to record and post-process the
whole video.

Uses a perceptual hash ([`ImageHash.phash`][phash]) to decide whether a new
frame is "different enough" from the last one saved, so compression noise and
subtle cursor movements don't produce duplicate captures.

[phash]: https://github.com/JohnDoe/imagehash

## Install

```bash
uv tool install git+https://github.com/marcelocantos/pageflip
```

Or for a local checkout:

```bash
git clone https://github.com/marcelocantos/pageflip
cd pageflip
uv tool install .
```

On first run, macOS will prompt to grant **Screen Recording** permission to
your terminal (System Settings → Privacy & Security → Screen Recording).

## Use

Interactive region picker, default cadence and threshold:

```bash
pageflip
```

Draw a rectangle around the slide area. Capture begins immediately; Ctrl-C
to stop. Output lands in `./pageflip-YYYYMMDD-HHMMSS/`.

With an explicit region (pixels, after any Retina scaling):

```bash
pageflip --region 400,200,1280,720 --interval 2 --threshold 10
```

## Options

| Flag | Default | Notes |
|---|---|---|
| `-i`, `--interval` | `2.0` | Seconds between captures. |
| `-t`, `--threshold` | `10` | Hamming-distance threshold. Lower → more captures. |
| `-r`, `--region` | *(interactive)* | `x,y,w,h` in screen pixels. |
| `-o`, `--output` | `./pageflip-<timestamp>/` | Output directory. |
| `--hash-size` | `16` | pHash size; 16 → 256-bit hash. |

## Tuning the threshold

The hash is 256 bits (`--hash-size 16`). Hamming distance between two hashes
roughly tracks how different the images look:

- `< 5` — near-identical, compression noise only
- `5–10` — cursor movement, small UI changes, animation mid-frame
- `10–20` — new slide, new diagram, substantial content change
- `> 20` — completely different content

Default threshold of `10` works well for most slide decks. Raise it if you
get duplicates from animated slides; lower it if subtle slide changes (e.g. a
new bullet appearing) are being missed.

## How it works

Every `--interval` seconds:

1. Grab the region via `mss`.
2. Compute a perceptual hash.
3. Compare against the hash of the **last saved** frame (not the last
   captured frame) — so a slow fade-in still resolves to one capture.
4. If the Hamming distance exceeds the threshold, save as PNG and update the
   reference hash.

## License

Apache 2.0. Copyright 2026 Marcelo Cantos.
