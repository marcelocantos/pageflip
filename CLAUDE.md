# pageflip

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

## Delivery

Merged to master. The owner pushes straight to master; the enforcement point
is the local pre-push hook, not a PR.

- `make gate` is the oracle: `fmt clippy build test` (Rust) **and**
  `meetcat-test` (`go vet` + `go test` in `meetcat/`). Until 2026-09 the Go
  leg ran nowhere — not locally, not in CI.
- `make hooks` wires `core.hooksPath=scripts/hooks`, so `scripts/hooks/pre-push`
  runs `make gate` and refuses a red push. `--no-verify` is the owner's
  emergency bypass, never an agent's.
- CI (`ci.yml` on master/`v*`/PRs, and the `test` job of `release.yml`) runs the
  same `make gate`, checking out `marcelocantos/claudia` beside pageflip
  because `meetcat/go.mod` replaces claudia with `../../claudia`.

### Known red: the meetcat Go leg

`make gate` is **red today** on `meetcat-test`, and was red before the gate
existed. `meetcat/go.mod` pins `github.com/marcelocantos/claudia v0.6.0` but
replaces it with the local `../../claudia` checkout, which is now v0.30.0. The
two have diverged:

- `go.sum` has no entries for claudia's newer deps (aws-sdk-go-v2, coder/websocket);
- after `go mod tidy` the source no longer compiles — `claudia.Agent.OnEvent`
  and `claudia.Task.RunTask` no longer exist.

Against the published `claudia v0.6.0` (replace removed) `go vet` and
`go test ./...` both pass. So the choice is the owner's: port meetcat to the
current claudia API and keep the path replace, or drop the replace and pin a
published claudia.

**Pushes are not blocked by it.** `scripts/gate-meetcat.sh` runs the leg on
every push and prints a boxed known-red ledger naming the cause and both
resolutions, but does not fail the gate on the *declared* redness — the person
pushing cannot fix it, and `--no-verify` would discard the four green Rust legs
along with it. It ratchets in both directions: any output line not attributable
to the declared signature blocks, and the leg going green while still declared
red blocks. `PAGEFLIP_REQUIRE_MEETCAT=1 make gate` makes the known red blocking
too.

While the redness stands the leg detects nothing about meetcat's own source —
`go vet` aborts at module load — so this buys visibility, not coverage. Full
reasoning: [`docs/gate.md`](docs/gate.md).
