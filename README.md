# pageflip

Watch screen-shared slides during a meeting and run multiple Claude
specialists over each slide in parallel — notes, questions, jargon
glossary, fact-check, contradictions — then surface the output in a
live web UI.

`pageflip` polls a screen region (or window), de-duplicates frames
that differ only in cursor movement or animation noise, and dispatches
each unique slide to a pool of specialist agents. Output streams into
a browser tab as the meeting progresses.

## Installation

```bash
brew install marcelocantos/tap/pageflip
```

This installs the `pageflip` orchestrator (what you run) and its
internal helper `pageflip-capture` (the screen-capture engine that
`pageflip` spawns as a subprocess).

For development from a local checkout:

```bash
make bullseye   # build + test both halves
go build -o ~/.local/bin/pageflip ./meetcat
cargo install --path .
```

## Gatekeeper on macOS

Pre-built release binaries are not code-signed yet (signing requires
an Apple Developer account — tracked as future work). If macOS blocks
the binary with "cannot be opened because the developer cannot be
verified", remove the quarantine attribute:

```bash
xattr -d com.apple.quarantine "$(which pageflip)" "$(which pageflip-capture)"
```

You need to do this once per install. The error appears because the
binaries were downloaded from the internet, not installed from the
App Store or a signed package. There is no security risk for software
you built or installed yourself.

Also grant **Screen Recording** permission on first run: System
Settings → Privacy & Security → Screen Recording. `pageflip-capture`
exits with a readable error message if permission is denied.

## Usage

Run against the active meeting. `pageflip` spawns `pageflip-capture`
under the hood, so you only need one command:

```bash
pageflip
```

Capture a fixed region:

```bash
pageflip --region 400,200,1280,720
```

Capture a specific window by title substring:

```bash
pageflip --window-title "Teams"
```

Pick a window interactively:

```bash
pageflip --window
```

The web UI opens automatically in the default browser. Specialist
output appears under each slide as it arrives.

### Flag reference

| Flag | Default | Notes |
|---|---|---|
| `--region X,Y,W,H` | *(interactive picker)* | Top-left x,y + width,height in logical screen points. Forwarded to `pageflip-capture`. |
| `--window` | — | Interactive window picker. Forwarded to `pageflip-capture`. |
| `--window-title SUBSTRING` | — | Attach to the first window whose title contains this. Forwarded to `pageflip-capture`. |
| `--window-id ID` | — | Attach to the window with this numeric ID. Forwarded to `pageflip-capture`. |
| `--no-spawn` | — | Don't spawn the capture engine; read slide events from stdin instead. Useful for piping pre-recorded streams. |
| `--log-file PATH` | — | Append structured NDJSON session log to PATH. |
| `--web-dir PATH` | *(embedded)* | Dev-only: serve HTML/CSS/JS from this directory instead of the embedded copy. |

## Architecture

`pageflip` is a Go orchestrator. It owns:

- The web UI (embedded HTML/JS, served on a localhost port).
- The specialist pool — parallel Claude Code sessions per slide.
- The slide manifest (resume, glossary cache, artefact directory).

It spawns `pageflip-capture` (a Rust binary) as a subprocess. The
capture engine owns:

- Screen region / window picking, pHash-based deduplication, OCR via
  Apple Vision.
- The NDJSON event stream that the orchestrator consumes on stdout.

This split exists because macOS screen-capture APIs are most
ergonomic from Rust, while the orchestrator benefits from Go's
concurrency primitives and embedded web server. Users only ever
invoke `pageflip` directly; `pageflip-capture` is an implementation
detail.

A third binary, `pageflip-feed`, is shipped for now but will be
retired (see 🎯T34). If you have an existing workflow that calls
`pageflip-feed` directly, it still works.

## Development

`make bullseye` is the standing invariant gate. It runs `cargo fmt
--check`, `cargo clippy --release -- -D warnings`, `cargo build
--release`, `cargo test --release`, and the Go orchestrator's
`go build` + `go test`. All must be green before any merge.

```bash
make bullseye
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the full contributor
workflow, including model-weight bootstrap and the audio invariant.

## Reporting bugs

`pageflip doctor` prints a markdown diagnostic (versions, permission
states, model cache inventory, recent session tail) designed to be
pasted into a GitHub issue. It deliberately excludes meeting
content, window titles, OCR text, and transcript text — safe to
paste.

```bash
pageflip doctor > report.md
```

File issues using the [Bug report
template](.github/ISSUE_TEMPLATE/bug_report.yml); it prompts for
these fields. See [`docs/bug-report.md`](docs/bug-report.md) for the
end-to-end flow.

## License

Apache 2.0. Copyright 2026 Marcelo Cantos.
