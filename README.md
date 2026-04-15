# pageflip

Poll a screen region (or window) every few seconds and write a PNG whenever the
image changes meaningfully. Designed to feed live slides from screen-shared
meetings into a Claude Code session for real-time analysis.

Uses a perceptual hash (pHash Hamming distance) to skip frames that differ only
in cursor movement, compression noise, or animation mid-state.

## Installation

```bash
brew install marcelocantos/tap/pageflip
```

This installs both `pageflip` (capture loop) and `pageflip-feed` (Claude feeder).

For development from a local checkout:

```bash
cargo install --path .
```

## Gatekeeper on macOS

Pre-built release binaries are not code-signed yet (signing requires an Apple
Developer account — tracked as future work). If macOS blocks the binary with
"cannot be opened because the developer cannot be verified", remove the
quarantine attribute:

```bash
xattr -d com.apple.quarantine "$(which pageflip)" "$(which pageflip-feed)"
```

You need to do this once per install. The error appears because the binaries
were downloaded from the internet, not installed from the App Store or a signed
package. There is no security risk for software you built or installed yourself.

Also grant **Screen Recording** permission on first run: System Settings →
Privacy & Security → Screen Recording. pageflip exits with a readable error
message if permission is denied.

## Usage

Capture a region interactively (draws a rubber-band overlay):

```bash
pageflip
```

Capture a fixed region:

```bash
pageflip --region 400,200,1280,720 --interval 2 --threshold 10
```

Capture a specific window by title substring, writing output to a custom directory:

```bash
pageflip --window-title "Teams" --output ~/meetings/slides
```

Pick a window interactively:

```bash
pageflip --window
```

List available windows and their IDs:

```bash
pageflip --list-windows
```

Capture and feed each new slide into an active Claude Code session:

```bash
# Terminal 1: start capturing
pageflip --window-title "Teams" --output /tmp/slides

# Terminal 2: feed each new PNG into Claude
pageflip-feed --watch /tmp/slides --session-id <session-id>
```

Find your session ID with `claude --list-sessions`.

### Flag reference

#### pageflip

| Flag | Default | Notes |
|---|---|---|
| `--region X,Y,W,H` | *(interactive picker)* | Top-left x,y + width,height in logical screen points. |
| `--window` | — | Interactive window picker. |
| `--window-title SUBSTRING` | — | Attach to the first window whose title contains this. |
| `--window-id ID` | — | Attach to the window with this numeric ID. |
| `--list-windows` | — | Print visible windows and exit. |
| `--interval SECS` | `2.0` | Capture cadence in seconds. |
| `--threshold N` | `10` | Minimum pHash Hamming distance to save a frame. |
| `--output DIR` | `./pageflip-<timestamp>/` | Output directory. |

#### pageflip-feed

| Flag | Default | Notes |
|---|---|---|
| `--watch DIR` | *(required)* | Directory to watch for new PNGs. |
| `--session-id ID` | *(required)* | Claude Code session ID to resume. |
| `--prompt TEMPLATE` | `Analyse this slide: @{path}` | Prompt sent per slide. `{path}` is replaced with the PNG path. |
| `--claude PATH` | `claude` | Path to the `claude` binary. |

## Development

`make bullseye` is the standing invariant gate — it runs `cargo fmt --check`,
`cargo clippy --release -- -D warnings`, `cargo build --release`, and
`cargo test --release`. All four must be green before any merge.

```bash
make bullseye
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the full contributor workflow,
including model-weight bootstrap and the audio invariant.

## Reporting bugs

`pageflip doctor` prints a markdown diagnostic (versions, permission states,
model cache inventory, recent session tail) designed to be pasted into a
GitHub issue. It deliberately excludes meeting content, window titles, OCR
text, and transcript text — safe to paste.

```bash
pageflip doctor > report.md
meetcat doctor >> report.md   # if applicable
```

File issues using the [Bug report template](.github/ISSUE_TEMPLATE/bug_report.yml);
it prompts for these fields. See [`docs/bug-report.md`](docs/bug-report.md)
for the end-to-end flow.

## License

Apache 2.0. Copyright 2026 Marcelo Cantos.
