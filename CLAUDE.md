# pageflip

`pageflip` is the Go orchestrator users run. It owns the localhost
web UI, specialist pool, and meeting manifest, and it spawns
`pageflip-capture` (Rust) for region/window capture, RMS dedup, and
the NDJSON event stream. `pageflip-feed` is a leftover headless
consumer. There is no `meetcat` binary; Go source still lives under
`meetcat/`.

## Release readiness

release_readiness: ready

Pageflip has had its first real-session successes — capture → web UI
→ specialist analysis end-to-end. Slides flow into the browser, the
RMS dedup discriminates cleanly, specialists return useful output
in the audience-mode shape the project is aiming for. The author is
running the installed version against real meetings.

Forward progress (new specialists, attendee/presenter modes, video
auto-suspend, daemonisation) is now legitimate release material;
unreleased fixes carry normal user-facing urgency.
