# Agent guide

Invocation notes for the binaries that exist in this tree after T33.
Prefer `pageflip --help` / `pageflip-capture --help` at runtime if a
flag is in doubt. Do not invoke `meetcat`; that binary is not shipped.
Go source still lives under `meetcat/`.

## Binaries

| Binary | Role | Typical caller |
|--------|------|----------------|
| `pageflip` | Go orchestrator: web UI, specialists, resume, manifest | User |
| `pageflip-capture` | Rust engine: picker, RMS dedup, PNGs, NDJSON events | Spawned by `pageflip` |
| `pageflip-feed` | Headless PNG-watch / NDJSON consumer | Scripted pipelines; may be retired before 1.0 |

`pageflip` always adds `--events-out stdout` when it spawns the
capture engine. Users do not need to stitch a pipe.

## `pageflip`

```
pageflip
pageflip --region X,Y,W,H
pageflip --window
pageflip --window-title SUBSTRING
pageflip --window-id ID
pageflip --no-spawn < events.jsonl
pageflip --log-file session.ndjson
pageflip --no-agents
pageflip --specialists skeptic,neutral
pageflip resume <meeting-id-or-dir>
pageflip doctor [--log-file session.ndjson]
pageflip glossary refresh --confluence-url URL
pageflip --help
pageflip --help-agent
pageflip --version
```

Subcommand `attach` is removed; it prints a redirect to `resume` and
exits 2.

Default specialists: skeptic, constructive, neutral, dejargoniser,
contradictions. Meeting artifact dirs are `pageflip-<unix-ms>/` under
`--work-dir` (default `.`). Legacy `meetcat-*` directories still
resume. Web UI binds `127.0.0.1:49831`.

## `pageflip-capture`

```
pageflip-capture [--region X,Y,W,H] [--interval SECS] [--threshold RMS] [--output DIR]
pageflip-capture --window
pageflip-capture --window-title SUBSTRING
pageflip-capture --window-id ID
pageflip-capture --window --crop
pageflip-capture --list-windows
pageflip-capture --events-out stdout|fd:N|<path>
pageflip-capture doctor [--log PATH] [--pageflip-log PATH] [--no-pageflip]
pageflip-capture --help
pageflip-capture --help-agent
pageflip-capture --version
```

Dedup is mean per-channel RMS on a 0–255 scale (default threshold
5.0, interval 1.0 s). Default `--output` is `pageflip-capture-<unix>`
in the current directory.

`pageflip-capture doctor` appends `pageflip doctor` when that binary
is on `$PATH`, unless `--no-pageflip`. Capture `--help-agent` text
may still name the pre-T33 `pageflip` capture binary; trust this
file and clap `--help` over that blob.

## `pageflip-feed`

```
pageflip-feed --watch DIR --session-id ID [--prompt TEMPLATE] [--claude PATH]
pageflip-feed --pipe --session-id ID
pageflip-feed --help
pageflip-feed --help-agent
pageflip-feed --version
```

`--watch` and `--pipe` are mutually exclusive. `--pipe` auto-detects
path-per-line vs NDJSON from the first non-empty stdin line.

## What not to assume

- No `meetcat` command, no `--no-meetcat`, no `--meetcat-log`.
- No `--mode` flag yet (🎯T30).
- Producer slide events currently omit `ocr`, `transcript_window`,
  and `frontmost_app` even though those schema fields exist.
- Raw audio never leaves `src/audio/`.
