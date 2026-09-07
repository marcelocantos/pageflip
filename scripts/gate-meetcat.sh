#!/usr/bin/env bash
# The meetcat leg of `make gate`, with its redness declared out loud.
#
# meetcat/go.mod pins github.com/marcelocantos/claudia v0.6.0 and then
# `replace`s it with the owner's live ../../claudia checkout, which is now
# v0.30.0. The two have diverged far enough that the leg cannot pass:
# go.sum has no entries for claudia's newer dependencies, and after a
# `go mod tidy` the source stops compiling (Agent.OnEvent, Task.RunTask are
# gone). Resolving that is a product decision only the owner can make.
#
# Blocking vs advisory — the reasoning, because it is the whole point of this
# file. The rule this repo adopts:
#
#     the gate BLOCKS on anything the person pushing can act on, and
#     DECLARES — loudly, every run — what only the owner's pending decision
#     can resolve.
#
# Blocking on the known redness would refuse every push of an unrelated Rust
# change for a reason with no connection to the commit, and would do so 100%
# of the time until a decision the pusher cannot make gets made. A gate that
# is bypassed on every push enforces nothing — and `--no-verify` does not
# bypass the meetcat leg selectively, it discards the four green Rust legs
# too. That is how a blocking known-red leg ends up weakening the gate.
#
# But "advisory" must not mean "silent". meetcat rotted precisely because it
# ran nowhere: invisibility, not non-blockingness, was the defect. So this
# leg runs on every push, prints an unmissable ledger, and RATCHETS in both
# directions — it fails the gate if the failure signature changes, and it
# fails the gate if the leg goes green while the declaration is still here.
# Both of those are actionable by whoever is pushing; the known red is not.
#
# PAGEFLIP_REQUIRE_MEETCAT=1 makes the known red blocking, for checking the
# resolution by hand.
#
# macOS /bin/bash is 3.2: no associative arrays, no ${var,,}.
set -uo pipefail

cd "$(dirname "$0")/../meetcat"

out="$(mktemp)"
trap 'rm -f "$out"' EXIT

go vet ./... > "$out" 2>&1 && go test ./... -count=1 >> "$out" 2>&1
status=$?

banner() {
	echo ""
	echo "════════════════════════════════════════════════════════════════════"
	echo "$1"
	echo "════════════════════════════════════════════════════════════════════"
}

if [[ "$status" -eq 0 ]]; then
	banner "✗ meetcat: DECLARED RED, BUT GREEN — the declaration is stale"
	cat <<-'EOF'
	  This leg is declared known-red in scripts/gate-meetcat.sh and docs/gate.md,
	  and it just passed. Either the claudia divergence was resolved or the
	  `replace` no longer resolves to a diverged checkout.

	  A stale declaration is how a gate quietly stops meaning anything, so this
	  fails the gate. It is a one-commit fix, and it is in your hands:

	    1. delete the known-red block from scripts/gate-meetcat.sh, so the leg
	       becomes an ordinary blocking leg again;
	    2. delete the "Known red" section from CLAUDE.md and docs/gate.md;
	    3. retire the target tracking the divergence.
	EOF
	echo ""
	exit 1
fi

# The declared signature: the claudia path-replace dragging in dependencies
# go.sum has never seen. Matched EXHAUSTIVELY — every line of output must be
# attributable to the declared cause. A "does the output contain X" test is
# not a ratchet: the first version of this script used one, and a planted
# syntax error in meetcat/agents.go was waved through as known-red because the
# go.sum lines were also present. Whatever survives the filter below blocks.
declared_lines='missing go.sum entry for module providing package .*\(imported by github\.com/marcelocantos/claudia\); to add:$|^	go get github\.com/marcelocantos/claudia@v[0-9]'
residual="$(grep -vE "$declared_lines" "$out" | grep -v '^[[:space:]]*$' || true)"

if [[ -z "$residual" ]] &&
   grep -q "missing go.sum entry for module providing package" "$out" &&
   grep -q '^replace github.com/marcelocantos/claudia => \.\./\.\./claudia$' go.mod; then

	banner "⚠  meetcat: KNOWN RED — declared, not blocking (see docs/gate.md)"
	cat <<-'EOF'
	  Leg:    meetcat (go vet ./... && go test ./... -count=1)
	  Status: RED, and red before this gate existed — the gate did not break it,
	          it made it visible. Nothing else in this repo depends on it.

	  Why:    meetcat/go.mod requires github.com/marcelocantos/claudia v0.6.0 and
	          replaces it with the owner's live ../../claudia checkout, now
	          v0.30.0. go.sum has no entries for claudia's newer dependencies
	          (aws-sdk-go-v2, coder/websocket), so `go vet` cannot load the
	          package; and after `go mod tidy` the source no longer compiles —
	          claudia.Agent.OnEvent and claudia.Task.RunTask are gone from the
	          v0.30 API. With the replace removed, meetcat builds and tests
	          green against the published claudia v0.6.0.

	  Resolutions, both the owner's call — an agent must not pick one:
	    (a) port meetcat to the current claudia API and keep the path replace,
	        so meetcat tracks claudia as it moves; or
	    (b) drop the replace and pin a published claudia, so meetcat builds
	        standalone and updates deliberately.

	  Not blocking, deliberately: this leg cannot be fixed by the person
	  pushing, so blocking would refuse every unrelated push until a decision
	  only the owner can make gets made — and --no-verify would then discard
	  the four green Rust legs along with it. It is loud instead of silent,
	  because invisibility (this leg ran nowhere until 2026-09) is what let
	  meetcat rot in the first place.

	  Ratchet: the declared signature is matched exhaustively — every line of
	  output must be attributable to the claudia divergence. This leg FAILS the
	  gate on any residual line, and FAILS it if the leg goes green while still
	  declared red.

	  Coverage, stated honestly: while this redness stands the leg detects
	  NOTHING about meetcat's own source. `go vet` aborts at module load, so a
	  syntax error in meetcat/*.go produces exactly the output above and is
	  invisible. Advisory here buys visibility of the divergence, not
	  regression coverage of meetcat — that only comes back when (a) or (b)
	  lands.

	  Check the resolution by hand with:  PAGEFLIP_REQUIRE_MEETCAT=1 make gate
	EOF
	echo ""
	echo "--- go vet/test output, verbatim -----------------------------------"
	sed 's/^/  /' "$out"
	echo "--------------------------------------------------------------------"
	echo ""

	if [[ "${PAGEFLIP_REQUIRE_MEETCAT:-0}" == "1" ]]; then
		echo "✗ meetcat: PAGEFLIP_REQUIRE_MEETCAT=1 — treating the known red as blocking." >&2
		exit 1
	fi
	echo "⚠  meetcat: known red, declared, NOT blocking this push."
	exit 0
fi

banner "✗ meetcat: UNDECLARED FAILURE — blocking"
cat <<-'EOF'
  This leg produced output that is not wholly attributable to the declared
  claudia-divergence signature ("missing go.sum entry" lines naming
  marcelocantos/claudia, with the path replace still in meetcat/go.mod). An
  undeclared failure is a real regression, not the known residue, so it blocks
  the push.

  If this IS a new class of known residue, declare it in
  scripts/gate-meetcat.sh and docs/gate.md in the same commit that
  introduces it. Do not widen the signature to make a red leg quiet.
EOF
echo ""
if [[ -n "$residual" ]]; then
	echo "--- lines matching no declared residue ------------------------------"
	printf '%s\n' "$residual" | sed 's/^/  /'
	echo "--------------------------------------------------------------------"
	echo ""
fi
echo "--- go vet/test output, verbatim -----------------------------------"
sed 's/^/  /' "$out"
echo "--------------------------------------------------------------------"
exit 1
