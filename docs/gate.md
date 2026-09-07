# The owner gate and its known-red ledger

pageflip ships to `master` by a gated push, not a pull request. `make hooks`
wires `core.hooksPath=scripts/hooks`; `scripts/hooks/pre-push` runs `make gate`
and refuses the push on a non-zero exit. CI (`ci.yml`, and the `test` job of
`release.yml`) runs the same recipe, checking out `marcelocantos/claudia`
beside pageflip because `meetcat/go.mod` replaces claudia with `../../claudia`.

Five legs: `fmt`, `clippy`, `build`, `test` (Rust) and `meetcat-test` (Go).

## The problem this file solves

The meetcat leg is **red**, and was red before the gate existed — the gate did
not break it, it made it visible. Until now that redness was declared only in
CLAUDE.md prose, while `make bullseye` depends on `gate`, so pageflip was
silently push-blocked by a Go module's dependency divergence.

Prose is not a ledger. The standing rule: **never wire a gate over a red or
host-state-dependent suite without declaring the residue visibly, in a ledger
the gate itself prints.**

## The decision: advisory, loud, ratcheted

`scripts/gate-meetcat.sh` runs the same `go vet ./... && go test ./... -count=1`
the leg always ran, and prints a boxed ledger naming the leg, the cause, and
the two candidate resolutions. It does **not** block on the known redness.

The rule pageflip adopts, which is what settles the question:

> The gate **blocks** on anything the person pushing can act on, and
> **declares** — loudly, every run — what only the owner's pending product
> decision can resolve.

Both halves of the team lead's principle are real, and this rule honours both:

- *A gate that blocks everything for a known unrelated reason gets bypassed.*
  Blocking here would refuse every push of an unrelated Rust change, 100% of
  the time, until a decision the pusher cannot make gets made. And
  `--no-verify` is not selective: bypassing the meetcat leg discards the four
  green Rust legs with it. A blocking known-red leg does not add enforcement;
  it subtracts the enforcement that already worked.
- *A leg that silently does not block is how meetcat got untested in the first
  place.* True — but the defect there was **invisibility**, not
  non-blockingness. meetcat ran nowhere: not locally, not in CI. This leg runs
  on every push and every CI run and prints an unmissable box.

The gap between "loud" and "enforcing" is closed by a ratchet in both
directions. The leg **fails** the gate when:

- the output contains any line not attributable to the declared signature —
  matched *exhaustively*, not by substring (see below); or
- the leg **passes** while the declaration still stands. A stale declaration is
  how a ledger quietly stops meaning anything. That is a one-commit fix and it
  is in the pusher's hands, so it blocks.

`PAGEFLIP_REQUIRE_MEETCAT=1 make gate` makes the known red blocking too, for
checking a resolution by hand.

## Exhaustive matching, and why substring matching is not a ratchet

The first version of this script asked "does the output contain a missing-go.sum
line naming claudia?". A planted syntax error in `meetcat/agents.go` was waved
through as known-red, because the go.sum lines were present too. A declared-red
leg that swallows genuine regressions is worse than a leg that does not run.

The script now strips every line matching the declared patterns and blocks on
whatever survives.

## Coverage, stated honestly

While this redness stands, the meetcat leg detects **nothing** about meetcat's
own source. `go vet` aborts at module load, so a syntax error in `meetcat/*.go`
produces byte-identical output to a clean tree, and no matching scheme —
exhaustive or otherwise — can tell them apart. This was verified by planting
one.

So the advisory leg buys **visibility of the divergence**, not regression
coverage of meetcat. Coverage returns only when the divergence is resolved.
That is an argument for resolving it, not for pretending the leg is a net.

## The redness in detail

`meetcat/go.mod` requires `github.com/marcelocantos/claudia v0.6.0` and
replaces it with the owner's live `../../claudia` checkout, now v0.30.0:

- `go.sum` has no entries for claudia's newer dependencies (aws-sdk-go-v2,
  coder/websocket), so `go vet` cannot load the package;
- after `go mod tidy` the source no longer compiles — `claudia.Agent.OnEvent`
  and `claudia.Task.RunTask` are gone from the v0.30 API.

With the `replace` removed, meetcat builds and tests green against the
published claudia v0.6.0.

Two resolutions, **both the owner's call — an agent must not pick one**:

- **(a)** port meetcat to the current claudia API and keep the path replace, so
  meetcat tracks claudia as it moves;
- **(b)** drop the replace and pin a published claudia, so meetcat builds
  standalone and updates deliberately.

When one lands: delete the known-red block from `scripts/gate-meetcat.sh` (the
leg becomes an ordinary blocking leg), delete this section and CLAUDE.md's, and
retire 🎯T42. The stale-declaration ratchet will fail the gate until you
do, which is the point.

## Drill

The gate is only evidence if it has been shown to refuse:

```bash
sha=$(git rev-parse HEAD)
printf 'refs/heads/burst/ci-gate %s refs/heads/master %s\n' "$sha" "$sha" > /tmp/refs.txt
git -c core.hooksPath=scripts/hooks hook run --to-stdin=/tmp/refs.txt pre-push \
  -- origin https://github.com/marcelocantos/pageflip.git
```

`git hook run` gives the hook `/dev/null` on stdin unless `--to-stdin` names a
file; piping into it is silently an empty push list and the gate never runs.
