# meetcat bug report guide

Use this document when filing bugs against meetcat.

## What to include

1. **meetcat version** — run `meetcat --version` and paste the output.
2. **Doctor report** — run `meetcat doctor` and paste the full markdown output.
3. **Session log** — if `--log-file` was configured, attach the NDJSON file.
   The log contains no meeting content — slide IDs, token counts, cost, and
   categorical error codes only. Safe to attach without redaction.
4. **Reproduction steps** — describe what you were doing when the bug occurred.
5. **Expected vs. actual behaviour** — one sentence each.
6. **pageflip version** — run `pageflip --version` and paste the output.

## Sensitive data policy

meetcat's structured log (`--log-file`) is designed to contain no meeting
content by construction: no OCR text, no transcript fragments, no specialist
output, no free-form error messages. If you believe a log entry contains
sensitive data, please file a privacy issue rather than a bug report.

Do NOT attach:
- Screen recordings or screenshots of meeting content.
- Raw slide images (`*.png`, `*.jpg`) captured by pageflip.
- Clipboard contents or OCR text from slides.
- Transcript audio files or whisperx output.

## Doctor output example

```
# meetcat doctor

## Versions
- **meetcat** 0.0.1 (sha: abc1234)
- **Go runtime** go1.26.1

## External tools
- **tmux** ✓ /opt/homebrew/bin/tmux — tmux 3.5
- **claude CLI** ✓ /usr/local/bin/claude — 1.2.3
- **claudia** — Go module dependency (linked at build time, no PATH entry needed)

## Anthropic auth state
- **ANTHROPIC_API_KEY** ✓ set in environment

## Active subprocesses
- (no active sessions — T19.2 not yet implemented)

## Recent session log
_No `--log-file` configured. Pass `--log-file <path>` to enable structured logging._
```

## Filing the report

Open an issue at https://github.com/marcelocantos/pageflip/issues and use the
label **meetcat**.
