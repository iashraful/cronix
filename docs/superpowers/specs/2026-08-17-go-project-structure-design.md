# Cronix — Idiomatic Go Project Structure Design

Date: 2026-08-17
Status: Approved

## Problem

All of Cronix's Go code lives flat in the repo root as `package main`: ten
production files and their tests share one package with no import boundaries.
The user wants a "nice project structure" — the idiomatic Go layout
(`cmd/` + `internal/` with fine-grained, single-purpose packages). No feature
changes; this is pure restructuring with behavior preserved.

## Decisions (from brainstorming)

- Layout: `cmd/` + `internal/` (module stays `cronix`; no path rename).
- Granularity: fine-grained domain packages, each with one purpose.
- `auth` and `spa` stay as separate packages (not folded into `api`).
- No new Go module dependencies; `go.mod` keeps `shlex` + `robfig/cron/v3`.

## Target Structure

```
cronix/
├── cmd/cronix/main.go          # wiring only: server mode + `cli` dispatch
├── internal/
│   ├── model/                  # Job, Result, Run, RunSummary, sentinel errors
│   ├── fsutil/                 # atomicWriteFile (shared by both stores)
│   ├── store/                  # Store interface + FileStore
│   ├── runstore/               # RunStore interface + FileRunStore
│   ├── runner/                 # ParseCommand, runJob (curl exec, retries, output cap)
│   ├── runlog/                 # formatRun (used by controller, api, cli)
│   ├── controller/             # Controller: cron scheduling, validation, run ledger
│   ├── auth/                   # issueSessionToken / validateSessionToken
│   ├── spa/                    # SPAHandler + //go:embed dist (assets in internal/spa/dist/)
│   ├── api/                    # Server, handlers, AuthConfig
│   └── cli/                    # RunCLI + all subcommands
├── ui/                         # unchanged (Vite source)
├── Dockerfile, Makefile, README.md, go.mod, docs/
```

## Design

### Package contents (what moves where)

| New package | Files | Notes |
|---|---|---|
| `internal/model` | `Job`, `Result`, `Run`, `RunSummary` types + sentinel errors `ErrNotFound`, `ErrValidation`, `ErrStorage`, `ErrCommandFailed` | Pure types + errors; no behavior. |
| `internal/fsutil` | `atomicWriteFile` | Shared atomic temp-file+rename helper. |
| `internal/store` | `Store` interface, `FileStore`, `NewFileStore` | Imports `model`, `fsutil`. |
| `internal/runstore` | `RunStore` interface, `FileRunStore`, `NewFileRunStore` | Imports `model`, `fsutil`. |
| `internal/runner` | `ParseCommand`, `runJob`, `tail`, `outputCap` | Imports `model`, `shlex`. |
| `internal/runlog` | `formatRun` | Imports `model`. |
| `internal/controller` | `Controller`, `NewController`, `Start`, `Stop`, `List`, `Get`, `Create`, `Update`, `Delete`, `Enable`, `Disable`, `Run`, `History`, `LastRun`, `genID`, validation/registration | Imports `model`, `store`, `runstore`, `runner`, `runlog`, `cron`. |
| `internal/auth` | `issueSessionToken`, `validateSessionToken` | Stdlib only. |
| `internal/spa` | `SPAHandler`, `//go:embed dist` | Assets build into `internal/spa/dist/`. Stdlib only. |
| `internal/api` | `Server`, `NewServer`, `AuthConfig`, `jobRequest`, `jobResponse`, handlers | Imports `controller`, `auth`, `spa`, `model`. |
| `internal/cli` | `RunCLI`, subcommands, `cliHTTP`, `cliErr`, `usage`, `formatRun` usage | Imports `model`, `runlog`. |
| `cmd/cronix/main.go` | `main`, `envOr`, `envDuration`, signal handling | Imports `controller`, `store`, `runstore`, `api`, `cli`. |

### Dependency flow (no cycles)

```
cmd/cronix → api, controller, cli
api → controller, auth, spa, model
controller → store, runstore, runner, runlog, model
cli → runlog, model
store/runstore → fsutil, model
runner → model      runlog → model      auth, spa → (stdlib only)
```

### Embed relocation (`dist/`)

`//go:embed dist` must reference a directory inside its package. The built UI
output moves from repo-root `dist/` to `internal/spa/dist/`:

- `ui/vite.config.js`: `outDir: '../dist'` → `'../internal/spa/dist'`
  (relative to `ui/`).
- `.gitignore`: `/dist/` → `/internal/spa/dist/` (keep `/web/`).
- `.dockerignore`: `dist` already excludes any `dist` path; add
  `/internal/spa/dist` explicitly to be safe.
- Dockerfile node stage: `COPY ui/ ./` + `npm run build` now emits
  `/internal/spa/dist` relative to `/ui`; the Go stage copies
  `COPY --from=web /internal/spa/dist ./internal/spa/dist`.
- `make ui` unchanged (`npm --prefix ui run build`); output lands in
  `internal/spa/dist/`.
- README: update the `go:embed` mention (`internal/spa/dist`) and the
  "Building from Source" note.

### `main.go` (wiring only)

Unchanged behavior, moved to `cmd/cronix/main.go`:
- `cronix cli ...` → `cli.RunCLI(os.Args[2:])`.
- Server mode → build `controller.NewController(store.NewFileStore(...),
  runstore.NewFileRunStore(...), curlPath)`, then `api.NewServer(ctrl, cfg)`.
- Signal handling + graceful shutdown preserved.

### Tests

- Each test file moves with its production file.
- `api` package tests define local in-memory fakes for `store.Store` and
  `runstore.RunStore` (no longer borrow `controller`'s test helpers, which
  are unexported in another package).
- `controller` package tests keep their own `memStore`/`memRunStore`.
- SPA tests (`spa_test.go`) move to `internal/spa` and test `SPAHandler`
  directly (deep-route fallback included).
- CLI tests move to `internal/cli`.

### Global Constraints

- No new Go module dependencies.
- No behavior change: schedules, retries, API routes, JSON shapes, CLI output,
  web UI, Docker image contents all identical.
- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .` clean.
- Final Docker image stays binary-only (`/cronix`, curl, certs).

## Testing

- `go build ./...` compiles all packages.
- `go vet ./...` and `go test ./...` pass (controller, store, runstore,
  runner, api, cli, spa, auth suites).
- `npm --prefix ui run build` emits into `internal/spa/dist/`; `go:embed`
  picks it up.
- `docker build` succeeds and image stays binary-only.

## Out of scope

- No feature changes, API changes, or UI changes.
- No module path rename (`cronix` stays `cronix`).
- No new dependencies.
