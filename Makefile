.PHONY: bullseye build test fmt clippy go-build go-test

bullseye: fmt clippy build test go-build go-test
	@echo "✓ bullseye invariants green"

build:
	@cargo build --release --quiet

fmt:
	@cargo fmt --check

clippy:
	@cargo clippy --quiet --release -- -D warnings

test:
	@cargo test --release --quiet

go-build:
	@cd meetcat && go build -o pageflip .

go-test:
	@cd meetcat && go test ./... > /dev/null
