# Cronix v2 Design: Live Scheduler with CLI, REST API, and SPA

Date: 2026-08-15
Status: Approved

## Overview

Cronix runs cron-style scheduled jobs inside a Docker container. Each job
shells out to a statically-linked `curl` binary to make an HTTP call.

Cronix v1 configures jobs solely from `CRON_*` environment variables read once
at startup. v2 adds a live control surface: a CLI, a REST API, and a small
frontend, all shipped in the same single `scratch`-based container. Jobs become
mutable at runtime with **no container restart** — the scheduler reconciles
itself in-place.

This document replaces the v1 design (`2026-08-15-cronix-design.md`).

## Goal / Success Criteria

- A user can create, read, update, delete, enable, disable, and manually run
  scheduled jobs *at runtime* — without editing env vars or restarting the
  container.
- The same CRUD is available three ways: CLI (via `docker exec`), REST API,
  and a thin browser frontend.
- All three surfaces share one control path in a single process, so state never
  diverges.
- The runtime image remains `scratch` (single static binary + static curl +
  CA bundle).
- Storage is behind a `Store` interface: a JSON file today, SQLite swappable
  later without touching callers.

## Non-Goals

- No multi-container orchestration, no external auth service, no multi-user
  tenancy.
- No shell features (`&&`, pipes, `$VAR` expansion, globs) inside curl
  commands — commands are tokenized, not shell-executed.
- No per-run history persistence beyond the single manual-run result shown to
  the CLI/SPA caller. Scheduled-run results continue to go to stdout only.
- No migration of existing env-var config: `CRON_*` job env vars are removed.

## Architecture

Single binary `/cronix`, two modes selected by the first argument:

```
/cronix                      → server mode: scheduler + REST API + SPA
/cronix cli <command...>     → CLI client, talks to the server over HTTP
```

Server mode data flow:

```
JSON file ──> load ─────────────────────────────┐
                                               ▼
REST API /api/v1 ──> controller (mutex) ──> robfig/cron scheduler
SPA (embedded web/)      │  ▲                     │  (on fire)
                         ▼  │                     ▼
                    Store.Save()            shlex ──> exec curl ──> log
CLI ──HTTP─> same REST API
```

Components:

1. **Store (`store.go`)** — interface + file implementation.
   ```go
   type Store interface {
       Load() ([]Job, error)
       Save([]Job) error
   }
   ```
   `FileStore` writes atomically (temp file + rename), creating the parent
   directory if needed. One writer: the server process.

2. **Controller (`controller.go`)** — owns the authoritative in-memory job
   list and reconciles it with the `robfig/cron` registry inside a mutex.
   Exposes the operations the API and CLI both end up calling.

3. **Runner (`runner.go`)** — unchanged in behavior: `shlex`-tokenize,
   verify `tokens[0]=="curl"`, `exec` the curl binary (no shell), capture
   combined output capped at 4 KiB, retry with backoff on non-zero exit.
   Manual runs reuse it.

4. **HTTP API + SPA (`api.go`, `web/`)** — REST under `/api/v1`; the SPA is
   embedded with `go:embed` and served at `/`.

5. **CLI (`cli.go`)** — thin HTTP client to the same `/api/v1` endpoints,
   rendering results as text.

## Job Model

Struct replaces the v1 env-var `Job`:

```go
type Job struct {
    Id         string `json:"id"`
    Name       string `json:"name"`
    Schedule   string `json:"schedule"`    // 5-field cron
    Curl       string `json:"curl"`        // full curl command; tokens[0]=="curl"
    Retries    int    `json:"retries"`     // default 0
    RetryDelay int    `json:"retry_delay"` // seconds, default 5; only used when retries > 0
    Enabled    bool   `json:"enabled"`     // default true; false = paused
}
```

- `Id` is optional at create: if omitted, the server generates a short random
  hex id. When supplied it must be non-empty, unique, and match
  `^[A-Za-z0-9_-]{1,128}$`.
- `Name` is optional.
- Disabled jobs keep their metadata in the list but are not registered with
  cron, so they never fire.
- Validate-at-write rules per job: `schedule` parses as standard 5-field cron,
  `curl` present and `tokens[0]=="curl"`, `retries`/`retry_delay` non-negative
  integers.

## Persistence

- `CRONIX_STORE_PATH` (default `/data/jobs.json`) holds an array of jobs.
- The server keeps the in-memory list authoritative and persists after every
  mutating operation.
- First start with no file present: the server runs with zero jobs and logs a
  hint to add one via CLI/API. No env seeding. (v1 `CRON_*` job discovery is
  removed.)
- Save ordering on mutation: apply to in-memory list → reconcile scheduler →
  `Store.Save()`. If `Save` fails, roll back the in-memory list and scheduler
  and return an error so no half-applied state is visible.
- Storage failure at `Load()` on startup logs the error; see Error Handling.

## REST API (`/api/v1`)

| Method   | Path                       | Body → Result |
|----------|----------------------------|---------------|
| `GET`    | `/api/v1/jobs`             | list all jobs |
| `POST`   | `/api/v1/jobs`             | `{...job}` (id optional; server generates if omitted) → 201 + created job |
| `GET`    | `/api/v1/jobs/{id}`        | single job, 404 if missing |
| `PUT`    | `/api/v1/jobs/{id}`        | full replace, 404 if missing |
| `DELETE` | `/api/v1/jobs/{id}`        | 204, 404 if missing |
| `POST`   | `/api/v1/jobs/{id}/run`    | manual run → `{steps: [{attempt, exit, output}...]}`; 404 if missing |

Contract:

- Request/response bodies are JSON.
- Error responses: `{"error": "<message>"}` with status 400 (validation),
  401 (bad/missing token), 404 (unknown id), 500 (storage failure).
- Success: `200`/`201` return job object(s) directly (`DELETE` → `204`).
- `POST /jobs/{id}/run` performs the manual run synchronously and returns the
  per-attempt results (already capped output); a non-zero final exit is still
  `200` with the results, so the caller can see them.

## Authentication

- Every `/api/v1/*` request must carry `Authorization: Bearer <token>` where
  the token is `CRONIX_API_TOKEN`.
- Token is **required**: the server refuses to start if
  `CRONIX_API_TOKEN` is unset, empty, or whitespace.
- Missing/wrong token → `401`.
- Token comparison uses `crypto/subtle.ConstantTimeCompare` to avoid timing
  leaks.
- The SPA and CLI obtain the token from the environment/user input and send
  the same header.

## CLI

Usage after the `cli` subcommand (`cronix cli <command> ...`):

```
cronix cli list
cronix cli get <id>
cronix cli add [--id myjob] --name n --schedule '*/5 * * * *' --curl 'curl -s https://x' [--retries 3] [--retry-delay 10]
cronix cli update <id> [--name n] [--schedule s] [--curl c] [--retries n] [--retry-delay s] [--enabled=true|false]
cronix cli delete <id>
cronix cli enable <id>
cronix cli disable <id>
cronix cli run <id>
```

- Talks to `CRONIX_HTTP_ADDR` (default `http://127.0.0.1:8080`).
- Token from `CRONIX_API_TOKEN` env var or `--token` flag.
- `add`/`update` validate locally before sending (same rules as the API) for
  fast feedback.
- `list` prints a table: `id name enabled schedule`.
- `run` prints the per-attempt `exit`/`output` lines from the response.
- Exit codes: `0` success, `1` usage/argument error, `2` API/HTTP error
  (prints the API `{"error": ...}` message or HTTP error).

## SPA (Frontend)

- Plain HTML + vanilla JS + minimal CSS in `web/`, embedded with `go:embed`
  and served at `/` by the server process. No build toolchain.
- Pages: jobs list table (id, name, enabled toggle, schedule) with per-row
  edit/run/delete actions; add/edit form with inline validation errors; run
  result panel showing the output tail.
- Token entered once in a login bar, stored in `sessionStorage`, sent as
  `Authorization: Bearer <token>` on every `/api/v1` fetch.
- On auth failure (401) the SPA clears the stored token and shows the login
  bar.

## Scheduler Reconciliation (Live Reload)

- Controller holds `map[id → cron.EntryID]` for enabled jobs.
- **Add/Enable:** parse+validate, if enabled `c.AddFunc`, store entry id.
- **Update:** if scheduled 5-field expression or enabled flag changed, remove
  old entry and re-add; name/retries/retry-delay/curl update in place (curl is
  read at run time; retries apply per run). Schedule expression is immutable
  string; an update that keeps the same schedule keeps the same entry.
- **Delete/Disable:** `c.Remove(entryID)`, drop from map.
- All controller ops run under a single mutex; cron callbacks and the manual
  `run` endpoint take the same lock so a manual run and a scheduled fire on the
  same job cannot interleave corruptly.

## Runner

Behavior preserved from v1 (`runner.go`):

- `ParseCommand`: `shlex.Split`, reject empty/no-token, reject
  `tokens[0] != "curl"`.
- `Run(job, curlPath)`: up to `Retries+1` attempts, sleeping `RetryDelay` on
  failure, stopping at first exit 0; each attempt records attempt number /
  total / exit code / output tail (≤ 4 KiB).
- Retry delay uses the job's `RetryDelay` in seconds.

## Docker Image

`scratch` runtime retained; new modules compose into the same single binary.

- `web/` is copied into the build context (already covered by `COPY . .`) and
  embedded at compile time.
- Volume: users mount a directory for `CRONIX_STORE_PATH` (e.g.
  `-v myjobs:/data`). `FileStore` creates the parent dir at first save.
- `ENTRYPOINT` stays `["/cronix"]`; `docker exec <c> /cronix cli ...`

## Environment Variables

| Variable | Default | Notes |
|---|---|---|
| `CRONIX_STORE_PATH` | `/data/jobs.json` | JSON store file |
| `CRONIX_HTTP_ADDR` | `:8080` | API + SPA listener |
| `CRONIX_API_TOKEN` | *(required)* | bearer token; server refuses to start without it |
| `CURL_PATH` | `/usr/local/bin/curl` | unchanged from v1 |
| `TZ` | local | container timezone; unchanged |

`CRON_SCHEDULE_*` / `CRON_CURL_*` / `CRON_RETRIES_*` / `CRON_RETRY_DELAY_*` are
removed.

## Logging

Server-mode stdout lines (visible via `docker logs`):

- Startup: `cronix started with <n> job(s)`, plus `api listening on <addr>`.
- Every scheduled or manual run attempt: `job=<id> schedule="<expr>"
  attempt=<n>/<total> exit=<code> output=<tail>`.
- Mutation summaries: `job=<id> added` / `updated` / `deleted` / `enabled` /
  `disabled`.
- Shutdown: on `SIGTERM`/`SIGINT`, stop scheduling, wait up to 10s for
  in-flight jobs, exit 0.

## Error Handling

- Startup: unmarshalable store file → log error and exit non-zero. Missing
  `CRONIX_API_TOKEN` → log error and exit non-zero.
- API: validation → 400 naming the field; missing token → 401; unknown id →
  404; `Store.Save` failure → 500 after rollback; curl binary missing at
  `run` time → 500 with a message.
- Scheduled run failures: logged as failure lines; scheduler continues.
- In-flight jobs drained (bounded 10s) on shutdown.

## Testing

Unit tests (`go test ./...`, `go vet ./...` gate):

- `store_test.go` — load/save round-trip, atomic write (temp+rename), parent
  dir creation, corrupt file error, default path.
- `controller_test.go` — add/update/delete/enable/disable reconcile cron
  entries correctly; save-failure rollback restores prior state; mutex prevents
  concurrent drift (tested via parallel goroutines).
- `api_test.go` — `httptest` per route: 200/201/204/400/401/404/500; validation
  messages name the field; auth enforced via constant-time compare.
- `cli_test.go` — flag parsing, local validation errors, output rendering;
  CLI against a stub HTTP server.
- `runner_test.go` (adapted) — existing parse/retry/truncation tests updated to
  the new `Job` shape (`Id` field added, `RetryDelay` remains).
- `config_test.go` — replaced: no longer loads `CRON_*`; removed or slimmed to
  document the removal.

Manual E2E:

1. `docker build`; run with `CRONIX_API_TOKEN`, a mounted volume, and a
   schedule that fires quickly.
2. `docker exec` the CLI to add a job; watch `docker logs` for its fire.
3. SPA: add/edit/disable/run via browser with the token.
4. Restart container; confirm jobs persisted and schedules intact.

## Migration

- v1 users reset their config to the JSON file (one-time manual step). The
  removal of `CRON_*` is intentional and documented in the README.