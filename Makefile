.PHONY: bullseye gate build test fmt clippy meetcat-test hooks

# The gate the pre-push hook runs: every oracle this repo already has, on both
# legs. `meetcat-test` is the Go leg — until 2026-09 it ran nowhere.
#
# The meetcat leg is KNOWN RED (claudia path-replace divergence) and runs as a
# declared, loudly-printed advisory rather than a blocker — see
# scripts/gate-meetcat.sh for the full reasoning and docs/gate.md for the
# ledger. It still fails the gate on any undeclared failure, or if it goes
# green while the declaration stands. So "gate green" here means the four Rust
# legs are green and the meetcat ledger above says where it stands; it does
# not mean every leg passed.
gate: fmt clippy build test meetcat-test
	@echo "✓ gate: fmt, clippy, build, test green — meetcat leg declared (ledger above)"

hooks:
	git config core.hooksPath scripts/hooks
	@echo "✓ core.hooksPath = scripts/hooks"

build:
	@cargo build --release --quiet

fmt:
	@cargo fmt --check

clippy:
	@cargo clippy --quiet --release -- -D warnings

test:
	@cargo test --release --quiet

# meetcat/go.mod carries `replace github.com/marcelocantos/claudia =>
# ../../claudia`, so this leg builds against a sibling claudia checkout — one
# that has moved to v0.30 while go.mod requires v0.6.0. The wrapper runs the
# same `go vet` + `go test`, declares that known redness in a ledger the gate
# prints, and blocks on anything else. PAGEFLIP_REQUIRE_MEETCAT=1 makes the
# known red blocking too.
meetcat-test:
	@scripts/gate-meetcat.sh

bullseye: gate
	@dirty=$$(git status --porcelain | grep -vE 'bullseye\.yaml$$' || true); \
	if [ -z "$$dirty" ]; then echo "✓ working tree clean"; \
	else \
	  echo "⚠  dirty working tree (warning only):"; \
	  echo "$$dirty"; \
	fi
	@echo "✓ bullseye invariants green"
