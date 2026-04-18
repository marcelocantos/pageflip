# Audit Log

## 2026-04-18 — /release v0.1.0

- **Commit**: pending
- **Outcome**: First release. macOS arm64 + Linux x86_64/arm64 binaries via
  GitHub Actions. Homebrew formula published to `marcelocantos/homebrew-tap`.
  Ships `pageflip` (capture loop), `pageflip-feed` (headless feeder), and
  `meetcat` (Go walking skeleton — validates slide-event stream only; full
  TUI + claudia session agents are post-v0.1.0).
- **Notes**: STABILITY.md created with pre-1.0 surface catalogue (18 pageflip
  flags, 6 pageflip-feed flags, 4 meetcat flags, 4 output formats, 2
  architectural invariants). 12 gaps identified for 1.0 readiness.
  HOMEBREW_TAP_TOKEN secret set from 1Password.
