# Cronix v3 — React UI + Persistent Run History Design

Date: 2026-08-16
Status: Approved (as designed)

## Problem

The embedded UI is a bare vanilla-JS page (web/index.html + app.js + style.css):
functional but visually minimal. Separately, run results are logged to stdout
only — after a restart there is no record of a job's past runs, so the UI cannot
show "last run" state or a history.

## Goals

- Replace the vanilla SPA with a **React + Vite** app, **plain CSS** (no
  Tailwind / component library), built into the existing `web/` embed directory.
- Match current feature parity (token auth, list, add/edit/delete, enable/
  disable, run-now with the unified result block).
- Extras: **last-run status badge** per job, **filter/search box**, **theme
  toggle**, and a **run-history panel** per job.
- Persist run history to a **separate `runs.json`** (existing `jobs.json`
  untouched, backward compatible), capped per job.
- Single container: Vite build happens in a Docker build stage; `go:embed`
  and `SPAHandler` stay unchanged.

## Design

### A. Backend — run history

#### `Run` record and store

New `Run` struct (store persists it):

```go
type Run struct {
	JobId    string    `json:"job_id"`
	Trigger  string    `json:"trigger"`   // "manual" | "scheduled"
	Time     time.Time `json:"time"`
	Status   string    `json:"status"`    // "ok" | "failed" | "error"
	ExitCode int       `json:"exit_code"` // last attempt's exit code
	Results  []Result  `json:"results"`   // same JSON as today's steps
}
```

`time.Time` marshals to RFC3339. `Status`/`ExitCode` are derived once at record
time (badge convenience); `Results` keeps the per-attempt detail.

New store interface + file store, mirroring `Store`/`FileStore`:

```go
type RunStore interface {
	LoadRuns() ([]Run, error)
	SaveRuns([]Run) error
}
```

- `FileRunStore{Path string}` writes to `runs.json` (default path
  `CRONIX_RUNS_PATH` env, fallback `/data/runs.json`).
- The atomic temp+rename write logic is extracted from `FileStore.Save` into a
  shared `atomicWriteFile(path, data) error` helper in `store.go`, reused by
  both file stores.
- `LoadRuns` returns `nil` on missing file (like `Store.Load`).

#### Controller ledger

- New field `runs map[string][]Run` (by job id), newest-first.
- `recordRun(job, trigger, results, err)`:
  - derives `Status` (`err == nil` → `ok`; `errors.Is(err, ErrCommandFailed)` →
    `failed`; else `error`) and `ExitCode` (last result's exit, `0` when none);
  - prepends a `Run` to `runs[job.Id]`;
  - trims to **per-job cap of 50**;
  - persists via `saveRunsLocked()`.
- `recordRun` is called from both `Controller.Run` (manual, `trigger="manual"`)
  and `Controller.fire` (scheduled, `trigger="scheduled"`).
- `Delete` also removes the job's run history from the ledger and persists, so
  deleted jobs leave no orphaned runs in `runs.json`.
- A **run-history save failure does not fail the run itself** (the run already
  happened); it is logged and the in-memory ledger keeps the record. This is the
  deliberate asymmetry with the jobs save path, where mutations roll back.
- Constructor becomes `NewController(store Store, runStore RunStore, curlPath
  string)`; all existing tests switch to a `memRunStore` test helper.

#### API

- `GET /api/v1/jobs/{id}/runs` → `200 {"runs":[Run...]}` newest-first, capped.
  `ErrNotFound` → 404 via `writeCtrlError`.
- **`last_run` summary** so badges render without per-job round trips. List and
  get handlers wrap the job:

  ```go
  type jobResponse struct {
      Job
      LastRun *RunSummary `json:"last_run,omitempty"` // nil when no history
  }
  type RunSummary struct {
      Status   string    `json:"status"`
      ExitCode int       `json:"exit_code"`
      Time     time.Time `json:"time"`
  }
  ```

  `handleList` → `[]jobResponse`; `handleGet` → `jobResponse`. The CLI decodes
  these as `[]Job`/`Job` — Go's `json.Unmarshal` ignores unknown fields, so CLI
  compatibility is preserved. `handleCreate`/`handleUpdate` keep returning the
  raw `Job` (a brand-new/updated job has no history yet).
- No changes to `run`, create, update, delete response contracts.

#### main.go wiring

```go
runStorePath := envOr("CRONIX_RUNS_PATH", defaultRunsPath)
ctrl, err := NewController(NewFileStore(storePath), NewFileRunStore(runStorePath), curlPath)
```

### B. Frontend — React app

#### Scaffold

- New `ui/` directory: `package.json`, `vite.config.js`, `index.html`,
  `src/` (React + plain CSS). Vite `build.outDir` = `../web`, `emptyOutDir:
  true` → built `index.html` + `assets/` land in the embedded directory.
- `spa.go`/`go:embed` unchanged — the embed picks up the built output.
- Dev flow: `npm run dev` with a Vite proxy (`/api` → `http://localhost:8080`)
  for HMR against a running server.
- **Committed `web/`**: built assets are checked in so `go build`/`go test`
  work anywhere without Node. `make ui` regenerates them. Node is only needed to
  rebuild the UI, never for Go builds. (Decision: user approved committed web/.)

#### Views (feature parity + extras)

- **Login gate**: token prompt; token kept in `sessionStorage` (same behavior as
  today), sent as `Authorization: Bearer <token>`.
- **Job list**: table with ID, name, enabled toggle, schedule, last-run badge
  (`ok`/`failed`/`error` colored dot + exit + relative time, blank when no
  history), and row actions (edit, delete, run).
- **Add/edit form**: name, schedule, curl, retries, retry delay, enabled —
  mirroring the current fields; inline validation errors from the API.
- **Run-now**: POST `/api/v1/jobs/{id}/run`; renders the unified result block
  (attempts + output) from the `steps` response — same info as the CLI block.
- **History panel**: fetch `GET /api/v1/jobs/{id}/runs`, list runs (time,
  trigger, status, exit, output) newest-first.
- **Filter/search**: text input narrowing the table by name/id/schedule.
- **Theme toggle**: CSS custom properties + `data-theme` attribute on `<html>`
  (light/dark), persisted in `localStorage`.
- Plain CSS: hand-written, no Tailwind; tokens (colors, spacing) via CSS
  variables so the theme toggle works by swapping variable values.

### C. Build plumbing

- **Dockerfile**: insert a `node:22-alpine` build stage:
  `COPY ui/package*.json ./ui/` → `npm ci --prefix ui` → `COPY ui ./ui` →
  `npm run build --prefix ui`. In the Go stage, `COPY --from=web /build/ui/out
  ./web` **after** `COPY . .` so built assets override any stale committed ones.
- **`.dockerignore`**: add `ui/node_modules`.
- **Makefile**: `ui` target (`npm --prefix ui install && npm run build --prefix
  ui`) and note in `docker-run` that the image build now compiles the UI.
- **README**: document `make ui`, `npm run dev`, `CRONIX_RUNS_PATH`, and the new
  runs endpoint.

## Testing

- Backend (pure Go, fully testable): `Run` JSON shape, `FileRunStore` round-trip
  + missing-file, cap/trim logic, `recordRun` status derivation, runs endpoint,
  `last_run` summary in list/get, CLI still decodes list/get. Host note: use
  `/usr/bin/true|false` (no `/bin/true` on this host).
- Frontend: `tsc --noEmit` (or `npm run build`) + `npm run build` must succeed;
  unit tests kept minimal (vitest optional — at minimum a build gate). Gate for
  the repo stays: `go build ./... && go vet ./... && go test ./...` green,
  `gofmt -l .` clean, and `npm --prefix ui run build` green.
- Docker live E2E at the end (add/list/scheduled-fire/run/history/restart
  persistence/delete) like Task 8 of v2.

## Out of scope

- No jobs.json format change (separate runs.json keeps deployed volumes valid).
- No run-history browsing beyond one job's capped 50 (no global history feed,
  no pagination).
- No auto-refresh polling; badges refresh on manual reload/refetch.
- No structured JSON logging; `go.mod` unchanged (Go 1.24, no new Go deps;
  Node deps are Vite/React build-time only, not shipped in the runtime image).
