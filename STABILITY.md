# Stability

## Commitment

Once pageflip reaches 1.0, backwards compatibility becomes a binding
contract. Breaking changes to the CLI interface, output formats, or
configuration will require forking the project into a new product (e.g.
`pageflip2`). The pre-1.0 period exists to get the interaction surface
right.

## Interaction surface catalogue

Snapshot as of v0.2.0.

### Binaries

| Binary | Status | Notes |
|--------|--------|-------|
| `pageflip` | Needs review | Capture loop + multi-monitor picker + doctor. Wire format and CLI surface settling but not yet locked. |
| `meetcat` | Needs review | Primary user-facing product: web UI, specialists, resume, manifest. Picked up rapidly; needs a few more meetings before locking. |
| `pageflip-feed` | Fluid | Headless consumer. Superseded by meetcat for the primary use case; kept as a thin alternative for scripted pipelines. May be retired before 1.0. |

### pageflip CLI flags

| Flag | Type | Default | Status |
|------|------|---------|--------|
| `--region X,Y,W,H` | string | (none) | Stable |
| `--window` | bool | false | Needs review |
| `--window-title SUBSTRING` | string | (none) | Needs review |
| `--window-id ID` | u32 | (none) | Needs review |
| `--list-windows` | bool | false | Stable |
| `--crop` | bool | false | Needs review |
| `--interval SECS` | f64 | 1.0 | Stable |
| `--threshold RMS` | f64 | 5.0 | Needs review — tuned against early real meetings; may move slightly |
| `--output DIR` | path | `pageflip-<unix>` | Stable |
| `--redact-faces` | bool | false | Needs review |
| `--redact-pii` | bool | false | Needs review |
| `--paranoid DIR` | path | (none) | Needs review |
| `--audio` | bool | false | Needs review |
| `--transcriber KIND` | string | `null` | Fluid — only `null` and `whisperx` exist |
| `--whisperx-model MODEL` | string | `large-v3` | Needs review |
| `--diarize` | bool | false | Needs review |
| `--events-out SPEC` | string | (none) | Needs review — schema may grow optional fields (OCR bboxes, etc.) |
| `--log-file PATH` | path | (none) | Stable |
| `--help-agent` | bool | false | Stable |
| `--version` | bool | false | Stable |
| `--help` | bool | false | Stable |

### pageflip subcommands

| Subcommand | Status | Notes |
|------------|--------|-------|
| `doctor` | Needs review | `--log`, `--meetcat-log`, `--no-meetcat` |

### meetcat CLI flags

| Flag | Type | Default | Status |
|------|------|---------|--------|
| `--no-agents` | bool | false | Stable — agents on by default; flag opts out |
| `--no-spawn` | bool | false | Stable — read slide events from stdin instead of spawning pageflip |
| `--region X,Y,W,H` | string | (none) | Stable — forwarded to pageflip |
| `--window` | bool | false | Needs review — forwarded to pageflip |
| `--window-title SUBSTRING` | string | (none) | Needs review — forwarded to pageflip |
| `--window-id ID` | string | (none) | Needs review — forwarded to pageflip |
| `--work-dir PATH` | path | `.` | Stable — root for `meetcat-<id>/` artifact directories |
| `--specialists LIST` | string | (none) | Needs review — to be re-shaped by 🎯T30 (modes) |
| `--glossary-cache PATH` | path | `~/.pageflip/glossary.json` | Needs review |
| `--log-file PATH` | path | (none) | Needs review |
| `--web-dir PATH` | path | (auto) | Stable (dev affordance) — auto-detect `<exe>/web` or `./web`, else embed |
| `--version` | bool | false | Stable |
| `--help` | bool | false | Stable |
| `--help-agent` | bool | false | Stable |

### meetcat subcommands

| Subcommand | Status | Notes |
|------------|--------|-------|
| `resume <meeting-id>` | Needs review | Re-spawns specialists from the JSONL persistence + manifest |
| `doctor` | Needs review | Diagnostic report |
| `glossary refresh` | Needs review | Confluence scrape |
| `attach` | Removed | Deprecated path now prints a redirect to `resume` |

### pageflip-feed CLI flags

| Flag | Type | Default | Status |
|------|------|---------|--------|
| `--watch DIR` | path | (none) | Fluid — likely retired pre-1.0 |
| `--session-id ID` | string | (none) | Fluid |
| `--prompt TEMPLATE` | string | `Analyse this slide: @{path}` | Fluid |
| `--claude PATH` | path | `claude` | Fluid |
| `--pipe` | bool | false | Fluid |
| `--help-agent` | bool | false | Stable |

### Output formats

| Format | Location | Status |
|--------|----------|--------|
| Slide-event NDJSON | `--events-out` target | Needs review — schema at `docs/slide-event-schema.json`; `phash` field deprecated (always `null` since v0.2.0); optional OCR/transcript/frontmost-app fields will grow |
| Legacy stdout: one absolute path per line | pageflip stdout (default) | Stable |
| Session log NDJSON | `--log-file` target | Needs review — event types will expand |
| `transcript.jsonl` | `<output_dir>/transcript.jsonl` | Fluid — T9.3 added `speaker_id` and slide-aligned segments |
| Web UI HTTP routes | `http://127.0.0.1:49831/` | Needs review — `/`, `/style.css`, `/script.js`, `/meeting`, `/events` (SSE), `/slides/<rel>` |
| SSE event types | `slide`, `specialist`, `turn-done`, `state`, `system` | Needs review — payload shapes settling but not locked |
| `meeting.json` manifest | `meetcat-<id>/meeting.json` | Needs review — schema_version 1; resume reads it; field set may grow (mode, specialists allowlist) |
| `session-ids.json` | `meetcat-<id>/session-ids.json` | Stable — flat name → UUID map |

### Architectural invariants

| Invariant | Status |
|-----------|--------|
| Raw audio bytes never leave `src/audio/` | Stable — enforced by type visibility |
| Session log schema has no free-form content fields | Stable — enforced by typed event enum |
| Web UI binds `127.0.0.1` only (no LAN exposure) | Stable |
| Specialists' Claude Code sessions persist via JSONL across restarts | Stable — basis of resume |

## Gaps and prerequisites for 1.0

- [ ] Modes feature (🎯T30) — attendee/presenter specialist sets; default attendee. Currently the specialists are presenter-biased.
- [ ] Video-detection auto-suspend (🎯T31) — without this, a video clip on a slide saturates the analysis pipeline.
- [ ] Daemon mode + meeting catalogue UI — meetcat as a long-running brew service with a front page listing past meetings.
- [ ] Agent guide (`agents-guide.md`) — currently missing; both binaries should ship one referenced from the README.
- [ ] WhisperX transcription verified against a real meeting (T9.2 validation).
- [ ] `--redact-faces` and `--redact-pii` verified end-to-end with face / PII content in real captures.
- [ ] Egress gate (T10.3) architectural enforcement verified at real-meeting scale.
- [ ] OCR pipeline (🎯T26) wired so specialists can ground analysis on slide text without each having to re-Read the PNG.
- [ ] Loader-emitted slide_label (🎯T27) for stable slide identification across revisits.
- [ ] `--threshold RMS` calibrated against a wider sample of meeting types (currently 5.0 from a handful of decks).
- [ ] `pageflip-feed` retirement decision: keep as a stripped-down headless alternative, or remove before 1.0.
- [ ] Web UI accessibility audit (keyboard navigation, screen-reader compatibility).

## Out of scope for 1.0

- Windows or Linux capture backends (macOS-only).
- Code signing of release binaries (cost + Apple Developer account).
- Multi-meeting concurrency (the daemon-mode story may add this; if it does, post-1.0).
- Streaming real-time transcription (post-hoc batch is sufficient).
- LAN-exposed web UI (always `127.0.0.1`-only).
