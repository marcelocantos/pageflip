# Weekly Meeting Digest

`scripts/meetings-digest.sh` reads the last 7 days of meetcat artifact folders
and produces a meta-pattern digest via `claude -p`. A launchd job runs it every
Sunday at 09:00.

## How it works

Each time meetcat processes a meeting it writes a folder under the meetings
root. The digest script scans that root for folders modified in the last 7
days, extracts the per-meeting artifacts, and composes a single prompt that is
piped to `claude -p`. The response is written to
`<meetings-dir>/digests/week-<YYYY-MM-DD>.md`.

### Artifacts consumed per meeting

| File | Content |
|---|---|
| `decisions.md` | Decisions recorded during the meeting |
| `actions.md` | Action items |
| `open-questions.md` | Unresolved questions |
| `contradictions.md` | Contradictions flagged by meetcat |
| `transcript.jsonl` | (Optional) Raw transcript — speaker IDs extracted for pattern observations |

### Digest sections

1. **Recurring themes** — topics appearing across multiple meetings.
2. **Drifting or reversed decisions** — decisions that changed or were walked
   back across meetings.
3. **Aging open questions** — questions present in more than one meeting
   without resolution.
4. **Fresh contradictions worth escalating** — newly surfaced contradictions.
5. **Speaker-pattern observations** — dominant/absent speakers, cross-meeting
   participation shifts (only when `transcript.jsonl` contains `speaker_id`
   data).

## Setup

### Prerequisites

- [`claude` CLI](https://github.com/anthropics/anthropic-tools) installed and
  authenticated (`claude --version`).
- meetcat writing artifact folders to a stable directory (e.g.
  `~/meetings`).

### Install the weekly launchd job (macOS)

```bash
./scripts/install-digest-cron.sh --meetings-dir ~/meetings
```

This writes
`~/Library/LaunchAgents/com.marcelocantos.pageflip-digest.plist` and loads it
immediately. The job fires **every Sunday at 09:00 local time**.

Logs land in `~/Library/Logs/pageflip/`:

| File | Content |
|---|---|
| `digest-stdout.log` | Script output (progress messages) |
| `digest-stderr.log` | Error output |

### Trigger a manual run

```bash
launchctl start com.marcelocantos.pageflip-digest
# or directly:
./scripts/meetings-digest.sh ~/meetings
```

### Uninstall

```bash
./scripts/install-digest-cron.sh --uninstall
```

## Linux (cron)

The digest script is cross-platform. Add a crontab entry manually:

```cron
0 9 * * 0 /path/to/pageflip/scripts/meetings-digest.sh /path/to/meetings
```

## Output location

```
<meetings-dir>/
└── digests/
    ├── week-2026-04-13.md
    ├── week-2026-04-20.md
    └── …
```

Each file is a standalone Markdown document with the date in the filename.
Commit the `digests/` folder to your notes repo for a searchable history.
