# pageflip

Capture the slides of a screen-shared meeting as they change, and run a set
of Claude specialist sessions over them in real time.

Two binaries do the work:

- **`pageflip`** polls a screen region or window on a timer and writes a PNG
  whenever the picture changes meaningfully. Dedup is a mean per-channel RMS
  pixel difference against the last saved frame (`src/dedup.rs`); cursor
  drift and compression noise stay well under the default threshold.
- **`meetcat`** spawns `pageflip`, reads its slide-event stream, and feeds
  each slide to persistent Claude Code specialist sessions (skeptic,
  constructive, neutral, dejargoniser, contradictions), shows their output in
  a local web UI, and keeps a per-meeting folder so a meeting can be resumed.

A third, `pageflip-feed`, is a thin headless alternative to meetcat that
pushes each new PNG into one existing Claude Code session.

macOS only. Audio (optional) is transcribed locally and never written to disk
or sent anywhere; see "The audio invariant" in
[`CONTRIBUTING.md`](CONTRIBUTING.md).

## Installation

```bash
brew install marcelocantos/tap/pageflip
```

The formula installs `pageflip` and `pageflip-feed`. `meetcat` is not in the
formula yet (🎯T32); build it from a checkout:

```bash
cargo install --path .            # pageflip + pageflip-feed
cd meetcat && go build -o meetcat # needs a claudia checkout, see Development
```

### Gatekeeper and permissions

Release binaries are not code-signed. If macOS refuses to open them, clear
the quarantine attribute once per install:

```bash
xattr -d com.apple.quarantine "$(which pageflip)" "$(which pageflip-feed)"
```

Grant **Screen Recording** (System Settings → Privacy & Security) on first
run; `pageflip` exits with a readable error if it is denied. `--audio` also
needs Microphone permission.

## Usage

### pageflip

```bash
pageflip                                        # interactive region picker
pageflip --region 400,200,1280,720 --interval 2 # fixed region, slower cadence
pageflip --window-title "Teams" --output ~/meetings/slides
pageflip --window --crop                        # pick a window, then drag a crop
pageflip --list-windows                         # print id, app, title and exit
pageflip --events-out stdout                    # NDJSON slide events instead of paths
pageflip doctor > report.md                     # paste-safe diagnostics
```

Defaults are read from `src/main.rs`.

| Flag | Default | Notes |
|---|---|---|
| `--region X,Y,W,H` | interactive picker | Logical screen points; PNGs are written at physical resolution. |
| `--window` / `--window-title SUBSTRING` / `--window-id ID` / `--list-windows` | — | Window targets; mutually exclusive with each other and `--region`. |
| `--crop` | off | After a window target resolves, drag a crop on a snapshot; stored as fractions so it tracks resizes. |
| `--interval SECS` | `1.0` | Capture cadence. |
| `--threshold RMS` | `5.0` | Mean per-channel RMS difference (0–255 scale) needed to save a frame. |
| `--output DIR` | `./pageflip-<timestamp>/` | Where PNGs go. |
| `--redact-faces` | off | Blur faces found by Apple Vision before writing. |
| `--redact-pii` | off | Mask OCR-detected emails, phones, cards, government IDs, IPs. |
| `--paranoid DIR` | — | Also keep the unredacted originals in DIR. |
| `--audio` | off | Capture system audio and transcribe locally. |
| `--transcriber KIND` | `null` | `null` discards samples; `whisperx` runs `scripts/transcribe.py` via `uv`. |
| `--whisperx-model MODEL` | `large-v3` | Whisper model for `whisperx`. |
| `--diarize` | off | Add `speaker_id` via pyannote (needs the gated model weights). |
| `--events-out SPEC` | — | `stdout`, `fd:N`, or a path; one JSON object per saved slide, schema in `docs/slide-event-schema.json`. Without it, one absolute PNG path per line on stdout. |
| `--log-file PATH` | — | Content-free NDJSON session log for bug reports. |
| `--help-agent` | — | Machine-oriented help. |

`pageflip doctor [--log PATH] [--meetcat-log PATH] [--no-meetcat]` prints a
markdown report and, when `meetcat` is on `PATH`, appends `meetcat doctor`.

### meetcat

```bash
meetcat                                  # picker → pageflip → specialists → web UI
meetcat --window-title "Teams" --work-dir ~/meetings
meetcat --specialists skeptic,neutral    # subset of specialists
pageflip --events-out stdout | meetcat --no-spawn
meetcat resume meetcat-1234              # re-spawn a past meeting's sessions
meetcat glossary refresh --confluence-url https://co.atlassian.net/wiki
meetcat doctor [--log-file PATH]
```

By default meetcat spawns `pageflip` itself (forwarding `--region`,
`--window`, `--window-title`, `--window-id`) and starts one Claude Code
session per specialist: skeptic, constructive, and neutral on Sonnet,
dejargoniser on Haiku, contradictions on Opus. Prompts live in
`meetcat/prompts/*.md`. `--no-agents` runs decode-only; `--no-spawn` reads
slide events from stdin. The web UI is served on `http://127.0.0.1:49831/`
(loopback only) and opened in the browser only when the specialists are
enabled *and* stderr is a TTY; otherwise output goes to stderr.

Each meeting gets a `meetcat-<id>/` folder under `--work-dir` (default `.`).
`meeting.json` is written at startup — it is what `meetcat resume` reads —
and `session-ids.json` at meeting stop. Slide PNGs stay in pageflip's own
output directory (a `pageflip-<timestamp>/` folder under the same working
directory), which is where the web UI serves them from; they are not copied
into the meeting folder. The summary artifact (`decisions.md`, `actions.md`,
`open-questions.md`, `contradictions.md`, `references.json`, transcript copy,
slide symlinks) is implemented in `meetcat/artifact.go` but is not yet wired
into a meeting run, so it is not produced today.

meetcat has no audio flag of its own and does not forward one to pageflip;
run `pageflip --audio` directly for transcription. The glossary cache
defaults to `~/.pageflip/glossary.json`; `--web-dir` serves the UI from a
directory instead of the embedded copy (development only).

Dependencies at runtime: the `claude` CLI, `tmux`, and Anthropic auth
(`meetcat doctor` reports all three).

### pageflip-feed

```bash
pageflip --window-title "Teams" --output /tmp/slides      # terminal 1
pageflip-feed --watch /tmp/slides --session-id <session>   # terminal 2
pageflip --events-out stdout | pageflip-feed --pipe --session-id <session>
```

| Flag | Default | Notes |
|---|---|---|
| `--watch DIR` | — | Watch for new PNGs (FSEvents). Exclusive with `--pipe`. |
| `--pipe` | off | Read paths or NDJSON events from stdin. |
| `--session-id ID` | required | Claude Code session to `--resume`. |
| `--prompt TEMPLATE` | `Analyse this slide: @{path}` | `{path}` is the PNG path. |
| `--claude PATH` | `claude` | The `claude` binary. |

### Weekly digest

`scripts/meetings-digest.sh <meetings-dir>` summarises the last seven days of
meetcat meeting folders with `claude -p` (it needs bash 4+ and the summary
files that the artifact pass does not yet write — see the status note in the
digest doc);
`scripts/install-digest-cron.sh --meetings-dir DIR` installs it as a Sunday
launchd job. Details in [`docs/weekly-digest.md`](docs/weekly-digest.md).

## Development

```bash
make bullseye        # cargo fmt --check, clippy -D warnings, build --release, test --release
cd meetcat && go vet ./... && go test ./...
```

`make bullseye` and CI (`.github/workflows/ci.yml`, macos-14) cover the Rust
crate only; the Go tests are run separately. Tests use `--release` because the
debug build's rpath does not find `/usr/lib/swift`, which ScreenCaptureKit
needs (`build.rs` patches the release rpath).

`meetcat/go.mod` replaces `github.com/marcelocantos/claudia` with
`../../claudia`, so a claudia checkout must sit beside the pageflip repo
directory (`~/work/github.com/marcelocantos/claudia` for the standard
layout); without it `go build` and `go test` fail at setup.

WhisperX transcription and diarisation need HuggingFace model weights fetched
once; the list is in [`CONTRIBUTING.md`](CONTRIBUTING.md). Interaction-surface
stability is catalogued in [`STABILITY.md`](STABILITY.md); ongoing work is in
`bullseye.yaml`.

## Reporting bugs

`pageflip doctor` and `meetcat doctor` print markdown diagnostics that exclude
meeting content, window titles, OCR, and transcripts by construction. Paste
them into the [bug report template](.github/ISSUE_TEMPLATE/bug_report.yml);
the full flow is in [`docs/bug-report.md`](docs/bug-report.md).

## License

Apache 2.0. Copyright 2026 Marcelo Cantos.
