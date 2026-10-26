.PHONY: bullseye build test fmt clippy

bullseye: fmt clippy build test
	@echo "✓ bullseye invariants green"

build:
	@cargo build --release --quiet

fmt:
	@cargo fmt --check

clippy:
	@cargo clippy --quiet --release -- -D warnings

test:
	@cargo test --release --quiet

# Publish the formula to the Homebrew tap for an existing release. Run after
# the release assets exist — tapper reads them to build the formula.
release-tap:
	./scripts/release-tap.sh $(TAG)

.PHONY: release-tap
