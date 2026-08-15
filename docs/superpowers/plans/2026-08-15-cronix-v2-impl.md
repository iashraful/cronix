# Cronix v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade Cronix from env-var-only config to a live-managed scheduler: jobs live in a JSON file behind a `Store` interface, are mutated at runtime through a `robfig/cron`-reconciling controller, and are reachable three ways — a CLI (`/cronix cli ...`), a REST API (`/api/v1`), and an embedded single-page frontend — all in the same `scratch` container.

**Architecture:** A single binary has two modes selected by the first argument. `/cronix` runs server mode: loads the JSON store, registers enabled jobs with `robfig/cron`, serves the REST API + embedded SPA on one HTTP listener, and reconciles the cron registry live via a mutex-guarded controller. `/cronix cli <cmd>` is a thin HTTP client to the same `/api/v1` endpoints, so CLI, API, and SPA share one control path with a single JSON-file writer. Storage is behind a `Store` interface (file implementation now, SQLite swappable later). `CRON_*` job env vars are removed; `CRONIX_API_TOKEN` is required.

**Tech Stack:** Go 1.24 (`net/http` ServeMux with method+wildcard patterns, `go:embed`), `github.com/robfig/cron/v3`, `github.com/google/shlex`, `text/tabwriter` for CLI tables, `crypto/subtle` for token compare, vanilla HTML/JS frontend, Docker scratch runtime (unchanged static curl).

## Global Constraints

- Runtime image is `scratch`: no shell, no libc, no OS packages. No `sh -c` anywhere in the Go code.
- Mandatory auth: server refuses to start unless `CRONIX_API_TOKEN` is set (non-empty, non-whitespace). Every `/api/v1/*` request needs `Authorization: Bearer <token>`, compared with `crypto/subtle.ConstantTimeCompare`.
- Job `Id` optional at create; server generates a short hex id if omitted. When supplied it must be non-empty, unique, and match `^[A-Za-z0-9_-]{1,128}$`.
- Validation per job: `schedule` parses as standard 5-field cron; `curl` non-empty and `tokens[0]=="curl"` after shlex split; `retries`/`retry_delay` non-negative integers.
- Defaults: `retry_delay` = 5 seconds when not supplied, `enabled` = true when not supplied, `retries` = 0.
- Disabled jobs keep metadata in the list but are not registered with cron (never fire). Enabled toggle applies at update.
- Persistence ordering on mutation: apply to in-memory list → reconcile cron → `Store.Save()`; if `Save` fails, roll back in-memory state and return an error.
- Captured subprocess output capped at 4 KiB (last 4 KiB kept).
- Log format per run attempt: `job=<id> schedule="<expr>" attempt=<n>/<total> exit=<code> output=<tail>`.
- On `SIGTERM`/`SIGINT`: stop scheduling, wait up to 10s for in-flight jobs, exit 0.
- Env vars: `CRONIX_STORE_PATH` (default `/data/jobs.json`), `CRONIX_HTTP_ADDR` (default `:8080`), `CRONIX_API_TOKEN` (required), `CURL_PATH` (default `/usr/local/bin/curl`), `TZ`.
- Go version floor: 1.24.
- Test/vet gate before each commit: `go build ./... && go vet ./... && go test ./...`

---

### Task 1: Job model + Store interface + FileStore

**Files:**
- Create: `store.go`
- Test: `store_test.go`

**Interfaces:**
- Consumes: nothing (only `encoding/json`, `os`, `path/filepath`).
- Produces:
  - `type Job struct { Id, Name, Schedule, Curl string; Retries, RetryDelay int; Enabled bool }` with JSON tags `id`, `name`, `schedule`, `curl`, `retries`, `retry_delay`, `enabled`.
  - `type Store interface { Load() ([]Job, error); Save([]Job) error }`
  - `func NewFileStore(path string) *FileStore`
  - `FileStore` satisfies `Store`. `Load()` on a missing file returns `nil, nil` (no jobs). `Save()` writes atomically (temp file in same dir + `os.Rename`), creating parent dirs with `os.MkdirAll(path, 0o755)`.

- [ ] **Step 1: Write the failing tests**

Create `store_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStoreLoadMissingReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	s, err := NewFileStore(path).Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s) != 0 {
		t.Fatalf("want no jobs, got %d", len(s))
	}
}

func TestFileStoreSaveThenLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	fs := NewFileStore(path)
	want := []Job{
		{Id: "a", Name: "alpha", Schedule: "*/5 * * * *", Curl: "curl -s https://a", Retries: 1, RetryDelay: 7, Enabled: true},
		{Id: "b", Schedule: "0 9 * * 1-5", Curl: "curl -s https://b", Enabled: false},
	}
	if err := fs.Save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := fs.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(got))
	}
	for i, j := range want {
		if got[i] != j {
			t.Errorf("job %d: want %+v, got %+v", i, j, got[i])
		}
	}
}

func TestFileStoreSaveCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "jobs.json")
	if err := NewFileStore(path).Save([]Job{{Id: "x", Enabled: true}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist after save: %v", err)
	}
}

func TestFileStoreLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewFileStore(path).Load()
	if err == nil {
		t.Fatal("want error for corrupt store file")
	}
	if !strings.Contains(err.Error(), "jobs.json") {
		t.Errorf("error should name the store path: %v", err)
	}
}

func TestFileStoreSaveLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	if err := NewFileStore(path).Save([]Job{{Id: "x", Enabled: true}}); err != nil {
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

Run: `go test ./...`
Expected: FAIL — compile error (no `Job`, no `Store`, no `NewFileStore`).

- [ ] **Step 3: Write minimal implementation**

Create `store.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Job struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	Schedule   string `json:"schedule"`
	Curl       string `json:"curl"`
	Retries    int    `json:"retries"`
	RetryDelay int    `json:"retry_delay"`
	Enabled    bool   `json:"enabled"`
}

type Store interface {
	Load() ([]Job, error)
	Save([]Job) error
}

type FileStore struct {
	Path string
}

func NewFileStore(path string) *FileStore {
	return &FileStore{Path: path}
}

func (f *FileStore) Load() ([]Job, error) {
	data, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var jobs []Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.Path, err)
	}
	return jobs, nil
}

func (f *FileStore) Save(jobs []Job) error {
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(f.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "jobs-*.tmp")
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
	return os.Rename(tmpName, f.Path)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS (all 5 `TestFileStore*` functions).

- [ ] **Step 5: Commit**

```bash
git add store.go store_test.go
git commit -m "feat: add Job model, Store interface, and atomic FileStore"
```

---

### Task 2: Adapt runner to new Job struct

**Files:**
- Modify: `runner.go` (lines 43-49, 80: `Job` fields; sleep uses int seconds; error message uses `Id`)
- Modify: `runner_test.go` (all `Job{...}` literals gain `Id`, drop `Index`, `RetryDelay` becomes int seconds)

**Interfaces:**
- Consumes: `Job` from Task 1.
- Produces (unchanged signatures):
  - `func ParseCommand(cmd string) ([]string, error)`
  - `type Result struct { Attempt int; Total int; ExitCode int; Output string }`
  - `func Run(job Job, curlPath string) ([]Result, error)` — up to `job.Retries+1` attempts, sleeping `time.Duration(job.RetryDelay)*time.Second` between failures, stopping early on exit 0.

- [ ] **Step 1: Rewrite the runner tests for the new Job shape, add a run-with-delay test**

Edit `runner_test.go`: add `"time"` to the import block, replace every `Job{Index: 0, ...}` literal with `Job{Id: "j0", ...}`, and change `RetryDelay: 5*time.Second`-style literals to integer seconds. Add this test:

```go
func TestRunHonorsRetryDelay(t *testing.T) {
	curlPath := fakeCurl(t, 2)
	start := time.Now()
	job := Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 1, RetryDelay: 1}
	results, err := Run(job, curlPath)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 attempts, got %d", len(results))
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("expected at least ~1s delay between retries, took %v", elapsed)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./...`
Expected: FAIL — compile errors in `runner.go` (`job.Index`, `job.RetryDelay` as Duration) and possibly test literals using old fields.

- [ ] **Step 3: Update `runner.go` to the new Job**

Edit `runner.go`:

- Add JSON tags to `Result` so the run endpoint marshals stable keys for the SPA:
  ```go
  type Result struct {
      Attempt  int    `json:"attempt"`
      Total    int    `json:"total"`
      ExitCode int    `json:"exit"`
      Output   string `json:"output"`
  }
  ```
- In `Run`, change the sleep to:
  ```go
  time.Sleep(time.Duration(job.RetryDelay) * time.Second)
  ```
- Change the final error to:
  ```go
  return results, fmt.Errorf("job %s failed after %d attempts (last exit=%d)", job.Id, total, lastExit)
  ```

The rest of `runner.go` (`ParseCommand`, `tail`, `outputCap`) is unchanged.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS (all runner + store tests).

- [ ] **Step 5: Commit**

```bash
git add runner.go runner_test.go
git commit -m "feat: adapt runner to new Job model"
```

---

### Task 3: Controller — validation, CRUD, cron reconciliation, persistence

**Files:**
- Create: `controller.go`
- Test: `controller_test.go`

**Interfaces:**
- Consumes: `Job`, `Store`, `NewFileStore` (Task 1); `ParseCommand`, `Run`, `Result` (Task 2); `cron/v3`.
- Produces:
  - Sentinel errors: `var ErrNotFound = errors.New("job not found")`, `var ErrValidation = errors.New("validation failed")`, `var ErrStorage = errors.New("storage failed")`.
  - `func NewController(store Store, curlPath string) (*Controller, error)` — loads stored jobs via `store.Load()`, validates and registers enabled ones; returns error naming the offending job if any stored job is invalid or cron registration fails.
  - `func (c *Controller) Start()` / `func (c *Controller) Stop() <-chan struct{}` — proxy `cron.Cron` Start/Stop.
  - `func (c *Controller) List() []Job` — snapshot in insertion order (enabled + disabled).
  - `func (c *Controller) Get(id string) (Job, error)` — `ErrNotFound` wrapped if missing.
  - `func (c *Controller) Create(j Job) (Job, error)` — generates `Id` if empty, rejects duplicate id (`ErrValidation`), validates, registers, persists; rolls back on save failure.
  - `func (c *Controller) Update(id string, j Job) (Job, error)` — full replace by id; revalidates; reconciles cron (entry removed+readded for schedule change or enable/disable toggle); persists; rolls back on save failure. `Id` field of replacement is ignored (keeps the path id).
  - `func (c *Controller) Delete(id string) error` — removes from list + registry, persists, rolls back on failure.
  - `func (c *Controller) Enable(id string) error` / `func (c *Controller) Disable(id string) error` — toggle `Enabled` then reconcile + persist.
  - `func (c *Controller) Run(id string) ([]Result, error)` — manual run via `Run`; `ErrNotFound` wrapped if missing.

- [ ] **Step 1: Write the failing tests**

Create `controller_test.go`:

```go
package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type memStore struct {
	jobs []Job
	loadErr error
	numSaves int
}

func (m *memStore) Load() ([]Job, error)      { return m.jobs, m.loadErr }
func (m *memStore) Save(j []Job) error        { m.jobs = j; m.numSaves++; return nil }

type failingStore struct{ memStore }

func (f *failingStore) Save([]Job) error { return errors.New("disk full") }

func newTestController(t *testing.T, store Store) *Controller {
	t.Helper()
	c, err := NewController(store, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}

func TestNewControllerLoadsStoredJobs(t *testing.T) {
	c := newTestController(t, &memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
		{Id: "b", Schedule: "* * * * *", Curl: "curl http://y", Enabled: false},
	}})
	jobs := c.List()
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	if _, ok := c.entries["a"]; !ok {
		t.Error("enabled job must be registered with cron")
	}
	if _, ok := c.entries["b"]; ok {
		t.Error("disabled job must not be registered with cron")
	}
}

func TestNewControllerWithBadStoredJobFails(t *testing.T) {
	_, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "not-cron", Curl: "curl http://x", Enabled: true},
	}}, "/bin/true")
	if err == nil {
		t.Fatal("want error for invalid stored job")
	}
	if !strings.Contains(err.Error(), "a") {
		t.Errorf("error should name job id %q: %v", "a", err)
	}
}

func TestCreateGeneratesIdWhenEmpty(t *testing.T) {
	c := newTestController(t, &memStore{})
	j, err := c.Create(Job{Schedule: "* * * * *", Curl: "curl http://x"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if j.Id == "" {
		t.Fatal("id should be generated")
	}
	if !j.Enabled {
		t.Error("enabled should default true")
	}
	if _, err := c.Get(j.Id); err != nil {
		t.Errorf("created job should be gettable: %v", err)
	}
}

func TestCreateRejectsDuplicateId(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://y"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestCreateValidatesSchedule(t *testing.T) {
	c := newTestController(t, &memStore{})
	_, err := c.Create(Job{Id: "a", Schedule: "nope", Curl: "curl http://x"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestCreateValidatesCurlCommand(t *testing.T) {
	c := newTestController(t, &memStore{})
	_, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "wget http://x"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation for non-curl command, got %v", err)
	}
}

func TestUpdateReplacesFieldsAndReconciles(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := c.Update("a", Job{Schedule: "0 0 * * *", Curl: "curl http://y", Enabled: false})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Schedule != "0 0 * * *" || got.Name != "" {
		t.Errorf("update wrong: %+v", got)
	}
	if _, ok := c.entries["a"]; ok {
		t.Error("job disabled by update should be unregistered")
	}
	if got.Enabled {
		t.Error("enabled should be false after update")
	}
}

func TestUpdateKeepsIdFromPath(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := c.Update("a", Job{Id: "other", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Id != "a" {
		t.Errorf("id should stay %q, got %q", "a", got.Id)
	}
}

func TestUpdateMissingJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	_, err := c.Update("nope", Job{Schedule: "* * * * *", Curl: "curl http://x", Enabled: true})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteRemovesJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(c.List()) != 0 {
		t.Errorf("job should be gone, got %d", len(c.List()))
	}
	if _, err := c.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteMissingJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	if err := c.Delete("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestEnableAndDisableToggle(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: false}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := c.entries["a"]; ok {
		t.Error("disabled job should not be registered")
	}
	if err := c.Enable("a"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, ok := c.entries["a"]; !ok {
		t.Error("enabled job should be registered")
	}
	if err := c.Disable("a"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, ok := c.entries["a"]; ok {
		t.Error("disabled job should be unregistered")
	}
}

func TestCreateRollsBackOnSaveFailure(t *testing.T) {
	c := newTestController(t, &failingStore{})
	_, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"})
	if !errors.Is(err, ErrStorage) {
		t.Fatalf("want ErrStorage, got %v", err)
	}
	if len(c.List()) != 0 {
		t.Errorf("in-memory list should be rolled back, got %d", len(c.List()))
	}
	if _, ok := c.entries["a"]; ok {
		t.Error("cron entry should be rolled back")
	}
}

func TestListPreservesOrder(t *testing.T) {
	c := newTestController(t, &memStore{})
	for _, id := range []string{"c", "a", "b"} {
		if _, err := c.Create(Job{Id: id, Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	jobs := c.List()
	for i, want := range []string{"c", "a", "b"} {
		if jobs[i].Id != want {
			t.Errorf("order[%d]: want %s, got %s", i, want, jobs[i].Id)
		}
	}
}

func TestControllerConcurrentMutationsNoPanic(t *testing.T) {
	mem := &memStore{}
	c := newTestController(t, mem)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("j%d", i%4)
			switch i % 3 {
			case 0:
				_, _ = c.Create(Job{Id: id, Schedule: "* * * * *", Curl: "curl http://x"})
			case 1:
				_, _ = c.Update(id, Job{Schedule: "*/5 * * * *", Curl: "curl http://y", Enabled: true})
			case 2:
				_ = c.Delete(id)
			}
		}(i)
	}
	wg.Wait()
	c.List()
	if n := len(mem.jobs); n > 4 {
		t.Errorf("more jobs than distinct ids possible: %d", n)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./...`
Expected: FAIL — compile error (no `Controller`).

- [ ] **Step 3: Write minimal implementation**

Create `controller.go`:

```go
package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

var (
	ErrNotFound   = errors.New("job not found")
	ErrValidation = errors.New("validation failed")
	ErrStorage    = errors.New("storage failed")
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func wrapValidation(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, a...))
}

type Controller struct {
	mu       sync.Mutex
	store    Store
	cron     *cron.Cron
	jobs     map[string]Job
	order    []string
	entries  map[string]cron.EntryID
	curlPath string
}

func NewController(store Store, curlPath string) (*Controller, error) {
	c := &Controller{
		store:    store,
		cron:     cron.New(),
		jobs:     map[string]Job{},
		entries:  map[string]cron.EntryID{},
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
	return c, nil
}

func (c *Controller) Start()         { c.cron.Start() }
func (c *Controller) Stop() <-chan struct{} { return c.cron.Stop() }

func (c *Controller) List() []Job {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Job, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.jobs[id])
	}
	return out
}

func (c *Controller) Get(id string) (Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	j, ok := c.jobs[id]
	if !ok {
		return Job{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return j, nil
}

func (c *Controller) Create(start Job) (Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if start.Id == "" {
		start.Id = genID()
	}
	if _, exists := c.jobs[start.Id]; exists {
		return Job{}, wrapValidation("a job with id %q already exists", start.Id)
	}
	if err := c.validate(start); err != nil {
		return Job{}, err
	}
	c.jobs[start.Id] = start
	c.order = append(c.order, start.Id)
	if err := c.register(start); err != nil {
		delete(c.jobs, start.Id)
		c.removeOrder(start.Id)
		return Job{}, err
	}
	if err := c.saveLocked(); err != nil {
		delete(c.jobs, start.Id)
		c.removeOrder(start.Id)
		c.unregister(start.Id)
		return Job{}, err
	}
	log.Printf("job=%s added", start.Id)
	return start, nil
}

func (c *Controller) Update(id string, next Job) (Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.jobs[id]
	if !ok {
		return Job{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	next.Id = cur.Id
	if err := c.validate(next); err != nil {
		return Job{}, err
	}
	prevEnabled := cur.Enabled
	prevSchedule := cur.Schedule
	c.jobs[id] = next
	if prevEnabled != next.Enabled || prevSchedule != next.Schedule {
		c.unregister(id)
		if next.Enabled {
			if err := c.register(next); err != nil {
				c.jobs[id] = cur
				return Job{}, err
			}
		}
	}
	if err := c.saveLocked(); err != nil {
		c.jobs[id] = cur
		return Job{}, err
	}
	log.Printf("job=%s updated", id)
	return next, nil
}

func (c *Controller) Delete(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.jobs[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	pos := c.orderIndex(id)
	delete(c.jobs, id)
	c.removeOrder(id)
	c.unregister(id)
	if err := c.saveLocked(); err != nil {
		// roll back: restore the job, its list position, and registration
		c.jobs[id] = cur
		c.order = append(c.order[:pos], append([]string{id}, c.order[pos:]...)...)
		if err := c.register(cur); err != nil {
			log.Printf("rollback: re-register %s: %v", id, err)
		}
		return err
	}
	log.Printf("job=%s deleted", id)
	return nil
}

func (c *Controller) Enable(id string) error  { return c.setEnabled(id, true) }
func (c *Controller) Disable(id string) error { return c.setEnabled(id, false) }

func (c *Controller) setEnabled(id string, enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.jobs[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	before := cur
	cur.Enabled = enabled
	c.jobs[id] = cur
	c.unregister(id)
	if enabled {
		if err := c.register(cur); err != nil {
			return err
		}
	}
	if err := c.saveLocked(); err != nil {
		// roll back to the original enabled state
		c.jobs[id] = before
		c.unregister(id)
		if before.Enabled {
			if err := c.register(before); err != nil {
				log.Printf("rollback: re-register %s: %v", id, err)
			}
		}
		return err
	}
	action := "disabled"
	if enabled {
		action = "enabled"
	}
	log.Printf("job=%s %s", id, action)
	return nil
}

func (c *Controller) Run(id string) ([]Result, error) {
	c.mu.Lock()
	job, ok := c.jobs[id]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	results, runErr := Run(job, c.curlPath)
	return results, runErr
}

func (c *Controller) validate(j Job) error {
	if !idPattern.MatchString(j.Id) {
		return wrapValidation("id must match %s", idPattern.String())
	}
	if _, err := cron.ParseStandard(j.Schedule); err != nil {
		return wrapValidation("schedule %q: %v", j.Schedule, err)
	}
	if j.Curl == "" {
		return wrapValidation("curl: command is required")
	}
	if _, err := ParseCommand(j.Curl); err != nil {
		return wrapValidation("curl: %v", err)
	}
	if j.Retries < 0 {
		return wrapValidation("retries must be >= 0")
	}
	if j.RetryDelay < 0 {
		return wrapValidation("retry_delay must be >= 0")
	}
	return nil
}

func (c *Controller) register(job Job) error {
	if !job.Enabled {
		return nil
	}
	jobCopy := job
	entryID, err := c.cron.AddFunc(job.Schedule, func() {
		c.fire(jobCopy)
	})
	if err != nil {
		return err
	}
	c.entries[job.Id] = entryID
	return nil
}

func (c *Controller) unregister(id string) {
	if entryID, ok := c.entries[id]; ok {
		c.cron.Remove(entryID)
		delete(c.entries, id)
	}
}

func (c *Controller) orderIndex(id string) int {
	for i, cur := range c.order {
		if cur == id {
			return i
		}
	}
	return -1
}

func (c *Controller) removeOrder(id string) {
	if i := c.orderIndex(id); i >= 0 {
		c.order = append(c.order[:i], c.order[i+1:]...)
	}
}

func (c *Controller) saveLocked() error {
	if err := c.store.Save(c.snapshot()); err != nil {
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	return nil
}

func (c *Controller) snapshot() []Job {
	out := make([]Job, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.jobs[id])
	}
	return out
}

func (c *Controller) fire(job Job) {
	results, err := Run(job, c.curlPath)
	for _, r := range results {
		log.Printf("job=%s schedule=%q attempt=%d/%d exit=%d output=%s",
			job.Id, job.Schedule, r.Attempt, r.Total, r.ExitCode, strings.TrimSpace(r.Output))
	}
	if err != nil {
		log.Printf("job=%s failed: %v", job.Id, err)
	}
}

func genID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("job%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS (all controller + runner + store tests).

- [ ] **Step 5: Commit**

```bash
git add controller.go controller_test.go
git commit -m "feat: add live-reloading job controller"
```

---

### Task 4: HTTP API with token auth

**Files:**
- Create: `api.go`
- Test: `api_test.go`

**Interfaces:**
- Consumes: `Controller`, `ErrNotFound`, `ErrValidation`, `ErrStorage`, `Job` (Task 3/1).
- Produces:
  - `type jobRequest struct` — pointer fields (`*string`, `*int`, `*bool`) mirroring the job JSON body; helper `func (r jobRequest) toJob() Job` applying defaults (`enabled` → true, `retry_delay` → 5 when nil; other fields copied or zero).
  - `func NewServer(ctrl *Controller, token string) http.Handler` — ServeMux with method+wildcard patterns, routes:
    - `GET /api/v1/jobs` → list
    - `POST /api/v1/jobs` → create
    - `GET /api/v1/jobs/{id}` → get
    - `PUT /api/v1/jobs/{id}` → update
    - `DELETE /api/v1/jobs/{id}` → delete
    - `POST /api/v1/jobs/{id}/run` → manual run
  - Auth middleware: `Bearer <token>` required, constant-time compare, 401 on failure.

- [ ] **Step 1: Write the failing tests**

Create `api_test.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer(t *testing.T, store Store) http.Handler {
	t.Helper()
	c, err := NewController(store, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return NewServer(c, "secret")
}

func req(t *testing.T, h http.Handler, method, path, token string, body any) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rd)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w, w.Body.Bytes()
}

func TestAPIRequiresToken(t *testing.T) {
	h := newTestServer(t, &memStore{})
	w, _ := req(t, h, "GET", "/api/v1/jobs", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	w, _ = req(t, h, "GET", "/api/v1/jobs", "wrong", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for wrong token, got %d", w.Code)
	}
}

func TestAPICreateAndList(t *testing.T) {
	store := &memStore{}
	h := newTestServer(t, store)
	body := map[string]any{"name": "x", "schedule": "*/5 * * * *", "curl": "curl http://x"}
	w, b := req(t, h, "POST", "/api/v1/jobs", "secret", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", w.Code, b)
	}
	var created Job
	if err := json.Unmarshal(b, &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Id == "" || !created.Enabled || created.RetryDelay != 5 {
		t.Errorf("bad create result: %+v", created)
	}
	w, b = req(t, h, "GET", "/api/v1/jobs", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", w.Code)
	}
	var jobs []Job
	if err := json.Unmarshal(b, &jobs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Id != created.Id {
		t.Errorf("list mismatch: %+v", jobs)
	}
}

func TestAPIValidationReturns400(t *testing.T) {
	h := newTestServer(t, &memStore{})
	body := map[string]any{"schedule": "nope", "curl": "curl http://x"}
	w, b := req(t, h, "POST", "/api/v1/jobs", "secret", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, b)
	}
	var e map[string]string
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	if e["error"] == "" {
		t.Error("error body should have message")
	}
}

func TestAPIGetMissingReturns404(t *testing.T) {
	h := newTestServer(t, &memStore{})
	w, _ := req(t, h, "GET", "/api/v1/jobs/nope", "secret", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestAPIUpdate(t *testing.T) {
	h := newTestServer(t, &memStore{})
	w, b := req(t, h, "POST", "/api/v1/jobs", "secret",
		map[string]any{"id": "a", "schedule": "* * * * *", "curl": "curl http://x"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", w.Code, b)
	}
	body := map[string]any{"schedule": "0 0 * * *", "curl": "curl http://y", "enabled": false}
	w, b = req(t, h, "PUT", "/api/v1/jobs/a", "secret", body)
	if w.Code != http.StatusOK {
		t.Fatalf("update: want 200, got %d: %s", w.Code, b)
	}
	var j Job
	if err := json.Unmarshal(b, &j); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if j.Schedule != "0 0 * * *" || j.Id != "a" || j.Enabled {
		t.Errorf("update result wrong: %+v", j)
	}
}

func TestAPIDeleteReturns204(t *testing.T) {
	h := newTestServer(t, &memStore{})
	req(t, h, "POST", "/api/v1/jobs", "secret",
		map[string]any{"id": "a", "schedule": "* * * * *", "curl": "curl http://x"})
	w, _ := req(t, h, "DELETE", "/api/v1/jobs/a", "secret", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	w, _ = req(t, h, "GET", "/api/v1/jobs/a", "secret", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 after delete, got %d", w.Code)
	}
}

func TestAPISaveFailureReturns500(t *testing.T) {
	h := newTestServer(t, &failingStore{})
	body := map[string]any{"schedule": "* * * * *", "curl": "curl http://x"}
	w, _ := req(t, h, "POST", "/api/v1/jobs", "secret", body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
	}
}

func TestAPIRunManual(t *testing.T) {
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, "secret")
	w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("run: want 200, got %d: %s", w.Code, b)
	}
	var resp struct {
		Steps []Result `json:"steps"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Steps) == 0 {
		t.Fatal("want at least one run step")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./...`
Expected: FAIL — compile error (no `NewServer`, no `jobRequest`). (`memStore`, `failingStore` come from Task 3 tests in `controller_test.go`.)

- [ ] **Step 3: Write minimal implementation**

Create `api.go`:

```go
package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
)

type jobRequest struct {
	Id         *string `json:"id"`
	Name       *string `json:"name"`
	Schedule   *string `json:"schedule"`
	Curl       *string `json:"curl"`
	Retries    *int    `json:"retries"`
	RetryDelay *int    `json:"retry_delay"`
	Enabled    *bool   `json:"enabled"`
}

func (r jobRequest) toJob() Job {
	j := Job{Enabled: true}
	if r.Id != nil {
		j.Id = *r.Id
	}
	if r.Name != nil {
		j.Name = *r.Name
	}
	if r.Schedule != nil {
		j.Schedule = *r.Schedule
	}
	if r.Curl != nil {
		j.Curl = *r.Curl
	}
	if r.Retries != nil {
		j.Retries = *r.Retries
	}
	if r.RetryDelay != nil {
		j.RetryDelay = *r.RetryDelay
	} else {
		j.RetryDelay = 5
	}
	if r.Enabled != nil {
		j.Enabled = *r.Enabled
	}
	return j
}

type Server struct {
	ctrl  *Controller
	token string
}

func NewServer(ctrl *Controller, token string) http.Handler {
	s := &Server{ctrl: ctrl, token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/jobs", s.auth(s.handleList))
	mux.HandleFunc("POST /api/v1/jobs", s.auth(s.handleCreate))
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.auth(s.handleGet))
	mux.HandleFunc("PUT /api/v1/jobs/{id}", s.auth(s.handleUpdate))
	mux.HandleFunc("DELETE /api/v1/jobs/{id}", s.auth(s.handleDelete))
	mux.HandleFunc("POST /api/v1/jobs/{id}/run", s.auth(s.handleRun))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, prefix) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		got := strings.TrimPrefix(h, prefix)
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.ctrl.List())
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req jobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: %v", err)
		return
	}
	job, err := s.ctrl.Create(req.toJob())
	if err != nil {
		writeCtrlError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	job, err := s.ctrl.Get(r.PathValue("id"))
	if err != nil {
		writeCtrlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var req jobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: %v", err)
		return
	}
	job, err := s.ctrl.Update(r.PathValue("id"), req.toJob())
	if err != nil {
		writeCtrlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.ctrl.Delete(r.PathValue("id")); err != nil {
		writeCtrlError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	results, err := s.ctrl.Run(r.PathValue("id"))
	if err != nil {
		writeCtrlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Steps []Result `json:"steps"`
	}{Steps: results})
}

func writeCtrlError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrStorage):
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, format string, a ...any) {
	writeJSON(w, status, map[string]string{"error": fmt.Sprintf(format, a...)})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS (all tests).

- [ ] **Step 5: Commit**

```bash
git add api.go api_test.go
git commit -m "feat: add REST API with bearer token auth"
```

---

### Task 5: CLI — thin HTTP client

**Files:**
- Create: `cli.go`
- Test: `cli_test.go`

**Interfaces:**
- Consumes: `Controller`-free; talks to `http.Handler` from Task 4 through the network. Uses `Job` for JSON decoding.
- Produces:
  - `func RunCLI(args []string) int` — dispatches subcommands; exit code `0` success, `1` usage/arg error, `2` API/HTTP error.
  - Subcommands: `list`, `get <id>`, `add`, `update <id>`, `delete <id>`, `enable <id>`, `disable <id>`, `run <id>`, `help`.
  - Common flags on every mutation/list command: `--addr` (default `http://127.0.0.1:8080`), `--token` (default `$CRONIX_API_TOKEN`).
  - `add`: `--id`, `--name`, `--schedule`, `--curl`, `--retries` (default 0), `--retry-delay` (default 5), `--enabled` (default true). Requires `--schedule` and `--curl`.
  - `update <id>`: same flags as add (all optional except none required).
  - `--enabled` flag accepts `true`/`false`.

- [ ] **Step 1: Write the failing tests**

Create `cli_test.go`:

```go
package main

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

func withCLIServer(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRunCLC_listAndAddFlow(t *testing.T) {
	c, err := NewController(&memStore{}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	addr := withCLIServer(t, NewServer(c, "tok"))
	out := new(bytes.Buffer)
	code := runCLIIn([]string{"add", "--addr", addr, "--token", "tok",
		"--name", "ping", "--schedule", "*/5 * * * *", "--curl", "curl http://x"}, out)
	if code != 0 {
		t.Fatalf("add: want exit 0, got %d: %s", code, out.String())
	}
	out.Reset()
	code = runCLIIn([]string{"list", "--addr", addr, "--token", "tok"}, out)
	if code != 0 {
		t.Fatalf("list: want exit 0, got %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "ping") {
		t.Errorf("list should contain job name, got: %s", out.String())
	}
}

func TestRunCLIMissingArgs(t *testing.T) {
	out := new(bytes.Buffer)
	code := runCLIIn([]string{"add"}, out)
	if code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
}

func TestRunCLIBadTokenExit2(t *testing.T) {
	c, err := NewController(&memStore{}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	addr := withCLIServer(t, NewServer(c, "tok"))
	out := new(bytes.Buffer)
	code := runCLIIn([]string{"list", "--addr", addr, "--token", "nope"}, out)
	if code != 2 {
		t.Fatalf("want exit 2, got %d", code)
	}
	if !strings.Contains(out.String(), "unauthorized") {
		t.Errorf("should print unauthorized error, got: %s", out.String())
	}
}

func TestRunCLICommandDoesNotExist(t *testing.T) {
	out := new(bytes.Buffer)
	code := runCLIIn([]string{"frobnicate"}, out)
	if code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./...`
Expected: FAIL — compile error (no `RunCLI`, `runCLIIn`).

- [ ] **Step 3: Write minimal implementation**

Create `cli.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
)

const defaultCLIAddr = "http://127.0.0.1:8080"

func RunCLI(args []string) int {
	return runCLIIn(args, os.Stdout)
}

func runCLIIn(args []string, out io.Writer) int {
	if len(args) == 0 {
		usage(out)
		return 1
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(out)
		return 0
	case "list":
		return cliList(args[1:], out)
	case "get":
		return cliGet(args[1:], out)
	case "add":
		return cliAdd(args[1:], out)
	case "update":
		return cliUpdate(args[1:], out)
	case "delete":
		return cliDelete(args[1:], out)
	case "enable":
		return cliSetEnabled(args[1:], out, true)
	case "disable":
		return cliSetEnabled(args[1:], out, false)
	case "run":
		return cliRun(args[1:], out)
	default:
		fmt.Fprintf(out, "unknown command: %s\n", args[0])
		usage(out)
		return 1
	}
}

type cliOpts struct {
	addr  string
	token string
}

func cliFlags(fs *flag.FlagSet, args []string) (cliOpts, error) {
	addr := fs.String("addr", defaultCLIAddr, "API base URL")
	token := fs.String("token", os.Getenv("CRONIX_API_TOKEN"), "API bearer token")
	if err := fs.Parse(args); err != nil {
		return cliOpts{}, err
	}
	return cliOpts{addr: *addr, token: *token}, nil
}

func cliList(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	status, body, err := cliHTTP("GET", opts.addr+"/api/v1/jobs", opts.token, nil)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusOK {
		return cliErr(out, status, body)
	}
	var jobs []Job
	if err := json.Unmarshal(body, &jobs); err != nil {
		fmt.Fprintf(out, "error: cannot decode response: %v\n", err)
		return 2
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tENABLED\tSCHEDULE")
	for _, j := range jobs {
		fmt.Fprintf(w, "%s\t%s\t%v\t%s\n", j.Id, j.Name, j.Enabled, j.Schedule)
	}
	w.Flush()
	return 0
}

func cliGet(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(out, "usage: cronix cli get <id> [--addr URL] [--token TOKEN]")
		return 1
	}
	status, body, err := cliHTTP("GET", opts.addr+"/api/v1/jobs/"+fs.Arg(0), opts.token, nil)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusOK {
		return cliErr(out, status, body)
	}
	out.Write(body)
	fmt.Fprintln(out)
	return 0
}

func cliAdd(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	id := fs.String("id", "", "job id (generated if empty)")
	name := fs.String("name", "", "job name")
	schedule := fs.String("schedule", "", "5-field cron expression")
	curlCmd := fs.String("curl", "", "curl command")
	retries := fs.Int("retries", 0, "extra attempts on failure")
	retryDelay := fs.Int("retry-delay", 5, "seconds between retries")
	enabled := fs.Bool("enabled", true, "enable the job")
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	if *schedule == "" || *curlCmd == "" {
		fmt.Fprintln(out, "add requires --schedule and --curl")
		return 1
	}
	body := map[string]any{
		"id": *id, "name": *name, "schedule": *schedule, "curl": *curlCmd,
		"retries": *retries, "retry_delay": *retryDelay, "enabled": *enabled,
	}
	status, resp, err := cliHTTP("POST", opts.addr+"/api/v1/jobs", opts.token, body)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusCreated {
		return cliErr(out, status, resp)
	}
	var j Job
	if err := json.Unmarshal(resp, &j); err != nil {
		fmt.Fprintf(out, "error: cannot decode response: %v\n", err)
		return 2
	}
	fmt.Fprintf(out, "created %s\n", j.Id)
	return 0
}

func cliUpdate(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "job name")
	schedule := fs.String("schedule", "", "5-field cron expression")
	curlCmd := fs.String("curl", "", "curl command")
	retries := fs.Int("retries", 0, "extra attempts on failure")
	retryDelay := fs.Int("retry-delay", 5, "seconds between retries")
	enabled := fs.Bool("enabled", true, "enable the job")
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(out, "usage: cronix cli update <id> [--schedule S] [--curl C] ...")
		return 1
	}
	id := fs.Arg(0)
	job, err := cliGetJob(opts, id)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	// Partial update: only overlay flags the user actually set, then PUT the
	// full replacement so unchanged fields are preserved.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["name"] {
		job.Name = *name
	}
	if set["schedule"] {
		job.Schedule = *schedule
	}
	if set["curl"] {
		job.Curl = *curlCmd
	}
	if set["retries"] {
		job.Retries = *retries
	}
	if set["retry-delay"] {
		job.RetryDelay = *retryDelay
	}
	if set["enabled"] {
		job.Enabled = *enabled
	}
	status, resp, err := cliHTTP("PUT", opts.addr+"/api/v1/jobs/"+id, opts.token, map[string]any{
		"name":        job.Name,
		"schedule":    job.Schedule,
		"curl":        job.Curl,
		"retries":     job.Retries,
		"retry_delay": job.RetryDelay,
		"enabled":     job.Enabled,
	})
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusOK {
		return cliErr(out, status, resp)
	}
	out.Write(resp)
	fmt.Fprintln(out)
	return 0
}

func cliDelete(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(out, "usage: cronix cli delete <id> [--addr URL] [--token TOKEN]")
		return 1
	}
	status, resp, err := cliHTTP("DELETE", opts.addr+"/api/v1/jobs/"+fs.Arg(0), opts.token, nil)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusNoContent {
		return cliErr(out, status, resp)
	}
	fmt.Fprintf(out, "deleted %s\n", fs.Arg(0))
	return 0
}

func cliSetEnabled(args []string, out io.Writer, enabled bool) int {
	cmd := "enable"
	if !enabled {
		cmd = "disable"
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(out, "usage: cronix cli %s <id> [--addr URL] [--token TOKEN]\n", cmd)
		return 1
	}
	id := fs.Arg(0)
	job, err := cliGetJob(opts, id)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	status, resp, err := cliHTTP("PUT", opts.addr+"/api/v1/jobs/"+id, opts.token, map[string]any{
		"name":        job.Name,
		"schedule":    job.Schedule,
		"curl":        job.Curl,
		"retries":     job.Retries,
		"retry_delay": job.RetryDelay,
		"enabled":     enabled,
	})
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusOK {
		return cliErr(out, status, resp)
	}
	fmt.Fprintf(out, "%s %s\n", cmd, id)
	return 0
}

func cliGetJob(opts cliOpts, id string) (Job, error) {
	status, body, err := cliHTTP("GET", opts.addr+"/api/v1/jobs/"+id, opts.token, nil)
	if err != nil {
		return Job{}, err
	}
	if status != http.StatusOK {
		return Job{}, fmt.Errorf("cannot fetch job %s (status %d)", id, status)
	}
	var j Job
	if err := json.Unmarshal(body, &j); err != nil {
		return Job{}, fmt.Errorf("cannot decode job: %w", err)
	}
	return j, nil
}

func cliRun(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(out, "usage: cronix cli run <id> [--addr URL] [--token TOKEN]")
		return 1
	}
	status, resp, err := cliHTTP("POST", opts.addr+"/api/v1/jobs/"+fs.Arg(0)+"/run", opts.token, nil)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusOK {
		return cliErr(out, status, resp)
	}
	var runResp struct {
		Steps []Result `json:"steps"`
	}
	if err := json.Unmarshal(resp, &runResp); err != nil {
		fmt.Fprintf(out, "error: cannot decode run: %v\n", err)
		return 2
	}
	for _, r := range runResp.Steps {
		fmt.Fprintf(out, "attempt=%d/%d exit=%d output=%s\n",
			r.Attempt, r.Total, r.ExitCode, strings.TrimSpace(r.Output))
	}
	return 0
}

func cliHTTP(method, url, token string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, b, nil
}

func cliErr(out io.Writer, status int, body []byte) int {
	var e map[string]string
	if err := json.Unmarshal(body, &e); err == nil && e["error"] != "" {
		fmt.Fprintf(out, "error: %s\n", e["error"])
	} else {
		fmt.Fprintf(out, "error: status %d\n", status)
	}
	return 2
}

func usage(out io.Writer) {
	fmt.Fprintf(out, `usage: cronix [cli <command>] ...

Commands:
  list                        list jobs
  get <id>                    show one job
  add ...                     create a job
  update <id> ...             replace a job
  delete <id>                 delete a job
  enable <id>                 enable a paused job
  disable <id>                disable a job
  run <id>                    run a job now

Common flags: --addr URL (default %s)  --token TOKEN (default $CRONIX_API_TOKEN)
add/update flags: --id --name --schedule --curl --retries --retry-delay --enabled
`, defaultCLIAddr)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS (all CLI + earlier tests).

- [ ] **Step 5: Commit**

```bash
git add cli.go cli_test.go
git commit -m "feat: add cronix cli client for the REST API"
```

---

### Task 6: main.go — server/cli dispatch, drop env config

**Files:**
- Modify: `main.go` (rewrite; remove env-var job loading)
- Delete: `config.go`
- Delete: `config_test.go`

**Interfaces:**
- Consumes: `RunCLI` (Task 5), `NewController`, `Controller.Start/Stop` (Task 3), `NewServer` (Task 4), `NewFileStore` (Task 1).
- Produces: none to later tasks (process entrypoint). Reads env: `CRONIX_STORE_PATH` (default `/data/jobs.json`), `CRONIX_HTTP_ADDR` (default `:8080`), `CRONIX_API_TOKEN` (required), `CURL_PATH` (default `/usr/local/bin/curl`).

- [ ] **Step 1: Delete env-var config files and their tests**

Run:
```bash
git rm config.go config_test.go
```

- [ ] **Step 2: Rewrite main.go**

Replace `main.go` entirely:

```go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	defaultStorePath = "/data/jobs.json"
	defaultHTTPAddr  = ":8080"
	defaultCurlPath  = "/usr/local/bin/curl"
)

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "cli" {
		os.Exit(RunCLI(os.Args[2:]))
	}

	token := os.Getenv("CRONIX_API_TOKEN")
	if strings.TrimSpace(token) == "" {
		log.Fatalf("CRONIX_API_TOKEN is required; refusing to start")
	}

	storePath := envOr("CRONIX_STORE_PATH", defaultStorePath)
	httpAddr := envOr("CRONIX_HTTP_ADDR", defaultHTTPAddr)
	curlPath := envOr("CURL_PATH", defaultCurlPath)

	ctrl, err := NewController(NewFileStore(storePath), curlPath)
	if err != nil {
		log.Fatalf("startup error: %v", err)
	}

	jobs := ctrl.List()
	log.Printf("cronix started with %d job(s)", len(jobs))
	if len(jobs) == 0 {
		log.Printf("no jobs configured; add one with '/cronix cli add' or the web UI at http://%s", httpAddr)
	}
	ctrl.Start()

	srv := &http.Server{Addr: httpAddr, Handler: NewServer(ctrl, token)}
	go func() {
		log.Printf("api listening on %s", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("api server: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	<-ctx.Done()

	log.Printf("shutting down")
	done := ctrl.Stop()
	select {
	case <-done.Done():
	case <-time.After(10 * time.Second):
		log.Printf("in-flight jobs did not finish in 10s; exiting")
	}
	log.Printf("bye")
}
```

- [ ] **Step 3: Build, vet, test**

Run:
```bash
go build ./...
go vet ./...
go test ./...
```
Expected: build and vet succeed; all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat: dispatch server/cli modes, require API token, drop env job config"
```

---

### Task 7: Embedded SPA

**Files:**
- Create: `web/index.html`
- Create: `web/app.js`
- Create: `web/style.css`
- Create: `spa.go`
- Test: modify `api_test.go` or add `spa_test.go`

**Interfaces:**
- Consumes: nothing from prior tasks; served as static assets.
- Produces:
  - `webFS` — `//go:embed web` embed.FS in `spa.go`.
  - `func SPAHandler() http.Handler` — wraps `http.FileServer(http.FS(sub(webFS, "web")))`.
  - The server from Task 4 is extended to also serve the SPA at `/` (add a `mux.Handle("GET /", SPAHandler())` line in `api.go`/`NewServer`).

- [ ] **Step 1: Write the failing tests**

Create `spa_test.go`:

```go
package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func spaServer(t *testing.T) http.Handler {
	t.Helper()
	c, err := NewController(&memStore{}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return NewServer(c, "secret")
}

func TestSpaServesIndex(t *testing.T) {
	h := spaServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("want html content type, got %q", ct)
	}
}

func TestSpaServesAppJS(t *testing.T) {
	h := spaServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/app.js", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	b, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(b), "fetch") {
		t.Error("app.js should contain fetch API calls")
	}
}
```

`memStore` comes from `controller_test.go`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./...`
Expected: FAIL — compile error (`webFS` missing, or `NewServer` doesn't serve `/`).

- [ ] **Step 3: Write the SPA assets**

Create `web/index.html`:

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Cronix</title>
  <link rel="stylesheet" href="/style.css">
</head>
<body>
  <header>
    <h1>Cronix</h1>
    <input id="token" type="password" placeholder="API token" autocomplete="off">
    <button id="save-token">Save</button>
  </header>
  <main>
    <section id="login" hidden>
      <p>Enter your API token above to continue.</p>
    </section>
    <section id="app" hidden>
      <div id="error" class="error" hidden></div>
      <div class="toolbar">
        <button id="new-job">New job</button>
      </div>
      <table id="jobs">
        <thead><tr><th>ID</th><th>Name</th><th>Enabled</th><th>Schedule</th><th></th></tr></thead>
        <tbody></tbody>
      </table>
    </section>
    <section id="editor" hidden>
      <h2 id="editor-title">New job</h2>
      <form id="job-form">
        <input type="hidden" id="field-id">
        <label>Name <input id="field-name" type="text"></label>
        <label>Schedule <input id="field-schedule" type="text" placeholder="*/5 * * * *"></label>
        <label>Curl <input id="field-curl" type="text" placeholder="curl -s https://example.com"></label>
        <label>Retries <input id="field-retries" type="number" value="0" min="0"></label>
        <label>Retry delay (s) <input id="field-retry-delay" type="number" value="5" min="0"></label>
        <label><input id="field-enabled" type="checkbox" checked> Enabled</label>
        <div id="field-errors" class="error" hidden></div>
        <button type="submit">Save</button>
        <button type="button" id="editor-cancel">Cancel</button>
      </form>
    </section>
    <section id="run-panel" hidden>
      <h2>Run output</h2>
      <pre id="run-output"></pre>
      <button id="run-close">Close</button>
    </section>
  </main>
  <script src="/app.js"></script>
</body>
</html>
```

Create `web/app.js`:

```js
const state = { token: sessionStorage.getItem('cronix_token') || '' };

const $ = (id) => document.getElementById(id);

function authHeaders() {
  const h = { 'Content-Type': 'application/json' };
  if (state.token) h['Authorization'] = 'Bearer ' + state.token;
  return h;
}

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: authHeaders(),
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 401) {
    state.token = '';
    sessionStorage.removeItem('cronix_token');
    render();
    throw new Error('unauthorized');
  }
  if (!res.ok) {
    let msg = 'request failed: ' + res.status;
    try {
      const j = await res.json();
      if (j && j.error) msg = j.error;
    } catch (_) {}
    throw new Error(msg);
  }
  if (res.status === 204) return null;
  return res.json();
}

function render() {
  const authed = !!state.token;
  $('app').hidden = !authed;
  $('login').hidden = authed;
  $('token').value = state.token;
  if (authed) loadJobs();
}

async function loadJobs() {
  try {
    const jobs = await api('GET', '/api/v1/jobs');
    const tbody = $('jobs').querySelector('tbody');
    tbody.innerHTML = '';
    for (const j of jobs) {
      const tr = document.createElement('tr');
      tr.innerHTML = `<td>${escapeHtml(j.id)}</td><td>${escapeHtml(j.name || '')}</td>`
        + `<td><input type="checkbox" data-id="${escapeHtml(j.id)}" class="toggle" ${j.enabled ? 'checked' : ''}></td>`
        + `<td>${escapeHtml(j.schedule)}</td>`;
      const actions = document.createElement('td');
      const edit = document.createElement('button'); edit.textContent = 'edit'; edit.dataset.id = j.id;
      const run = document.createElement('button'); run.textContent = 'run'; run.dataset.id = j.id;
      const del = document.createElement('button'); del.textContent = 'delete'; del.dataset.id = j.id;
      actions.append(edit, run, del);
      tr.appendChild(actions);
      tbody.appendChild(tr);
    }
    showError('');
  } catch (e) {
    showError(e.message);
  }
}

async function toggleJob(id, enabled) {
  const jobs = await api('GET', '/api/v1/jobs');
  const j = jobs.find((x) => x.id === id);
  if (!j) return;
  j.enabled = enabled;
  await api('PUT', '/api/v1/jobs/' + encodeURIComponent(id), {
    name: j.name, schedule: j.schedule, curl: j.curl,
    retries: j.retries, retry_delay: j.retry_delay, enabled,
  });
  loadJobs();
}

async function openEdit(job) {
  $('editor').hidden = false;
  $('editor-title').textContent = job ? 'Edit job' : 'New job';
  $('field-id').value = job ? job.id : '';
  $('field-name').value = job ? job.name || '' : '';
  $('field-schedule').value = job ? job.schedule : '';
  $('field-curl').value = job ? job.curl : '';
  $('field-retries').value = job ? job.retries : 0;
  $('field-retry-delay').value = job ? job.retryDelay : 5;
  $('field-enabled').checked = job ? job.enabled : true;
  $('field-errors').hidden = true;
}

async function saveJob(e) {
  e.preventDefault();
  const job = {
    name: $('field-name').value,
    schedule: $('field-schedule').value,
    curl: $('field-curl').value,
    retries: parseInt($('field-retries').value, 10),
    retry_delay: parseInt($('field-retry-delay').value, 10),
    enabled: $('field-enabled').checked,
  };
  const id = $('field-id').value;
  try {
    if (id) {
      await api('PUT', '/api/v1/jobs/' + encodeURIComponent(id), job);
    } else {
      await api('POST', '/api/v1/jobs', job);
    }
    $('editor').hidden = true;
    loadJobs();
  } catch (e) {
    $('field-errors').textContent = e.message;
    $('field-errors').hidden = false;
  }
}

async function runJob(id) {
  try {
    const res = await api('POST', '/api/v1/jobs/' + encodeURIComponent(id) + '/run');
    const lines = (res.steps || []).map((s) =>
      `attempt=${s.attempt}/${s.total} exit=${s.exit} output=${s.output}`).join('\n');
    $('run-output').textContent = lines || '(no output)';
    $('run-panel').hidden = false;
  } catch (e) {
    showError(e.message);
  }
}

function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

function showError(msg) {
  $('error').textContent = msg;
  $('error').hidden = !msg;
}

$('save-token').addEventListener('click', () => {
  state.token = $('token').value.trim();
  sessionStorage.setItem('cronix_token', state.token);
  render();
});
$('new-job').addEventListener('click', () => openEdit(null));
$('editor-cancel').addEventListener('click', () => { $('editor').hidden = true; });
$('run-close').addEventListener('click', () => { $('run-panel').hidden = true; });
document.getElementById('job-form').addEventListener('submit', saveJob);

document.addEventListener('change', (e) => {
  if (e.target.classList.contains('toggle')) toggleJob(e.target.dataset.id, e.target.checked);
});
document.addEventListener('click', async (e) => {
  const edit = e.target.closest('button[data-id]');
  if (!edit) return;
  if (edit.textContent === 'edit') {
    const j = (await api('GET', '/api/v1/jobs')).find((x) => x.id === edit.dataset.id);
    openEdit(j);
  } else if (edit.textContent === 'run') {
    runJob(edit.dataset.id);
  } else if (edit.textContent === 'delete') {
    await api('DELETE', '/api/v1/jobs/' + encodeURIComponent(edit.dataset.id));
    loadJobs();
  }
});

render();
```

Create `web/style.css`:

```css
:root { color-scheme: light dark; }
body { font-family: system-ui, sans-serif; max-width: 60rem; margin: 0 auto; padding: 1rem; }
header { display: flex; gap: 0.5rem; align-items: center; }
header h1 { margin-right: auto; }
input[type="password"] { width: 16rem; }
table { border-collapse: collapse; width: 100%; margin-top: 1rem; }
th, td { text-align: left; padding: 0.4rem 0.6rem; border-bottom: 1px solid #ccc; }
.toolbar { margin-top: 1rem; }
.error { color: #b00020; background: #ffe0e0; padding: 0.5rem; border-radius: 4px; margin-top: 0.5rem; }
form label { display: block; margin: 0.5rem 0; }
form input[type="text"], form input[type="number"] { width: 24rem; }
pre { background: #f4f4f4; padding: 0.75rem; border-radius: 4px; white-space: pre-wrap; }
button { margin-right: 0.25rem; }
```

Create `spa.go`:

```go
package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web
var webFS embed.FS

func SPAHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
```

In `api.go`, inside `NewServer`, after the API routes add:

```go
mux.Handle("GET /", SPAHandler())
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS — `TestSpaServesIndex` and `TestSpaServesAppJS` pass now that `NewServer` registers the SPA route.

- [ ] **Step 5: Verify tests pass and vet**

Run:
```bash
go test ./...
go vet ./...
```
Expected: all PASS, vet clean.

- [ ] **Step 6: Commit**

```bash
git add web spa.go spa_test.go api.go
git commit -m "feat: embed and serve single-page web UI"
```

---

### Task 8: README, Dockerfile check, and end-to-end verification

**Files:**
- Modify: `README.md` (rewrite for v2: CLI, API, SPA, env vars, volume)
- Modify: `Dockerfile` (only if needed — should be unchanged; verify `COPY . .` includes `web/`)
- Modify: `.dockerignore` (verify `web/` is NOT ignored)

**Interfaces:**
- Consumes: everything.

- [ ] **Step 1: Verify Dockerfile/build context covers new files**

Run:
```bash
cat .dockerignore
```
Confirm `web/` and `*.go` are not listed (only `.git`, `docs`, `Dockerfile`, `.dockerignore`).

- [ ] **Step 2: Rewrite README.md**

Replace the README with v2 documentation covering: modes (`/cronix` server, `/cronix cli ...`), env vars (`CRONIX_STORE_PATH`, `CRONIX_HTTP_ADDR`, `CRONIX_API_TOKEN`, `CURL_PATH`, `TZ`), the JSON store format, full CLI reference, REST API reference, the SPA, the `docker run` + `docker exec` quick start, volume mount example, and a migration note that `CRON_*` env vars are gone.

- [ ] **Step 3: Build the image**

Run: `docker build -t cronix .`
Expected: build succeeds; `web/` assets embedded.

- [ ] **Step 4: End-to-end verification**

Run:
```sh
docker run --rm -d --name cronix-test \
  -e CRONIX_API_TOKEN=sekrit \
  -v "$(mktemp -d):/data" \
  -p 8080:8080 \
  cronix
```
Then:
```sh
docker exec cronix-test /cronix cli add --name ping --schedule '* * * * *' --curl 'curl -s https://example.com' --token sekrit
docker exec cronix-test /cronix cli list --token sekrit
sleep 65
docker logs cronix-test | tail -5
docker exec cronix-test /cronix cli run <id> --token sekrit   # id from add output
docker exec cronix-test /cronix cli disable <id> --token sekrit
curl -s -H "Authorization: Bearer sekrit" http://127.0.0.1:8080/api/v1/jobs
docker exec cronix-test /cronix cli delete <id> --token sekrit
docker stop cronix-test
```
Expected: the scheduled fire appears in logs ~once a minute; `run` prints the attempt line; disable stops firing; curl lists the job; delete removes it.

- [ ] **Step 5: Verify persisted jobs survive restart**

Find the temp dir used in Step 4; `docker run` again with the same volume and confirm `cli list` shows the remaining jobs.

- [ ] **Step 6: Commit**

```bash
git add README.md
git commit -m "docs: rewrite README for Cronix v2"
```