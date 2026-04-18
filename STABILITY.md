# Stability

## Commitment

Once pageflip reaches 1.0, backwards compatibility becomes a binding
contract. Breaking changes to the CLI interface, output formats, or
configuration will require forking the project into a new product (e.g.
`pageflip2`). The pre-1.0 period exists to get the interaction surface
right.

## Interaction surface catalogue

Snapshot as of v0.1.0.

### Binaries

| Binary | Status | Notes |
|--------|--------|-------|
| `pageflip` | Needs review | Primary capture loop + doctor subcommand |
| `pageflip-feed` | Needs review | Headless consumer — may be superseded by meetcat for most users |
| `meetcat` | Fluid | Walking skeleton (T19.1); no claudia agents or TUI yet |

### pageflip CLI flags

| Flag | Type | Default | Status |
|------|------|---------|--------|
| `--region X,Y,W,H` | string | (none) | Stable |
| `--window` | bool | false | Needs review |
| `--window-title SUBSTRING` | string | (none) | Needs review |
| `--window-id ID` | u32 | (none) | Needs review |
| `--list-windows` | bool | false | Stable |
| `--crop` | bool | false | Needs review |
| `--interval SECS` | f64 | 2.0 | Stable |
| `--threshold N` | u32 | 10 | Stable |
| `--output DIR` | path | `pageflip-<unix>` | Stable |
| `--redact-faces` | bool | false | Needs review — T10.2/T10.3 may reshape the redaction surface |
| `--audio` | bool | false | Needs review — capture works but transcript pipeline is new |
| `--transcriber KIND` | string | `null` | Fluid — only `null` and `whisperx` exist; more may follow |
| `--whisperx-model MODEL` | string | `large-v3` | Needs review |
| `--events-out SPEC` | string | (none) | Needs review — schema shape may evolve as OCR/transcript fields land |
| `--log-file PATH` | path | (none) | Stable |
| `--help-agent` | bool | false | Stable |
| `--version` | bool | false | Stable |
| `--help` | bool | false | Stable |

### pageflip subcommands

| Subcommand | Status | Notes |
|------------|--------|-------|
| `doctor` | Needs review | `--log`, `--meetcat-log`, `--no-meetcat` |

### pageflip-feed CLI flags

| Flag | Type | Default | Status |
|------|------|---------|--------|
| `--watch DIR` | path | (none) | Needs review |
| `--session-id ID` | string | (none) | Needs review |
| `--prompt TEMPLATE` | string | `Analyse this slide: @{path}` | Needs review |
| `--claude PATH` | path | `claude` | Stable |
| `--pipe` | bool | false | Needs review — reads NDJSON from stdin (T18) |
| `--help-agent` | bool | false | Stable |

### meetcat CLI flags

| Flag | Type | Default | Status |
|------|------|---------|--------|
| `--log-file PATH` | path | (none) | Needs review |
| `-version` | bool | false | Stable |
| `-help` | bool | false | Stable |
| `-help-agent` | bool | false | Stable |
| `doctor` (subcommand) | — | — | Needs review |

### Output formats

| Format | Location | Status |
|--------|----------|--------|
| Legacy stdout: one absolute path per line | pageflip stdout (default) | Stable |
| Slide-event NDJSON | `--events-out` target | Fluid — schema at `docs/slide-event-schema.json`; optional fields will grow |
| Session log NDJSON | `--log-file` target | Needs review — event types will expand |
| `transcript.jsonl` | `<output_dir>/transcript.jsonl` | Fluid — T9.3 will add `speaker_id` + `slide_id` fields |

### Architectural invariants

| Invariant | Status |
|-----------|--------|
| Raw audio bytes never leave `src/audio/` | Stable — enforced by type visibility |
| Session log schema has no free-form content fields | Stable — enforced by typed event enum |

## Gaps and prerequisites for 1.0

- [ ] Visual verification of the region picker overlay (T2) on multiple
      display configurations (single monitor, multi-monitor, different
      scale factors)
- [ ] Visual verification of the crop picker (T4)
- [ ] End-to-end WhisperX transcription run on a real meeting (T9.2)
- [ ] Speaker diarisation (T9.3) — acceptance requires pyannote pipeline
- [ ] The `--events-out` schema stabilises once OCR, transcript, and
      frontmost-app fields are populated by their respective targets
- [ ] meetcat session-mode agents (T19.2) wired — the current skeleton
      only validates the event stream
- [ ] meetcat TUI (T19.3) — the primary user-facing product doesn't exist
      yet
- [ ] `--redact-faces` verified with a real face in a live capture
- [ ] PII text redaction via OCR (T10.2) landed and tested
- [ ] Egress gate (T10.3) architectural enforcement verified
- [ ] Release CI workflow verified end-to-end (this release is the first
      test)
- [ ] Homebrew tap formula verified via `brew install`

## Out of scope for 1.0

- Windows or Linux backends (deleted from roadmap)
- Code signing of release binaries (cost + Apple Developer account)
- Cross-meeting contradiction detection via mnemo (T14)
- Weekly meta-pattern digest (T17)
- Streaming real-time transcription (post-hoc batch is sufficient)
