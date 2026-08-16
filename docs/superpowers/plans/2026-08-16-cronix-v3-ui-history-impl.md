# Cronix v3 — React UI + Persistent Run History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the vanilla SPA with a React (Vite, plain CSS) app — feature parity plus last-run badges, filter/search, theme toggle, and a run-history panel — and persist run history to a separate `runs.json` (per-job cap 50).

**Architecture:** A new `Run` record + `RunStore`/`FileRunStore` (atomic temp+rename via a shared `atomicWriteFile` helper extracted from `FileStore.Save`). The `Controller` gains a `runs map[string][]Run` ledger (newest-first, capped at 50/job) recorded from both manual runs and scheduled fires, persisted best-effort (save failures never fail the run), and pruned on job delete. The API adds `GET /api/v1/jobs/{id}/runs` and a `last_run` summary inline in list/get via a `jobResponse` wrapper (CLI unaffected — `json.Unmarshal` ignores unknown fields). A new `ui/` Vite React app builds into the existing `web/` embed directory, so `go:embed`/`SPAHandler` stay untouched and one container delivers everything.

**Tech Stack:** Go 1.24 (no new Go deps); React 18 + Vite 5 + vitest 2 (build-time only, under `ui/`); Node 22 in the Docker build stage; committed built `web/` assets so Go builds need no Node.

## Global Constraints

- Do NOT change `go.mod`, the `Job`/`Result` JSON tags, or the `run` response contract (`200 {steps:[...]}`).
- `jobs.json` format is UNCHANGED; run history lives in a separate `runs.json` (`CRONIX_RUNS_PATH`, default `/data/runs.json`).
- Run-history save failures log and keep the in-memory record (the run already happened) — the run must NOT fail.
- Per-job history cap is 50 (`runHistoryCap`); runs stored newest-first.
- `Run` JSON keys are `job_id`, `trigger`, `time`, `status`, `exit_code`, `results`; `status` ∈ `ok|failed|error`; `trigger` ∈ `manual|scheduled`.
- `go:embed web` must keep compiling: `web/` always contains a valid build (committed locally; produced by the web build stage in Docker).
- Host note: `/bin/true` does NOT exist — use `/usr/bin/true`, `/usr/bin/false`, `/usr/bin/env`, `/bin/sleep`, `/nonexistent/curl`.
- Gate before EVERY commit: `go build ./... && go vet ./... && go test ./...` green, `gofmt -l .` clean, and (from Task 5 on) `npm --prefix ui run build` green.
- Tags for tasks: use conventional commits (`feat:` / `test:` / `docs:`), one commit per task, matching repo history style.

---

### Task 1: `Run` record, `RunStore`, `FileRunStore`, shared `atomicWriteFile`

**Files:**
- Create: `run_store.go`, `run_store_test.go`
- Modify: `store.go` (extract `atomicWriteFile`, reuse it in `FileStore.Save`)

**Interfaces:**
- Consumes: `Result` (runner.go), `FileStore`'s atomic-write body (store.go:48-74).
- Produces:
  - `Run` struct (see JSON keys above, `Time time.Time` UTC, `Results []Result`)
  - `RunStore` interface: `LoadRuns() ([]Run, error)`, `SaveRuns([]Run) error`
  - `FileRunStore{Path string}` + `NewFileRunStore(path string) *FileRunStore`
  - `func atomicWriteFile(pattern, path string, data []byte) error` (temp file + chmod + rename)
  - Task 2 `recordRun` and Task 3 `jobResponse`/`LastRun` rely on these.

- [ ] **Step 1: Write the failing tests**

Create `run_store_test.go`:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunJSONKeys(t *testing.T) {
	r := Run{
		JobId: "a", Trigger: "manual", Time: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
		Status: "ok", ExitCode: 0,
		Results: []Result{{Attempt: 1, Total: 1, ExitCode: 0, Output: "hi"}},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"job_id", "trigger", "time", "status", "exit_code", "results"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing JSON key %q in: %s", k, b)
		}
	}
}

func TestFileRunStoreLoadMissingReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.json")
	got, err := NewFileRunStore(path).LoadRuns()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no runs, got %d", len(got))
	}
}

func TestFileRunStoreSaveThenLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.json")
	fs := NewFileRunStore(path)
	want := []Run{
		{JobId: "a", Trigger: "manual", Time: time.Now().UTC(), Status: "ok", ExitCode: 0},
		{JobId: "b", Trigger: "scheduled", Time: time.Now().UTC().Add(-time.Minute), Status: "failed", ExitCode: 6,
			Results: []Result{{Attempt: 1, Total: 1, ExitCode: 6, Output: "x"}}},
	}
	if err := fs.SaveRuns(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := fs.LoadRuns()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 runs, got %d", len(got))
	}
	if got[0].JobId != "a" || got[1].Status != "failed" || len(got[1].Results) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestFileRunStoreSaveCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "runs.json")
	if err := NewFileRunStore(path).SaveRuns([]Run{{JobId: "a", Status: "ok"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist after save: %v", err)
	}
}

func TestFileRunStoreLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewFileRunStore(path).LoadRuns()
	if err == nil {
		t.Fatal("want error for corrupt store file")
	}
	if !strings.Contains(err.Error(), "runs.json") {
		t.Errorf("error should name the store path: %v", err)
	}
}

func TestFileRunStoreSaveLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.json")
	if err := NewFileRunStore(path).SaveRuns([]Run{{JobId: "a", Status: "ok"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestRunJSONKeys|TestFileRunStore' ./...`
Expected: FAIL — `Run`/`RunStore`/`FileRunStore` undefined (compile error).

- [ ] **Step 3: Implement**

Create `run_store.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Run struct {
	JobId    string    `json:"job_id"`
	Trigger  string    `json:"trigger"`
	Time     time.Time `json:"time"`
	Status   string    `json:"status"`
	ExitCode int       `json:"exit_code"`
	Results  []Result  `json:"results"`
}

type RunStore interface {
	LoadRuns() ([]Run, error)
	SaveRuns([]Run) error
}

type FileRunStore struct {
	Path string
}

func NewFileRunStore(path string) *FileRunStore {
	return &FileRunStore{Path: path}
}

func (f *FileRunStore) LoadRuns() ([]Run, error) {
	data, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []Run
	if err := json.Unmarshal(data, &runs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.Path, err)
	}
	return runs, nil
}

func (f *FileRunStore) SaveRuns(runs []Run) error {
	data, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile("runs-*.tmp", f.Path, data)
}
```

In `store.go`, extract the atomic-write body of `FileStore.Save` (lines 48-74) into a shared helper and call it from `Save`:

```go
func (f *FileStore) Save(jobs []Job) error {
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile("jobs-*.tmp", f.Path, data)
}

func atomicWriteFile(pattern, path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
```

Note the renamed parameter: inside `atomicWriteFile` the final rename target is the `path` argument, NOT `f.Path` (there is no `f` receiver here). The code above is the full corrected body — do not copy `f.Path` verbatim from the original `Save`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run 'TestRunJSONKeys|TestFileRunStore|TestFileStore' ./...`
Expected: PASS — new run-store tests plus the existing `TestFileStore*` suite (they now exercise the shared helper).

- [ ] **Step 5: Commit**

```bash
git add run_store.go run_store_test.go store.go
git commit -m "feat: add run store with atomic file persistence"
```

---

### Task 2: Controller run ledger (`recordRun`, `History`, `LastRun`, delete pruning)

**Files:**
- Modify: `controller.go` (field, load, `Run`, `fire`, `Delete`, new methods)
- Test: `controller_test.go` (update helper + all direct `NewController` calls; add ledger tests)

**Interfaces:**
- Consumes: `Run`, `RunStore` (Task 1), `ErrCommandFailed` (runner.go), `Result`.
- Produces:
  - `NewController(store Store, runStore RunStore, curlPath string) (*Controller, error)` — **SIGNATURE CHANGE**
  - `(c *Controller) History(id string) ([]Run, error)` — `ErrNotFound` if the job is missing
  - `(c *Controller) LastRun(id string) *RunSummary` — nil when no history yet
  - `const runHistoryCap = 50`
  - Task 3 uses `History` (runs endpoint) and `LastRun` (list/get summaries).

- [ ] **Step 1: Update the constructor call sites first (compile fix)**

The new `NewController` takes a third `RunStore` argument. Update every call site. In `controller_test.go`, add this helper near `memStore` and replace `newTestController`'s body:

```go
type memRunStore struct {
	runs     []Run
	loadErr  error
	saveErr  error
	numSaves int
}

func (m *memRunStore) LoadRuns() ([]Run, error) { return m.runs, m.loadErr }
func (m *memRunStore) SaveRuns(r []Run) error {
	m.runs = r
	m.numSaves++
	if m.saveErr != nil {
		return m.saveErr
	}
	return nil
}
```

Update `newTestController`:

```go
func newTestController(t *testing.T, store Store) *Controller {
	t.Helper()
	c, err := NewController(store, &memRunStore{}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}
```

Update these remaining direct `NewController(_, _)` calls in `controller_test.go` by inserting `&memRunStore{},` as the middle argument (`/usr/bin/env` / `/bin/sleep` remain the third arg):
- `TestNewControllerWithBadStoredJobFails` (line ~55)
- `TestScheduledFireUsesLiveCurlAfterUpdate` (line ~286) → `NewController(&memStore{}, &memRunStore{}, "/usr/bin/env")`
- `TestConcurrentRunsDoNotInterleave` (line ~315) → `NewController(&memStore{...}, &memRunStore{}, "/bin/sleep")`

In `api_test.go`, update `newTestServer` and every direct `NewController` call (lines ~17, 152, 175, 202, 220, 242) with `&memRunStore{},` as the middle argument.

In `cli_test.go`, update the four direct calls (lines ~19, 49, 93, 114) similarly.

In `spa_test.go`, update `spaServer` (line ~13) similarly.

Run: `go build ./...`
Expected: passes after all call sites updated (tests still compile with the old behavior).

- [ ] **Step 2: Write the failing ledger tests**

Add to `controller_test.go`:

```go
func TestRunRecordsManualHistory(t *testing.T) {
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	if _, err := c.Run("a"); err != nil {
		t.Fatalf("run: %v", err)
	}
	runs, err := c.History("a")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	if runs[0].Trigger != "manual" || runs[0].Status != "ok" || runs[0].ExitCode != 0 {
		t.Errorf("run record wrong: %+v", runs[0])
	}
	ls := c.LastRun("a")
	if ls == nil || ls.Status != "ok" {
		t.Errorf("last run summary wrong: %+v", ls)
	}
}

func TestRunRecordsScheduledHistory(t *testing.T) {
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	c.fire("a")
	runs, err := c.History("a")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(runs) != 1 || runs[0].Trigger != "scheduled" {
		t.Errorf("scheduled fire should record trigger=scheduled: %+v", runs)
	}
}

func TestRecordRunStatusDerivation(t *testing.T) {
	c := newTestController(t, &memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}})
	c.recordRun(Job{Id: "a"}, "manual", []Result{{Attempt: 1, Total: 1, ExitCode: 0}}, nil)
	c.recordRun(Job{Id: "a"}, "manual", []Result{{Attempt: 1, Total: 1, ExitCode: 6}},
		fmt.Errorf("%w: job a failed", ErrCommandFailed))
	c.recordRun(Job{Id: "a"}, "manual", nil, errors.New("cannot start /bin/true"))
	runs, _ := c.History("a")
	if len(runs) != 3 {
		t.Fatalf("want 3 runs, got %d", len(runs))
	}
	if runs[0].Status != "error" || runs[1].Status != "failed" || runs[2].Status != "ok" {
		t.Errorf("status order wrong (newest-first): %+v", runs)
	}
	if runs[1].ExitCode != 6 {
		t.Errorf("failed run should carry last exit: %+v", runs[1])
	}
}

func TestRecordRunTrimsToCap(t *testing.T) {
	c := newTestController(t, &memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}})
	for i := 0; i < runHistoryCap+10; i++ {
		c.recordRun(Job{Id: "a"}, "manual", []Result{{Attempt: 1, Total: 1, ExitCode: 0}}, nil)
	}
	runs, _ := c.History("a")
	if len(runs) != runHistoryCap {
		t.Fatalf("want %d runs, got %d", runHistoryCap, len(runs))
	}
}

func TestRecordRunSaveFailureDoesNotFail(t *testing.T) {
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{saveErr: errors.New("disk full")}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	c.recordRun(Job{Id: "a"}, "manual", []Result{{Attempt: 1, Total: 1, ExitCode: 0}}, nil)
	runs, err := c.History("a")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatal("run must stay in the in-memory ledger even when persistence fails")
	}
}

func TestHistoryMissingJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.History("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestLastRunNilWhenEmpty(t *testing.T) {
	c := newTestController(t, &memStore{})
	if ls := c.LastRun("nope"); ls != nil {
		t.Errorf("want nil LastRun, got %+v", ls)
	}
}

func TestDeletePrunesHistory(t *testing.T) {
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	if _, err := c.Run("a"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := c.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ls := c.LastRun("a"); ls != nil {
		t.Errorf("deleted job must not retain last run: %+v", ls)
	}
}

func TestNewControllerLoadsAndPrunesRunHistory(t *testing.T) {
	store := &memRunStore{runs: []Run{
		{JobId: "a", Trigger: "manual", Time: time.Now().UTC(), Status: "ok", ExitCode: 0},
		{JobId: "ghost", Trigger: "manual", Time: time.Now().UTC(), Status: "ok", ExitCode: 0},
	}}
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, store, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	runs, _ := c.History("a")
	if len(runs) != 1 {
		t.Fatalf("want loaded run for job a, got %d", len(runs))
	}
	if ls := c.LastRun("ghost"); ls != nil {
		t.Error("runs for unknown jobs must be pruned at load")
	}
}
```

Add `"time"` to the `controller_test.go` imports (currently `bytes`, `errors`, `fmt`, `log`, `os`, `strings`, `sync`, `testing`, `time` — `time` is already imported).

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test -run 'TestRunRecords|TestRecordRun|TestHistory|TestLastRun|TestDeletePrunes|TestNewControllerLoadsAndPrunes' ./...`
Expected: FAIL — `History`/`recordRun`/`LastRun` undefined and `runs` field missing.

- [ ] **Step 4: Implement**

In `controller.go`:

Add the const and `runs` field, and thread `runStore` through the constructor:

```go
const runHistoryCap = 50

type Controller struct {
	mu       sync.Mutex
	runMu    sync.Mutex
	store    Store
	runStore RunStore
	cron     *cron.Cron
	jobs     map[string]Job
	order    []string
	entries  map[string]cron.EntryID
	runs     map[string][]Run
	curlPath string
}

func NewController(store Store, runStore RunStore, curlPath string) (*Controller, error) {
	c := &Controller{
		store:    store,
		runStore: runStore,
		cron:     cron.New(),
		jobs:     map[string]Job{},
		entries:  map[string]cron.EntryID{},
		runs:     map[string][]Run{},
		curlPath: curlPath,
	}
	stored, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("load store: %w", err)
	}
	for _, job := range stored {
		if err := c.validate(job); err != nil {
			return nil, fmt.Errorf("job %q: %w", job.Id, err)
		}
		c.jobs[job.Id] = job
		c.order = append(c.order, job.Id)
		if err := c.register(job); err != nil {
			return nil, fmt.Errorf("job %q: %w", job.Id, err)
		}
	}
	loadedRuns, err := runStore.LoadRuns()
	if err != nil {
		return nil, fmt.Errorf("load run store: %w", err)
	}
	for _, r := range loadedRuns {
		if _, ok := c.jobs[r.JobId]; !ok {
			continue // prune orphaned runs for jobs that no longer exist
		}
		c.runs[r.JobId] = append(c.runs[r.JobId], r)
	}
	return c, nil
}
```

Add the ledger methods:

```go
func (c *Controller) recordRun(job Job, trigger string, results []Result, runErr error) {
	status := "ok"
	switch {
	case runErr == nil:
	case errors.Is(runErr, ErrCommandFailed):
		status = "failed"
	default:
		status = "error"
	}
	exit := 0
	if n := len(results); n > 0 {
		exit = results[n-1].ExitCode
	}
	run := Run{
		JobId:    job.Id,
		Trigger:  trigger,
		Time:     time.Now().UTC(),
		Status:   status,
		ExitCode: exit,
		Results:  results,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runs[job.Id] = append([]Run{run}, c.runs[job.Id]...)
	if len(c.runs[job.Id]) > runHistoryCap {
		c.runs[job.Id] = c.runs[job.Id][:runHistoryCap]
	}
	if err := c.runStore.SaveRuns(c.runsSnapshot()); err != nil {
		log.Printf("run history save: %v", err)
	}
}

func (c *Controller) History(id string) ([]Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.jobs[id]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	out := make([]Run, len(c.runs[id]))
	copy(out, c.runs[id])
	return out, nil
}

func (c *Controller) LastRun(id string) *RunSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	runs := c.runs[id]
	if len(runs) == 0 {
		return nil
	}
	first := runs[0]
	return &RunSummary{Status: first.Status, ExitCode: first.ExitCode, Time: first.Time}
}

func (c *Controller) runsSnapshot() []Run {
	out := make([]Run, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.runs[id]...)
	}
	return out
}
```

Record runs from both execution paths. `Controller.Run` (lines 228-236):

```go
func (c *Controller) Run(id string) ([]Result, error) {
	job, ok := c.jobByID(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	c.runMu.Lock()
	defer c.runMu.Unlock()
	results, err := Run(job, c.curlPath)
	c.recordRun(job, "manual", results, err)
	return results, err
}
```

`fire` (lines 311-327): add the `recordRun` call right after `Run` returns, before the log branches:

```go
	c.runMu.Lock()
	defer c.runMu.Unlock()
	results, err := Run(job, c.curlPath)
	c.recordRun(job, "scheduled", results, err)
	if err != nil {
```

`Delete`: after the jobs `saveLocked()` succeeds, prune the run history:

```go
	if err := c.saveLocked(); err != nil {
		// roll back: restore the job, its list position, and registration
		c.jobs[id] = cur
		c.order = append(c.order[:pos], append([]string{id}, c.order[pos:]...)...)
		if err := c.register(cur); err != nil {
			log.Printf("rollback: re-register %s: %v", id, err)
		}
		return err
	}
	delete(c.runs, id)
	if err := c.runStore.SaveRuns(c.runsSnapshot()); err != nil {
		log.Printf("run history save: %v", err)
	}
	log.Printf("job=%s deleted", id)
```

Add the `RunSummary` type to `run_store.go`:

```go
type RunSummary struct {
	Status   string    `json:"status"`
	ExitCode int       `json:"exit_code"`
	Time     time.Time `json:"time"`
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS — new ledger tests plus the migrated existing suite (contracts unchanged).

- [ ] **Step 6: Commit**

```bash
git add controller.go controller_test.go run_store.go api_test.go cli_test.go spa_test.go
git commit -m "feat: persist run history ledger in controller"
```

---

### Task 3: API — runs endpoint + `last_run` in list/get

**Files:**
- Modify: `api.go` (route, `handleRuns`, `jobResponse`, list/get)
- Test: `api_test.go`

**Interfaces:**
- Consumes: `Controller.History(id) ([]Run, error)`, `Controller.LastRun(id) *RunSummary` (Task 2).
- Produces: `GET /api/v1/jobs/{id}/runs` → `200 {"runs":[Run...]}`; list/get now return `jobResponse` with optional `last_run`. CLI unaffected (extra fields ignored).

- [ ] **Step 1: Write the failing tests**

Add to `api_test.go`:

```go
func TestAPIRunsEndpoint(t *testing.T) {
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, "secret")
	w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("run: %d: %s", w.Code, b)
	}
	w, b = req(t, h, "GET", "/api/v1/jobs/a/runs", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("runs: want 200, got %d: %s", w.Code, b)
	}
	var resp struct {
		Runs []Run `json:"runs"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Runs) != 1 || resp.Runs[0].Status != "ok" || resp.Runs[0].Trigger != "manual" {
		t.Errorf("runs payload wrong: %+v", resp.Runs)
	}
}

func TestAPIRunsMissingJobReturns404(t *testing.T) {
	h := newTestServer(t, &memStore{})
	w, _ := req(t, h, "GET", "/api/v1/jobs/nope/runs", "secret", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestAPIListIncludesLastRunSummary(t *testing.T) {
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true},
		{Id: "b", Schedule: "* * * * *", Curl: "curl true", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, "secret")
	if w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil); w.Code != http.StatusOK {
		t.Fatalf("run: %d: %s", w.Code, b)
	}
	w, b := req(t, h, "GET", "/api/v1/jobs", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var jobs []jobResponse
	if err := json.Unmarshal(b, &jobs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	var withLast, withoutLast *jobResponse
	for i := range jobs {
		if jobs[i].Id == "a" {
			withLast = &jobs[i]
		} else {
			withoutLast = &jobs[i]
		}
	}
	if withLast == nil || withLast.LastRun == nil || withLast.LastRun.Status != "ok" {
		t.Errorf("job a should carry a last_run summary: %+v", withLast)
	}
	if withoutLast == nil || withoutLast.LastRun != nil {
		t.Errorf("job b should have no last_run yet: %+v", withoutLast)
	}
}

func TestAPIGetIncludesLastRunSummary(t *testing.T) {
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, "secret")
	w, b := req(t, h, "GET", "/api/v1/jobs/a", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d", w.Code)
	}
	var j jobResponse
	if err := json.Unmarshal(b, &j); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if j.Id != "a" || j.LastRun != nil {
		t.Errorf("get without history: %+v", j)
	}
}

func TestCLIListStillDecodesJobResponse(t *testing.T) {
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Name: "ping", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	addr := withCLIServer(t, NewServer(c, "tok"))
	out := new(bytes.Buffer)
	if code := runCLIIn([]string{"list", "--addr", addr, "--token", "tok"}, out); code != 0 {
		t.Fatalf("list: exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "ping") {
		t.Errorf("list output broken by last_run field: %s", out.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestAPIRuns|TestAPIListIncludesLastRun|TestAPIGetIncludesLastRun|TestCLIListStillDecodes' ./...`
Expected: FAIL — `handleRuns` route/404 and `last_run` absent.

- [ ] **Step 3: Implement**

In `api.go`, register the route in `NewServer` (next to the `run` route):

```go
	mux.HandleFunc("POST /api/v1/jobs/{id}/run", s.auth(s.handleRun))
	mux.HandleFunc("GET /api/v1/jobs/{id}/runs", s.auth(s.handleRuns))
```

Add the handler:

```go
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.ctrl.History(r.PathValue("id"))
	if err != nil {
		writeCtrlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Runs []Run `json:"runs"`
	}{Runs: runs})
}
```

Replace `handleList` and `handleGet` with the `jobResponse` wrappers:

```go
type jobResponse struct {
	Job
	LastRun *RunSummary `json:"last_run,omitempty"`
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	jobs := s.ctrl.List()
	out := make([]jobResponse, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobResponse{Job: j, LastRun: s.ctrl.LastRun(j.Id)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := s.ctrl.Get(id)
	if err != nil {
		writeCtrlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobResponse{Job: job, LastRun: s.ctrl.LastRun(id)})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS — new API tests, existing API/CLI tests (including `TestAPIGetMissingReturns404`, `TestRunCLI...` which decode the enriched payloads), and everything else.

- [ ] **Step 5: Commit**

```bash
git add api.go api_test.go
git commit -m "feat: expose run history endpoint and last-run summaries"
```

---

### Task 4: `main.go` wiring for the run store

**Files:**
- Modify: `main.go`

**Interfaces:**
- Consumes: `NewFileRunStore`, `defaultRunsPath`.
- Produces: `CRONIX_RUNS_PATH` env var honored; server starts with run persistence.

- [ ] **Step 1: Write the failing test (none — wiring verified by build + container)**

No unit test here; the behavior is verified by Task 8's Docker E2E and the existing suite. Implement and verify by build.

- [ ] **Step 2: Implement**

In `main.go`, add the const:

```go
const (
	defaultStorePath = "/data/jobs.json"
	defaultRunsPath  = "/data/runs.json"
	defaultHTTPAddr  = ":8080"
	defaultCurlPath  = "/usr/local/bin/curl"
)
```

And wire it in `main()`:

```go
	storePath := envOr("CRONIX_STORE_PATH", defaultStorePath)
	runStorePath := envOr("CRONIX_RUNS_PATH", defaultRunsPath)
	httpAddr := envOr("CRONIX_HTTP_ADDR", defaultHTTPAddr)
	curlPath := envOr("CURL_PATH", defaultCurlPath)

	ctrl, err := NewController(NewFileStore(storePath), NewFileRunStore(runStorePath), curlPath)
	if err != nil {
		log.Fatalf("startup error: %v", err)
	}
```

- [ ] **Step 3: Verify**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: green.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat: persist run history to runs.json via CRONIX_RUNS_PATH"
```

---

### Task 5: React scaffold — `ui/` Vite app building into `web/`

**Files:**
- Create: `ui/package.json`, `ui/package-lock.json` (via `npm install`), `ui/vite.config.js`, `ui/index.html`, `ui/src/main.jsx`, `ui/src/api.js`, `ui/src/style.css`, `ui/src/App.jsx` (minimal shell), `ui/.gitignore` (`node_modules`)
- Modify: `spa_test.go` (React index assertions)
- Replace: built assets under `web/` (delete stale `app.js`/`style.css`, add built `index.html` + `assets/`)

**Interfaces:**
- Consumes: API contract as of Task 3 (`/api/v1/jobs` + `last_run`, `/api/v1/jobs/{id}/runs`).
- Produces: a buildable Vite React app; `web/` becomes generated output (delete stale files, `git add -A web/`).

- [ ] **Step 1: Write the failing SPA tests**

Replace `TestSpaServesAppJS` in `spa_test.go` (the vanilla `app.js` is going away) so it asserts the React bundle is embedded:

```go
func TestSpaServesReactIndex(t *testing.T) {
	h := spaServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	b, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(b), `<div id="root">`) {
		t.Error("built index should contain the React mount node")
	}
	if !strings.Contains(string(b), "/assets/") {
		t.Error("built index should reference Vite asset bundles")
	}
}
```

Run: `go test -run 'TestSpaServesReactIndex' ./...`
Expected: FAIL — the current vanilla `index.html` has no `<div id="root">` and no `/assets/`.

- [ ] **Step 2: Scaffold the app**

Create `ui/package.json`:

```json
{
  "name": "cronix-ui",
  "private": true,
  "version": "1.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "preview": "vite preview",
    "test": "vitest run"
  },
  "dependencies": {
    "react": "^18.3.1",
    "react-dom": "^18.3.1"
  },
  "devDependencies": {
    "@vitejs/plugin-react": "^4.3.4",
    "vite": "^5.4.11",
    "vitest": "^2.1.8"
  }
}
```

Create `ui/vite.config.js`:

```js
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../web',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8080',
    },
  },
  test: {
    environment: 'node',
  },
})
```

Create `ui/index.html`:

```html
<!doctype html>
<html lang="en" data-theme="light">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Cronix</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.jsx"></script>
  </body>
</html>
```

Create `ui/src/main.jsx`:

```jsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App.jsx'
import './style.css'

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
```

Create `ui/src/api.js` (the API client; token in `sessionStorage`):

```js
const TOKEN_KEY = 'cronix_token'

export function getToken() {
  return sessionStorage.getItem(TOKEN_KEY)
}

export function setToken(token) {
  if (token) {
    sessionStorage.setItem(TOKEN_KEY, token)
  } else {
    sessionStorage.removeItem(TOKEN_KEY)
  }
}

async function request(method, path, body) {
  const headers = { Authorization: `Bearer ${getToken() ?? ''}` }
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }
  const resp = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (resp.status === 401) {
    throw new Error('unauthorized')
  }
  if (resp.status === 204) {
    return null
  }
  const data = await resp.json().catch(() => null)
  if (!resp.ok) {
    throw new Error((data && data.error) || `request failed (${resp.status})`)
  }
  return data
}

export const listJobs = () => request('GET', '/api/v1/jobs')
export const getJob = (id) => request('GET', `/api/v1/jobs/${id}`)
export const createJob = (job) => request('POST', '/api/v1/jobs', job)
export const updateJob = (id, job) => request('PUT', `/api/v1/jobs/${id}`, job)
export const deleteJob = (id) => request('DELETE', `/api/v1/jobs/${id}`)
export const runJob = (id) => request('POST', `/api/v1/jobs/${id}/run`)
export const listRuns = (id) => request('GET', `/api/v1/jobs/${id}/runs`)
```

Create `ui/src/style.css` (CSS variables drive the theme toggle):

```css
:root {
  --bg: #f7f8fa;
  --fg: #1f2328;
  --muted: #656d76;
  --card: #ffffff;
  --border: #d0d7de;
  --accent: #0969da;
  --ok: #1a7f37;
  --fail: #cf222e;
  --err: #9a6700;
  --code-bg: #f6f8fa;
}

[data-theme='dark'] {
  --bg: #0d1117;
  --fg: #e6edf3;
  --muted: #8b949e;
  --card: #161b22;
  --border: #30363d;
  --accent: #58a6ff;
  --ok: #3fb950;
  --fail: #f85149;
  --err: #d29922;
  --code-bg: #161b22;
}

* { box-sizing: border-box; }

body {
  margin: 0;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif;
  background: var(--bg);
  color: var(--fg);
}

button {
  background: var(--card);
  color: var(--fg);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 6px 12px;
  cursor: pointer;
}

button:hover { border-color: var(--accent); }

input {
  background: var(--card);
  color: var(--fg);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 6px 8px;
}

table { width: 100%; border-collapse: collapse; }
th, td { text-align: left; padding: 10px; border-bottom: 1px solid var(--border); vertical-align: top; }
th { color: var(--muted); font-size: 13px; }
pre { background: var(--code-bg); border: 1px solid var(--border); padding: 10px; border-radius: 6px; overflow-x: auto; font-size: 12px; }

.badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 10px;
  font-size: 12px;
  color: #fff;
}
.badge.ok { background: var(--ok); }
.badge.failed { background: var(--fail); }
.badge.error { background: var(--err); }
.badge.never { background: var(--muted); }

.error { color: var(--fail); }
.muted { color: var(--muted); }
```

Create `ui/src/App.jsx` (minimal shell — full views come in Task 6):

```jsx
import { useEffect, useState } from 'react'
import { getToken, setToken, listJobs } from './api'

export default function App() {
  const [token, setTokenState] = useState(getToken())
  const [tokenInput, setTokenInput] = useState('')
  const [jobs, setJobs] = useState([])
  const [error, setError] = useState('')
  const [theme, setTheme] = useState(() => localStorage.getItem('cronix_theme') || 'light')

  const authed = Boolean(token)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    localStorage.setItem('cronix_theme', theme)
  }, [theme])

  useEffect(() => {
    if (!authed) return
    listJobs()
      .then((j) => {
        setJobs(Array.isArray(j) ? j : [])
        setError('')
      })
      .catch((e) => setError(e.message))
  }, [authed])

  if (!authed) {
    return (
      <form
        className="card"
        onSubmit={(e) => {
          e.preventDefault()
          setToken(tokenInput.trim())
          setTokenState(tokenInput.trim())
        }}
      >
        <h1>Cronix</h1>
        <input
          type="password"
          value={tokenInput}
          placeholder="API token"
          onChange={(e) => setTokenInput(e.target.value)}
        />
        <button type="submit">Sign in</button>
      </form>
    )
  }

  return (
    <div className="app">
      <header>
        <h1>Cronix</h1>
        <button onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')}>
          {theme === 'light' ? 'Dark' : 'Light'}
        </button>
        <button
          onClick={() => {
            setToken('')
            setTokenState('')
          }}
        >
          Sign out
        </button>
      </header>
      {error && <p className="error">{error}</p>}
      <p>Jobs: {jobs.length}</p>
    </div>
  )
}
```

- [ ] **Step 3: Install, build, and commit web assets**

```bash
npm --prefix ui install
npm --prefix ui run build
```

Wait: `npm install` produces `ui/package-lock.json`; `npm run build` emits into `web/`. Check the output: `ls web/` should show `index.html` and `assets/` (no `app.js`, no `style.css`).

- [ ] **Step 4: Run the SPA test and build gate**

```bash
go test -run 'TestSpaServesReactIndex' ./...
npm --prefix ui run build
```

Expected: SPA test PASS; Vite build clean.

- [ ] **Step 5: Commit (including deletion of stale web assets)**

```bash
git add ui/ web/ spa_test.go
git rm -f web/app.js web/style.css 2>/dev/null || true
git commit -m "feat: add Vite React UI scaffold embedded into web"
```

---

### Task 6: Full React app — views, badges, filter, history, theme

**Files:**
- Modify: `ui/src/App.jsx` (full views)
- Create: `ui/src/lib.js`, `ui/src/lib.test.js`

**Interfaces:**
- Consumes: `api.js` client (Task 5), API contracts (list includes `last_run`, `/runs` endpoint), `run` → `{steps:[...]}`.
- Produces: complete UI; `lib.js` pure helpers (`filterJobs`, `relativeTime`) with vitest coverage.

- [ ] **Step 1: Write the failing vitest tests**

Create `ui/src/lib.test.js` (imports `./lib`, which does not exist yet):

```js
import { describe, expect, it } from 'vitest'
import { filterJobs, relativeTime } from './lib'

describe('relativeTime', () => {
  it('returns empty for missing input', () => {
    expect(relativeTime('')).toBe('')
    expect(relativeTime(null)).toBe('')
  })

  it('formats just now / minutes / hours / days', () => {
    const base = Date.now()
    expect(relativeTime(new Date(base - 5 * 1000).toISOString())).toBe('just now')
    expect(relativeTime(new Date(base - 90 * 1000).toISOString())).toBe('1m ago')
    expect(relativeTime(new Date(base - 2 * 3600 * 1000).toISOString())).toBe('2h ago')
    expect(relativeTime(new Date(base - 3 * 86400 * 1000).toISOString())).toBe('3d ago')
  })
})

describe('filterJobs', () => {
  const jobs = [
    { id: 'abc', name: 'ping', schedule: '*/5 * * * *' },
    { id: 'def', name: '', schedule: '0 9 * * 1-5' },
  ]

  it('returns everything when query is blank', () => {
    expect(filterJobs(jobs, '')).toEqual(jobs)
    expect(filterJobs(jobs, '   ')).toEqual(jobs)
  })

  it('matches by name, id, and schedule (case-insensitive)', () => {
    expect(filterJobs(jobs, 'ping')).toHaveLength(1)
    expect(filterJobs(jobs, 'ABC')).toHaveLength(1)
    expect(filterJobs(jobs, '9')).toHaveLength(1)
    expect(filterJobs(jobs, 'zzz')).toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm --prefix ui test`
Expected: FAIL — `./lib` cannot be resolved (vitest module-not-found).

- [ ] **Step 3: Implement the pure helpers**

Create `ui/src/lib.js`:

```js
export function relativeTime(iso) {
  if (!iso) return ''
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ''
  const secs = Math.max(0, Math.floor((Date.now() - then) / 1000))
  if (secs < 10) return 'just now'
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  const days = Math.floor(hrs / 24)
  return days === 1 ? 'yesterday' : `${days}d ago`
}

export function filterJobs(jobs, query) {
  const needle = (query || '').trim().toLowerCase()
  if (!needle) return jobs
  return jobs.filter((j) =>
    [j.id, j.name, j.schedule].some((v) => (v || '').toLowerCase().includes(needle)),
  )
}
```

- [ ] **Step 4: Run the vitest tests to verify they pass**

Run: `npm --prefix ui test`
Expected: PASS — both `describe` blocks green.

- [ ] **Step 5: Implement the full `App.jsx`**

Create `ui/src/App.jsx` (replaces the Task 5 shell):

```jsx
import { useMemo, useEffect, useState } from 'react'
import * as api from './api'
import { filterJobs, relativeTime } from './lib'

const emptyJob = {
  name: '',
  schedule: '*/5 * * * *',
  curl: '',
  retries: 0,
  retry_delay: 5,
  enabled: true,
}

function Badge({ lastRun }) {
  if (!lastRun) return <span className="badge never">never</span>
  const label = lastRun.status === 'failed'
    ? `failed ${lastRun.exit_code}`
    : lastRun.status
  return (
    <span
      className={`badge ${lastRun.status}`}
      title={lastRun.time ? `last run ${relativeTime(lastRun.time)}` : ''}
    >
      {label}
    </span>
  )
}

function Editor({ job, onSave, onCancel, error }) {
  const [form, setForm] = useState(job)
  const set = (key) => (e) => setForm({ ...form, [key]: e.target.value })
  const setNum = (key) => (e) => setForm({ ...form, [key]: Number(e.target.value) })
  return (
    <form
      className="card"
      onSubmit={(e) => {
        e.preventDefault()
        onSave(form)
      }}
    >
      <h2>{job.id ? 'Edit job' : 'New job'}</h2>
      <label>
        Name
        <input value={form.name} placeholder="ping example" onChange={set('name')} />
      </label>
      <label>
        Schedule
        <input value={form.schedule} placeholder="*/5 * * * *" onChange={set('schedule')} />
      </label>
      <label>
        Curl command
        <input value={form.curl} placeholder="curl -s https://example.com" onChange={set('curl')} />
      </label>
      <label>
        Retries
        <input type="number" min="0" value={form.retries} onChange={setNum('retries')} />
      </label>
      <label>
        Retry delay (s)
        <input type="number" min="0" value={form.retry_delay} onChange={setNum('retry_delay')} />
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={form.enabled}
          onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
        />
        Enabled
      </label>
      {error && <p className="error">{error}</p>}
      <div className="row">
        <button type="submit">Save</button>
        <button type="button" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  )
}

function RunPanel({ name, steps, onClose }) {
  return (
    <div className="panel">
      <div className="row between">
        <h2>Run output — {name}</h2>
        <button onClick={onClose}>Close</button>
      </div>
      {steps.map((s, i) => (
        <pre key={i}>
attempt {s.attempt}/{s.total} exit={s.exit}
{s.output || '(no output)'}
        </pre>
      ))}
    </div>
  )
}

function HistoryPanel({ runs, onClose }) {
  return (
    <div className="panel">
      <div className="row between">
        <h2>Run history</h2>
        <button onClick={onClose}>Close</button>
      </div>
      {!runs || runs.length === 0 ? (
        <p className="muted">No runs yet.</p>
      ) : (
        runs.map((r, i) => (
          <pre key={i}>
{r.trigger} {r.time ? relativeTime(r.time) : ''} — {r.status} exit={r.exit_code}
{r.results.map((s) => `  attempt ${s.attempt}/${s.total} exit=${s.exit}${s.output ? `: ${s.output}` : ''}`).join('\n')}
          </pre>
        ))
      )}
    </div>
  )
}

export default function App() {
  const [token, setTokenState] = useState(api.getToken())
  const [tokenInput, setTokenInput] = useState('')
  const [jobs, setJobs] = useState([])
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [editing, setEditing] = useState(null) // null | {} new | job
  const [runPanel, setRunPanel] = useState(null) // {name, steps}
  const [history, setHistory] = useState(null) // {id, runs} or null
  const [theme, setTheme] = useState(() => localStorage.getItem('cronix_theme') || 'light')

  const authed = Boolean(token)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    localStorage.setItem('cronix_theme', theme)
  }, [theme])

  const refresh = useMemo(
    () => () =>
      api.listJobs()
        .then((j) => {
          setJobs(Array.isArray(j) ? j : [])
          setError('')
        })
        .catch((e) => setError(e.message)),
    [],
  )

  useEffect(() => {
    if (authed) refresh()
  }, [authed, refresh])

  if (!authed) {
    return (
      <form
        className="card"
        onSubmit={(e) => {
          e.preventDefault()
          api.setToken(tokenInput.trim())
          setTokenState(tokenInput.trim())
        }}
      >
        <h1>Cronix</h1>
        <input
          type="password"
          value={tokenInput}
          placeholder="API token"
          onChange={(e) => setTokenInput(e.target.value)}
        />
        <button type="submit">Sign in</button>
      </form>
    )
  }

  const shown = filterJobs(jobs, query)

  const save = (form) => {
    const body = { ...form }
    delete body.id
    const p = form.id
      ? api.updateJob(form.id, body)
      : api.createJob(body)
    p.then(() => {
      setEditing(null)
      setError('')
      refresh()
    }).catch((e) => setError(e.message))
  }

  const toggle = (j) =>
    api.updateJob(j.id, { ...j, enabled: !j.enabled })
      .then(refresh)
      .catch((e) => setError(e.message))

  const remove = (j) => {
    if (!window.confirm(`Delete job ${j.name || j.id}?`)) return
    api.deleteJob(j.id)
      .then(() => {
        setHistory(null)
        refresh()
      })
      .catch((e) => setError(e.message))
  }

  const runNow = (j) =>
    api.runJob(j.id)
      .then((r) => {
        setRunPanel({ name: j.name || j.id, steps: (r && r.steps) || [] })
        refresh()
      })
      .catch((e) => setError(e.message))

  const openHistory = (j) =>
    api.listRuns(j.id)
      .then((r) => setHistory({ id: j.id, runs: (r && r.runs) || [] }))
      .catch((e) => setError(e.message))

  return (
    <div className="app">
      <header>
        <h1>Cronix</h1>
        <input
          className="search"
          value={query}
          placeholder="Filter jobs..."
          onChange={(e) => setQuery(e.target.value)}
        />
        <button onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')}>
          {theme === 'light' ? 'Dark' : 'Light'}
        </button>
        <button onClick={() => setEditing({ ...emptyJob })}>New job</button>
        <button
          onClick={() => {
            api.setToken('')
            setTokenState('')
          }}
        >
          Sign out
        </button>
      </header>

      {error && <p className="error">{error}</p>}

      {editing && (
        <Editor
          key={editing.id || 'new'}
          job={editing}
          error={error}
          onSave={save}
          onCancel={() => setEditing(null)}
        />
      )}

      {!editing && (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Schedule</th>
              <th>Last run</th>
              <th>Enabled</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((j) => (
              <tr key={j.id}>
                <td>
                  <strong>{j.name || j.id}</strong>
                  <div className="muted">{j.id}</div>
                </td>
                <td>{j.schedule}</td>
                <td><Badge lastRun={j.last_run} /></td>
                <td>
                  <button
                    className={j.enabled ? 'toggle on' : 'toggle'}
                    onClick={() => toggle(j)}
                  >
                    {j.enabled ? 'on' : 'off'}
                  </button>
                </td>
                <td>
                  <button onClick={() => openHistory(j)}>history</button>
                  <button onClick={() => runNow(j)}>run</button>
                  <button onClick={() => setEditing({ ...j })}>edit</button>
                  <button onClick={() => remove(j)}>delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {history && history.id && (
        <HistoryPanel runs={history.runs} onClose={() => setHistory(null)} />
      )}

      {runPanel && (
        <RunPanel name={runPanel.name} steps={runPanel.steps} onClose={() => setRunPanel(null)} />
      )}
    </div>
  )
}
```

Appending to `ui/src/style.css`:

```css
.app { max-width: 960px; margin: 0 auto; padding: 16px; }
header { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; margin-bottom: 16px; }
header h1 { margin: 0 auto 0 0; font-size: 20px; }
.search { flex: 1; min-width: 180px; }
.card { display: flex; flex-direction: column; gap: 10px; max-width: 480px; margin: 24px auto; /* empty for overlay */ }
.card label { display: flex; flex-direction: column; gap: 4px; font-size: 13px; color: var(--muted); }
.card label.check { flex-direction: row; align-items: center; gap: 8px; }
.row { display: flex; gap: 8px; }
.row.between { justify-content: space-between; }
.panel { border: 1px solid var(--border); border-radius: 8px; padding: 12px 16px; margin-top: 16px; background: var(--card); }
.toggle.on { border-color: var(--ok); color: var(--ok); }
```

Add `.card` and login styling so the sign-in form (which uses `.card` alone at line ~155) does not look misplaced: `.card` max-width centers it. Good enough — no separate login class needed.

- [ ] **Step 6: Run tests and build**

```bash
npm --prefix ui test
npm --prefix ui run build
go test -run 'TestSpaServesReactIndex' ./...
```

Expected: `lib` vitest tests PASS; Vite build clean; SPA test green against the new bundle.

- [ ] **Step 7: Commit**

```bash
git add ui/
git commit -m "feat: build full React UI with badges, history, filter, and theme"
```

---

### Task 7: Docker build stage, Makefile, README

**Files:**
- Modify: `Dockerfile`, `.dockerignore`, `Makefile`, `README.md`

**Interfaces:**
- Consumes: `ui/` (Vite source) and the built `web/` output.
- Produces: container image where the Go build stage compiles the web assets into the binary.

- [ ] **Step 1: Dockerfile — add the web build stage**

Insert a `node` stage **before** the `golang` build stage and copy its output into the Go stage **after** `COPY . .`:

```dockerfile
# syntax=docker/dockerfile:1

FROM node:22-alpine AS web
WORKDIR /ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM golang:1.24-alpine AS build

ARG CURL_VERSION=8.21.0
ARG CURL_SHA256=d9b327997999045a24cda50f3983e69e51c516bd8be6ef9842fc7f99135e33bb

RUN apk add --no-cache curl gcc musl-dev make perl mbedtls-dev mbedtls-static zlib-dev zlib-static ca-certificates \
 && curl -fsSL "https://curl.se/download/curl-${CURL_VERSION}.tar.gz" -o /curl.tar.gz \
 && echo "${CURL_SHA256}  /curl.tar.gz" | sha256sum -c - \
 && mkdir /src \
 && tar xzf /curl.tar.gz -C /src --strip-components=1 \
 && cd /src \
 && ./configure --prefix=/out \
      --disable-shared --enable-static \
      --with-mbedtls --with-zlib \
      --disable-ldap --disable-ldaps \
      --without-libidn2 --without-librtmp --without-libpsl \
      --without-nghttp2 --without-brotli \
      --disable-docs --disable-manual \
 && make -j"$(nproc)" LDFLAGS="-all-static" \
 && make install \
 && cd / \
 && rm -rf /src /curl.tar.gz

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web ./web
RUN CGO_ENABLED=0 go build -o /cronix .

FROM scratch

COPY --from=build /out/bin/curl /usr/local/bin/curl
COPY --from=build /cronix /cronix
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENV CURL_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["/cronix"]
```

- [ ] **Step 2: `.dockerignore` — keep the build context lean**

Append `ui/node_modules`:

```
.git
docs
Dockerfile
.dockerignore
ui/node_modules
```

- [ ] **Step 3: Makefile — `ui` target**

Add to the `.PHONY` list and add the target:

```make
.PHONY: help build test vet lint run ui \
        docker-build docker-run docker-logs docker-stop \
        cli-list cli-add cli-run \
        clean
```

Add after `lint`:

```make
ui:
	npm --prefix ui ci
	npm --prefix ui run build
```

Add a `help` line:

```make
	@echo "  ui              build the React UI into web/ (npm ci + vite build)"
```

- [ ] **Step 4: README — document the UI and run store**

Append/update the README sections (match existing README style — check `README.md` first, then add documented lines for):
- `CRONIX_RUNS_PATH` env var (default `/data/runs.json`) — run history, capped at 50 runs/job.
- `GET /api/v1/jobs/{id}/runs` endpoint listing a job's run history newest-first.
- The `ui/` directory: `make ui` to rebuild the embedded UI, `npm --prefix ui run dev` for HMR with the Vite `/api` proxy.
- Built `web/` assets are committed so Go builds need no Node.

- [ ] **Step 5: Verify the full gate + image build**

```bash
go build ./... && go vet ./... && go test ./... -count=1
gofmt -l .
npm --prefix ui run build
docker build -t cronix .
```

Expected: Go gate green, `gofmt -l .` empty, Vite build clean, `docker build` succeeds with the node stage.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile .dockerignore Makefile README.md
git commit -m "feat: build React UI in the container and document v3"
```

---

### Task 8: Full-suite verification + Docker live E2E

**Files:** none.

- [ ] **Step 1: Full Go gate**

```bash
go build ./... && go vet ./... && go test ./... -count=1
gofmt -l .
npm --prefix ui run build
npm --prefix ui test
```

Expected: all green.

- [ ] **Step 2: Live E2E in the container**

```bash
make docker-build
make docker-run
sleep 2
make cli-add            # creates "ping" job (schedule */5, curl example.com)
make cli-list
# trigger a manual run and poll history
docker exec cronix-dev /cronix cli run --token devtoken $(docker exec cronix-dev /cronix cli list --token devtoken | awk 'NR==2{print $1}')
curl -s -H "Authorization: Bearer devtoken" http://127.0.0.1:7002/api/v1/jobs | head -c 600
curl -s -H "Authorization: Bearer devtoken" "http://127.0.0.1:7002/api/v1/jobs/$(docker exec cronix-dev /cronix cli list --token devtoken | awk 'NR==2{print $1}')/runs"
# scheduled fire: wait ~70s for */5 cron, then check runs.json grew
docker exec cronix-dev cat /data/runs.json
# restart persistence
make docker-stop
make docker-run
docker exec cronix-dev cat /data/jobs.json
# verify UI serves the React page
curl -s http://127.0.0.1:7002/ | grep -c 'root'
make docker-stop
```

Expected: add/list/run work; `/runs` returns history; a `*/5` scheduled job populates `runs.json` within ~70s and the records survive `docker-stop`/`docker-run` (volume persists `.data/`); `/` serves the React index.

- [ ] **Step 3: Update the SDD progress ledger**

Append v3 entries to `.superpowers/sdd/progress.md` mirroring the v2/v2.1 format (tasks 1–8, commits, gate results, E2E pass).

- [ ] **Step 4: Commit any stragglers**

If anything changed, commit it. Otherwise done.