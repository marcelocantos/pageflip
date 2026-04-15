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
	@cargo test --quiet
