# ReconcileOS

Monorepo for the ReconcileOS platform, including:

- `api` (Go + Gin) REST API for auth, triage, queue, attestations, repo status, and execution lifecycle.
- `runtime` (Go) execution runtime for queued bot runs.
- `cli` (Rust) `recos` command-line client.
- `web` frontend app.
- `infra` and `supabase` infrastructure/schema assets.

## Current Status

Phase 1 CLI + API flow is implemented:

- `recos login` opens OAuth flow, receives callback on localhost, exchanges code for session, stores token in system keychain.
- `recos status` resolves current git repo and shows reconciliation status.
- `recos run <bot-id>` triggers execution (`dry_run: true` by default), polls status, prints result/diff, exits non-zero on failure.
- `recos verify <hash>` verifies public attestation records.
- `recos publish` is a Phase 3 stub.

API endpoints added for CLI support:

- `GET /api/v1/repos/:repo_full_name/status` (JWT protected)
- `POST /api/v1/executions/trigger` (JWT protected)
- `GET /api/v1/executions/:id/status` (JWT protected)
- `POST /api/v1/attestations/verify` (public)

## Repository Layout

- `api/` Go module (`reconcileos.dev/api`)
- `runtime/` Go module (`reconcileos.dev/runtime`)
- `cli/` Rust crate (`recos`) in workspace root `Cargo.toml`
- `DATA_FLOW_DIAGRAM.md` continuously updated architecture/data-flow artifact

## Quick Start

### CLI (development build)

From repo root:

```bash
cargo check -p recos
cargo run -p recos -- --help
```

### CLI (Linux musl static build)

```bash
rustup target add x86_64-unknown-linux-musl
cargo build --release --package recos --target x86_64-unknown-linux-musl
```

Binary output:

`target/x86_64-unknown-linux-musl/release/recos`

### CLI environment

```bash
export RECOS_API_URL="http://localhost:8080"
export RECOS_GITHUB_OAUTH_URL="https://github.com/login/oauth/authorize"
export RECOS_CALLBACK_PORT="9876"
```

## Testing

- API: `go test ./api/...`
- CLI: `cargo check -p recos`

## Notes

- Go workspace currently declares `go 1.22.2` in `go.work`.
- The CLI binary is not installed globally by default; run via full path or install to your `PATH`.
