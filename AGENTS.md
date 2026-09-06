# pageflip — agent instructions

Slide capture for screen-shared meetings (`pageflip`, Rust) plus a Go
orchestrator (`meetcat`) that runs Claude specialist sessions over the
slides. `README.md` describes the shipped behaviour and flags; keep it in
step with `src/main.rs`, `meetcat/main.go`, and the two `--help-agent` texts
(`src/help_agent.txt`, `src/bin/help_agent_feed.txt`, the `helpAgent`
constant in `meetcat/main.go`).

## Invariants (enforced by types, not convention)

- Raw audio never leaves `src/audio/`: `AudioSamples` is crate-private with
  no Serialize, file, or network path. Only transcript `Segment`s come out.
- Session logs (`src/session_log.rs`, `meetcat/log.go`) have no free-form
  content fields: numbers, enum codes, hashed identifiers only.
- Frames reach an egress endpoint only as `RedactedFrame`, which has no
  public constructor outside `src/redact/` (`src/policy.rs`).
- The web UI binds `127.0.0.1` only (`meetcat/web.go`).

## Commands

| Command | Notes |
|---|---|
| `make bullseye` | fmt, clippy `-D warnings`, `cargo build --release`, `cargo test --release`. CI runs the same on macos-14. Rust only. |
| `cd meetcat && go vet ./... && go test ./...` | Not in `make bullseye` or CI. Needs a claudia checkout at `../../claudia` relative to `meetcat/` (the `replace` in `meetcat/go.mod`). Because the replace points at a live checkout, both fail with `missing go.sum entry` whenever claudia has grown imports that `meetcat/go.sum` does not cover; refresh the sums before trusting a red result. |
| `cargo install --path .` | Installs `pageflip` and `pageflip-feed`. |
| `cd meetcat && go build -o meetcat` | `meetcat` is not in the Homebrew formula yet (🎯T32). |

Tests must use `--release` (rpath for `/usr/lib/swift`; see `build.rs`).

## Layout

| Path | Role |
|---|---|
| `src/main.rs` | `pageflip` CLI (clap), capture loop, `doctor` subcommand |
| `src/capture/` | ScreenCaptureKit / `screencapture` region and window capture (macOS) |
| `src/dedup.rs` | RMS pixel-difference dedup (replaced pHash in v0.2.0) |
| `src/picker.rs` | Region, window, crop, and multi-monitor pickers (winit + softbuffer) |
| `src/redact/` | Face blur, OCR, PII text redaction |
| `src/policy.rs` | App allowlist, paranoid saver, `RedactedFrame` egress gate |
| `src/audio/` | Audio capture and the WhisperX transcriber (`scripts/transcribe.py` via `uv`) |
| `src/events.rs` | `--events-out` NDJSON; schema in `docs/slide-event-schema.json` |
| `src/session_log.rs`, `src/doctor.rs` | Bug-report material |
| `src/bin/pageflip-feed.rs` | Headless feeder into one Claude Code session |
| `meetcat/` | Go module: `main.go` (flags, subcommands), `agents.go` (specialists), `web.go` (UI on 49831), `artifact.go`, `manifest.go` (resume), `glossary.go`, `resolver.go`, `revisits.go`, `doctor.go`, `log.go`, `prompts/`, `web/` |
| `meetcat/artifact.go` | Meeting-summary writer. Only `WriteSessionIDs` is on the shipped path (`main.go` `writeSessionIDsIfPossible`); `WriteArtifact` — the decisions/actions/open-questions/contradictions pass — has no caller outside tests, so those files are not produced by a run yet. |
| `scripts/` | `transcribe.py`, weekly digest and its launchd installer |
| `packaging/pageflip.rb` | Formula template; the live copy is in the tap and is written by `/release` |
| `docs/` | `bug-report.md`, `weekly-digest.md`, `slide-event-schema.json`, `audit-log.md` |

## Conventions

- Every source file starts with the Apache-2.0 SPDX header.
- New capabilities are new targets in `bullseye.yaml` (use the bullseye
  tools, never hand-edit). Prompts change in `meetcat/prompts/*.md` and their
  in-code defaults together (`agents.go` says they must match).
- Examples and repro steps in issues and docs stay generic: the use case is
  confidential meetings.

## Delivery

Squash-merged to `master` through a PR with green CI; releases via
`/release`, which also updates the Homebrew tap.
