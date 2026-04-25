# Audit Log

## 2026-04-26 — /release v0.2.0

- **Commit**: `pending`
- **Outcome**: First release after pageflip's "real product" milestone.
  Ships the web UI replacement for the bubbletea TUI, RMS-based dedup
  (replacing pHash), `meetcat resume` with manifest persistence,
  multi-monitor picker, single-binary entry point (meetcat spawns
  pageflip), and many other improvements across 52 commits. Homebrew
  formula updated.
- **Notes**: `release_readiness: pre-product` in CLAUDE.md flipped to
  `ready` as part of this release — pageflip has had its first
  successful real-meeting sessions. STABILITY.md catalogue refreshed:
  pageflip threshold flag is now `f64` RMS (was `u32` bits), default
  capture interval 2s → 1s, slide-event `phash` field deprecated
  (always null), meetcat gained ~10 flags + the resume subcommand,
  web UI HTTP routes added to the surface catalogue. 12 → 11 gaps for
  1.0; six older gaps closed (T19.2 agents, T19.3 TUI replacement,
  T9.3 diarisation, T10.2 PII, T10.3 egress, multi-monitor picker).
  Two new gaps raised: 🎯T30 (attendee/presenter modes) and 🎯T31
  (video-detection auto-suspend).

## 2026-04-18 — /release v0.1.0

- **Commit**: `a88cf3b`
- **Outcome**: First release. macOS arm64 + Linux x86_64/arm64 binaries via
  GitHub Actions. Homebrew formula published to `marcelocantos/homebrew-tap`.
  Ships `pageflip` (capture loop), `pageflip-feed` (headless feeder), and
  `meetcat` (Go walking skeleton — validates slide-event stream only; full
  TUI + claudia session agents are post-v0.1.0).
- **Notes**: STABILITY.md created with pre-1.0 surface catalogue (18 pageflip
  flags, 6 pageflip-feed flags, 4 meetcat flags, 4 output formats, 2
  architectural invariants). 12 gaps identified for 1.0 readiness.
  HOMEBREW_TAP_TOKEN secret set from 1Password.
