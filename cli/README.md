# CLI

Rust CLI (`recos`) for ReconcileOS.

## Commands

- `recos login` authenticate with GitHub OAuth and persist session token in system keychain.
- `recos status` show reconciliation status for the current git repository.
- `recos run <bot-id>` trigger bot execution against the current repository (dry-run by default).
- `recos run <bot-id> --live` trigger non-dry-run execution.
- `recos verify <hash>` verify attestation chain for an artifact hash (public API endpoint).
- `recos publish` Phase 3 stub.

## Configuration

Environment variables:

- `RECOS_API_URL` (default `http://localhost:8080`)
- `RECOS_GITHUB_OAUTH_URL` (default `https://github.com/login/oauth/authorize`)
- `RECOS_CALLBACK_PORT` (default `9876`)

## Linux static binary build (musl)

```bash
rustup target add x86_64-unknown-linux-musl
cargo build --release --package recos --target x86_64-unknown-linux-musl
```
