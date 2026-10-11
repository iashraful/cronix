# Cronix Agent Guide

This document provides a comprehensive architecture blueprint and operational guide for AI agents working in this repository. It gives complete context on codebase structure, design patterns, domain models, concurrency guarantees, and invariants without needing to scan the entire tree.

---

## 1. Project Overview

**Cronix** is a standalone, lightweight, cron-style HTTP scheduler built in Go with an embedded React web UI. It is designed to run in minimal environments (such as a Docker image based on `scratch`).

* **Core philosophy**: No shell, no heavy runtimes, no external database dependencies. Every scheduled job executes directly via a statically-linked `curl` binary using `exec.CommandContext`.
* **Runtime modes**:
  1. **Server mode** (`cronix`): Starts scheduler, HTTP REST API, and embedded SPA.
  2. **CLI mode** (`cronix cli <command>`): Command-line client that communicates with a running Cronix server via its REST API.

---

## 2. Repository Layout

```
.
├── cmd/
│   └── cronix/
│       └── main.go                 # Entrypoint for both server and CLI modes
├── internal/
│   ├── api/                        # HTTP REST API router, handlers, auth middleware
│   ├── auth/                       # HMAC-SHA256 session token generation and verification
│   ├── cli/                        # CLI subcommand handlers (list, add, get, update, run, etc.)
│   ├── controller/                 # Scheduler engine, robfig/cron, job lifecycle, concurrency
│   ├── fsutil/                     # Atomic file write utilities (temp file + fsync + rename)
│   ├── model/                      # Core domain types (Job, Run, Result) and sentinel errors
│   ├── runlog/                     # Human-readable execution log formatting
│   ├── runner/                     # Command parsing (shlex) and curl execution with timeouts
│   ├── runstore/                   # JSON persistence for run execution history
│   ├── spa/                        # Serves embedded React UI assets (go:embed dist)
│   │   └── dist/                   # Built production bundle from ui/
│   └── store/                      # JSON persistence for job configurations
├── ui/                             # React 18 + Vite frontend
│   ├── src/
│   │   ├── components/             # Reusable UI widgets (JobEditor, StatusDot, Badge, etc.)
│   │   ├── views/                  # Primary pages: Dashboard.jsx, JobDetail.jsx
│   │   ├── api.js                  # Fetch wrapper for Cronix REST API
│   │   └── lib.js                  # Utilities (relative time, filtering, stats)
│   └── vite.config.js              # Vite config; builds into ../internal/spa/dist
├── Dockerfile                      # Multi-stage build (Node -> Go + static curl -> scratch)
├── Makefile                        # Development automation (build, test, lint, ui, run)
└── features.md                     # Roadmap and feature ideas
```

---

## 3. Core Architecture & Component Map

```
                     ┌───────────────────────────────┐
                     │          Entrypoint           │
                     │       (cmd/cronix/main.go)    │
                     └───────────────┬───────────────┘
                                     │
           ┌─────────────────────────┴─────────────────────────┐
           ▼                                                   ▼
   [CLI Subcommands]                                   [Server Mode]
  (internal/cli/cli.go)                                        │
           │                                                   ▼
           │ HTTP Calls                        ┌───────────────────────────────┐
           └──────────────────────────────────>│      API Server (mux)         │
                                               │      (internal/api/api.go)    │
                                               └───────────────┬───────────────┘
                                                               │
                                                               ▼
                                               ┌───────────────────────────────┐
                                               │         Controller            │
                                               │(internal/controller/controller)
                                               └───────┬───────────────┬───────┘
                                                       │               │
                            ┌──────────────────────────┘               └──────────────────────────┐
                            ▼                                                                     ▼
             ┌─────────────────────────────┐                                       ┌─────────────────────────────┐
             │       robfig/cron/v3        │                                       │       Runner Pipeline       │
             │     (Standard 5-field)      │                                       │   (internal/runner/runner)  │
             └──────────────┬──────────────┘                                       └──────────────┬──────────────┘
                            │                                                                     │
                            ▼                                                                     ▼
             ┌─────────────────────────────┐                                       ┌─────────────────────────────┐
             │     Job Locks & TryLock     │                                       │    exec.CommandContext      │
             │   (Skip overlapping ticks)  │                                       │ (curl binary + timeout ctx) │
             └─────────────────────────────┘                                       └─────────────────────────────┘
                            │                                                                     │
                            └──────────────────────────┬──────────────────────────────────────────┘
                                                       ▼
                                       ┌───────────────────────────────┐
                                       │     Persistence / Storage     │
                                       │    store/ & runstore/         │
                                       │    jobs.json  &  runs.json    │
                                       └───────────────────────────────┘
```

---

## 4. Key Packages & Responsibilities

### `cmd/cronix/main.go`

* Resolves configuration via environment variables:
  * `CRONIX_API_TOKEN`: Required master API token.
  * `CRONIX_STORE_PATH`: Path to jobs JSON file (default `/data/jobs.json`).
  * `CRONIX_RUNS_PATH`: Path to run history JSON file (default `/data/runs.json`).
  * `CRONIX_HTTP_ADDR`: Server address (default `:8080`).
  * `CURL_PATH`: Path to curl binary (auto-detects `curl` via `exec.LookPath`, falls back to `/usr/local/bin/curl`).
  * `CRONIX_JOB_TIMEOUT`: Maximum execution time per curl attempt (default `60s`).
* Handles clean shutdown on `SIGINT`/`SIGTERM` via `srv.Shutdown(ctx)` (10s timeout) and `ctrl.Stop()`.

### `internal/model`

* `model.Job`: Schema for a job configuration:
  * `Id` (string, regex `^[A-Za-z0-9_-]{1,128}$`)
  * `Name` (string, optional)
  * `Schedule` (string, 5-part cron syntax)
  * `Curl` (string, command string starting with `curl`)
  * `Retries` (int, default `0`, `>= 0`)
  * `RetryDelay` (int, seconds between attempts, default `5`, `>= 0`)
  * `Enabled` (bool, whether cron scheduler executes it)
* `model.Run`: Completed run record (`JobId`, `Trigger`, `Time`, `Status`, `ExitCode`, `Results`).
* `model.Result`: Per-attempt record (`Attempt`, `Total`, `ExitCode`, `Output`).
* Sentinel errors: `ErrNotFound`, `ErrValidation`, `ErrStorage`, `ErrCommandFailed`.

### `internal/controller`

* Orchestrates scheduled and manual runs.
* **Concurrency model**:
  * `jobLocks map[string]*sync.Mutex`: Per-job locking. Job A and Job B run in parallel without blocking each other.
  * **Scheduled runs (`c.fire`)**: Acquire job lock via `jobMu.TryLock()`. If a job is already in flight, the scheduled tick is immediately skipped to prevent goroutine backlog.
  * **Manual runs (`c.Run`)**: Acquire job lock via `jobMu.Lock()` to wait for any in-flight execution to finish cleanly before running.
  * **Disk I/O decoupling**: In `recordRun` and `Delete`, `c.mu` is locked only to update in-memory state and snapshot records. Disk I/O (`SaveRuns`) is executed after unlocking `c.mu`, guarded by `runStoreMu`.
* Keeps a rolling history cap of **50 runs per job**.

### `internal/runner`

* Parses command strings using `github.com/google/shlex`.
* Validates that the command starts strictly with `curl` (no arbitrary shell commands or pipelines).
* Executes the binary directly without an intermediate shell via `exec.CommandContext`.
* Caps stdout/stderr output per attempt to 4096 bytes (tail).
* Per-attempt timeout: Uses context deadline. If curl stalls or hangs, the process is killed and exit code `124` is recorded.

### `internal/fsutil`

* `AtomicWriteFile(pattern, path, data)`: Writes to a temporary file in the target directory, calls `tmp.Sync()` to flush filesystem cache to disk, closes, sets `0644` permissions, and atomically renames the temp file to `path`.

### `internal/auth`

* Provides stateless session tokens for web UI sign-in:
  * Keyed by the master `CRONIX_API_TOKEN`.
  * Encoded as `base64(payload).base64(hmacSHA256)`.
  * Validates token expiration timestamp (`exp`).

### `internal/api`

* REST API endpoints:
  * `POST /api/v1/login`: Issues session token given username/password.
  * `GET /api/v1/jobs`: List all jobs with their `last_run` summary and `next_run` timestamp.
  * `POST /api/v1/jobs`: Create job.
  * `GET /api/v1/jobs/{id}`: Get job details.
  * `PUT /api/v1/jobs/{id}`: Replace/update job.
  * `DELETE /api/v1/jobs/{id}`: Delete job and prune its history.
  * `POST /api/v1/jobs/{id}/run`: Manually trigger job now (returns attempt steps).
  * `GET /api/v1/jobs/{id}/runs`: Get execution history for a job.
  * `GET /`: Serves embedded single-page application.
* Authenticates requests via `Authorization: Bearer <token>` (accepts either the master token or an active session token).

---

## 5. Development & Testing Commands

All tasks are managed via the root `Makefile`:

```sh
# Build UI bundle into internal/spa/dist
make ui

# Compile server binary to bin/cronix (automatically builds UI)
make build

# Run all Go unit and integration tests
make test

# Run Go static analysis (vet)
make vet

# Check code formatting (gofmt)
make lint

# Run UI unit tests (Vitest)
npm --prefix ui test

# Run server locally with dev environment defaults
make run
```

---

## 6. Critical Invariants for Agents

1. **Direct Binary Execution (No Shell)**:
   Never replace `exec.CommandContext` with `sh -c` or `bash -c`. Commands must remain tokenized and executed directly through the statically-built `curl` binary to prevent command injection and ensure portability inside `scratch` containers.

2. **Timeout Contexts on Process Spawns**:
   All external executions in `runner` must be bounded by a context timeout. Never revert to unbounded `exec.Command(curlPath, ...)`.

3. **Concurrency & Deadlock Prevention**:
   * Do not hold `c.mu` during network calls, process execution, or file writes.
   * Do not introduce a global mutex for job execution; maintain per-job locks (`getJobLock(id)`).
   * Scheduled triggers (`fire`) must never block waiting for an in-flight job; they must use `TryLock` and skip.

4. **UI Build Artifacts Location**:
   The Vite configuration outputs built assets directly into `internal/spa/dist/`. The Go package `cronix/internal/spa` embeds this directory using `//go:embed dist`. If UI changes are made, run `npm --prefix ui run build` before building or testing the Go binary.

5. **Atomic Storage**:
   Any persistence to disk (`store`, `runstore`) must go through `fsutil.AtomicWriteFile` to guarantee crash resilience and prevent partial writes.
