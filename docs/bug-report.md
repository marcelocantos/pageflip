# Reporting a bug

Bug reports are most useful when they include environment details and a
session log. The `doctor` subcommands emit a markdown diagnostic that is
**paste-safe by construction** — no meeting content, no window titles, no
OCR, no transcripts.

## TL;DR

```bash
pageflip doctor \
  --log         ~/.local/share/pageflip/session.ndjson \
  --meetcat-log ~/.local/share/pageflip/meetcat.ndjson \
  > report.md
```

`pageflip doctor` auto-invokes `meetcat doctor` when it finds meetcat on
`$PATH` and appends its output. Pass `--no-meetcat` to skip. Omit either
log flag if you didn't set it on the failing run.

Paste `report.md` into a new issue using the
[Bug report template](../.github/ISSUE_TEMPLATE/bug_report.yml).

## 1. `pageflip doctor`

```bash
pageflip doctor
```

Emits a markdown report covering:

- pageflip version + git SHA + rustc version
- macOS version + chip + host
- HF model cache inventory (per-model present/missing + size + snapshot path)
- Screen Recording + Microphone permission states
- External tool versions (tmux, ffmpeg, uv, python3, claude, go)
- HF token + Anthropic auth presence (boolean — never the secret)

## 2. `meetcat doctor` (auto-invoked by pageflip doctor)

`pageflip doctor` runs `meetcat doctor` for you when `meetcat` is on
`$PATH`. If you want meetcat's doctor standalone:

```bash
meetcat doctor
```

Companion report with:

- meetcat version + Go runtime
- tmux + claude CLI availability
- Anthropic auth state
- Active claudia sessions (once 🎯T19.2 lands)
- Recent session log tail (if `--log-file` was set)

## 3. Session log

If you passed `--log-file <PATH>` to pageflip or meetcat during the failing
run, tail it into your report:

```bash
pageflip doctor --log ~/.local/share/pageflip/session.ndjson >> report.md
# — or paste the tail directly —
tail -n 200 ~/.local/share/pageflip/session.ndjson
```

If you did not enable `--log-file`, enable it on your next run:

```bash
pageflip --log-file ~/.local/share/pageflip/session.ndjson <other flags>
meetcat  --log-file ~/.local/share/pageflip/meetcat.ndjson
```

### What the session log contains

Numeric values, enum codes, and hashed identifiers — that is all.

- Event types: `session_start`, `slide_saved`, `slide_deduped`, `audio_batch`,
  `transcribe_start`, `transcribe_end`, `specialist_turn`, …
- Per-event fields: timestamps (ms), counts, durations, sizes, error
  category codes, 4-char hashed session IDs, 12-char salted path hashes.

### What the session log does **not** contain

By schema, not by convention — the event types simply have no fields where
these values could go:

- Window titles
- OCR text
- Transcript text
- Specialist output text
- Frontmost app display names (only bundle IDs, opt-in)
- Raw file paths (always salted + truncated to 12 hex chars)

## 4. Open an issue

<https://github.com/marcelocantos/pageflip/issues/new>

Pick the **Bug report** template. Paste the contents of `report.md` into the
respective fields. The form prompts for a repro-steps block — include the
exact command(s) you ran.

## What not to attach

Even when filing a detailed report, do **not** paste or attach:

- Screenshots containing meeting content. Scrub first.
- Raw slide PNGs or other pageflip output frames.
- Meeting transcripts (`transcript.jsonl` contents).
- Claudia session transcripts or claude `.jsonl` replay files.
- `ANTHROPIC_API_KEY` or any other secret.
- Audio recordings — pageflip never produces these, and upstream cannot
  help you process them (the project's audio invariant forbids it).

## Sensitive data policy

If you find a log entry that contains unexpected sensitive content,
please open a **private security advisory** on the repo rather than a
regular issue: <https://github.com/marcelocantos/pageflip/security/advisories/new>.
