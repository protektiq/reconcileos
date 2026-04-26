# Runtime Verification Runbook

## Preconditions

- `SUPABASE_URL` and `SUPABASE_SERVICE_ROLE_KEY` are set.
- Supabase migrations are applied, including runtime lock RPC migration.
- At least one bot is installed with an active installation and valid manifest.

## Manual Verification Steps

1. Start runtime:
   - `go run .`
2. Insert a test event with `processed=false` in `events`.
3. Confirm dispatcher behavior:
   - event is marked `processed=true`
   - matching `executions` rows are created with `status='queued'`
4. Confirm executor behavior:
   - queued execution transitions to `running` with `started_at`
   - execution transitions to `completed` and stores `result` on success
5. Timeout validation:
   - use a bot that sleeps beyond 10 minutes
   - verify execution transitions to `failed` with timeout message
6. Review flag validation:
   - emit stdout JSON containing `{"requires_review": true}`
   - verify execution `requires_review=true`
7. Graceful shutdown validation:
   - send `SIGTERM` while an execution is running
   - verify runtime stops fetching new work
   - verify runtime waits for in-progress work up to 30 seconds before force-cancel
