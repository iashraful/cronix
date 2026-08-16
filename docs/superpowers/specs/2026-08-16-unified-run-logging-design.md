# Cronix v2.1 — Unified Run Logging Design

Date: 2026-08-16
Status: Approved (approach A)

## Problem

Job-run failures are hard to read:

- **Scheduled fire failures** log one compact line: `job=<id> schedule="<expr>" attempt=1/1 exit=6 output=<tail>` — decent, but no job name, no command, and the output is crammed.
- **Manual `run` failures** log only a chained error `%v`: `job a832940ec3e7 run finished with non-zero exit: command failed after exhausting retries: ... (last exit=6)` — no attempt detail, no output, no context.
- **CLI `run`** prints compact `attempt=1/1 exit=6 output=...` lines with no job name/command.

The three surfaces each format run output differently, so failure detail is inconsistent and hard to scan in `docker logs`.

## Goals

- One human-readable run block rendered identically across server logs, the API `run` handler log, and the CLI `run` output.
- The API `run` **response contract is unchanged**: `200` `{steps:[...]}` for both success and exhausted-retries; only genuine faults → `500`.
- Scheduled fires stay log-friendly: compact one-liner on **success**, full block only on **failure**.

## Design

### `run_log.go` (new, package main)

Single renderer:

```go
func formatRun(job Job, results []Result, err error) string
```

Block shape (no trailing newline):

```
run job=<id> name=<name> schedule="<expr>" command="<curl>"
  attempt 1/1 exit=6
    output: curl: (6) Could not resolve host: ...
  result: FAILED (last exit=6, 1/1 attempts)
```

Rules:

- `name=` is omitted when the job's `Name` is empty (no blank label).
- `schedule=` and `command=` always present.
- `Output` is truncated via the existing `tail`/`outputCap` (4096); when empty, prints `(no output)`.
- The `result:` line:
  - `errors.Is(err, ErrCommandFailed)` → `FAILED (last exit=<n>, <attempt>/<total> attempts)`
  - `err == nil` → `OK`
  - any other error → `ERROR (<err>)`
- With zero results, the per-attempt section is omitted.

### `controller.go` — `fire()`

- Success: keep the current compact line (`job=... schedule=... attempt=1/1 exit=0`).
- Failure (`ErrCommandFailed`): log the full `formatRun` block.
- Other errors (startup/parse): log the full block; the `result:` line carries the `ERROR (...)` text.

### `api.go` — `handleRun`

- Log the full `formatRun` block for **every** manual run (success and failure) — this is what lands in `docker logs`.
- Response: `200` `{steps:[...]}` on both OK and exhausted-retries; genuine faults → `500` via `writeCtrlError`. No contract change.

### `cli.go` — `cliRun`

- Fetch the job first via the existing `cliGetJob(opts, id)`; on failure, the existing honest error path stands.
- Render `formatRun(job, steps, <derived error>)` and print it.
- A failed-but-completed run returns `200` from the API, so the CLI prints the block and exits `0` (failure is a domain outcome, not a CLI error).
- `result:` derivation on the CLI: the API returns only `steps`, so the CLI infers `FAILED` when the last step's exit is non-zero, else `OK` (the API hides `ErrCommandFailed` inside a 200).

## Testing

- `run_log_test.go`: `formatRun` cases — success, failed-with-output, no-output, empty name, non-command error, zero results.
- Update existing tests where assertions touch run/log output (controller, api, cli run tests).
- Gate: `go build ./... && go vet ./... && go test ./...` green, `gofmt -l .` clean.

## Out of scope

- No API contract change, no `Result`/`Job` struct changes, no store/auth/SPA changes.
- No structured JSON logging; logs remain human-readable text.
