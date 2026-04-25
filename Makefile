.PHONY: dev build lint test docker-build

dev:
	@echo "Starting web dev server..."
	npm --prefix web run dev

build:
	@echo "Building Go modules..."
	cd api && go build ./...
	cd runtime && go build ./...
	@echo "Building Rust workspace..."
	cargo build --workspace
	@echo "Building web..."
	npm --prefix web run build

lint:
	@echo "Linting Go modules..."
	cd api && go fmt ./...
	cd runtime && go fmt ./...
	@echo "Linting Rust workspace..."
	cargo fmt --all --check
	cargo clippy --workspace --all-targets -- -D warnings
	@echo "Linting web..."
	npm --prefix web run lint

test:
	@echo "Testing Go modules..."
	cd api && go test ./...
	cd runtime && go test ./...
	@echo "Testing Rust workspace..."
	cargo test --workspace
	@echo "Testing web (if configured)..."
	npm --prefix web run test --if-present

docker-build:
	@echo "Building Docker images (placeholders)..."
	docker build -t reconcileos-api ./api
	docker build -t reconcileos-runtime ./runtime
	docker build -t reconcileos-web ./web
