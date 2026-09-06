.PHONY: bullseye gate build test fmt clippy meetcat-test hooks

# The gate the pre-push hook runs: every oracle this repo already has, on both
# legs. `meetcat-test` is the Go leg — until now it ran nowhere.
gate: fmt clippy build test meetcat-test
	@echo "✓ gate green"

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
# ../../claudia`, so this leg builds against a sibling claudia checkout.
meetcat-test:
	cd meetcat && go vet ./... && go test ./... -count=1

bullseye: gate
	@dirty=$$(git status --porcelain | grep -vE 'bullseye\.yaml$$' || true); \
	if [ -z "$$dirty" ]; then echo "✓ working tree clean"; \
	else \
	  echo "⚠  dirty working tree (warning only):"; \
	  echo "$$dirty"; \
	fi
	@echo "✓ bullseye invariants green"
