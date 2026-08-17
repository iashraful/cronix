# Cronix — Idiomatic Go Project Structure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure the flat `package main` root into the idiomatic `cmd/` + `internal/` layout with fine-grained domain packages, preserving all behavior.

**Architecture:** Move shared types and errors into `internal/model`; persistence into `internal/store` + `internal/runstore` (sharing `internal/fsutil.AtomicWriteFile`); job execution into `internal/runner`; run-output formatting into `internal/runlog`; the scheduler into `internal/controller`; session tokens into `internal/auth`; the SPA embed into `internal/spa` (assets relocate to `internal/spa/dist/`); HTTP into `internal/api`; the CLI client into `internal/cli`; and wiring into `cmd/cronix/main.go`. Each task creates its package alongside the still-present root package (both compile), so the repo stays green at every commit; the final task deletes the root files.

**Tech Stack:** Go 1.24, Vite 5 (UI), Docker multi-stage, Make, git.

## Global Constraints

- No new Go module dependencies. `go.mod` keeps only `github.com/google/shlex` and `github.com/robfig/cron/v3`.
- No behavior change: schedules, retries, API routes, JSON shapes, CLI output, web UI, Docker image contents all identical.
- `go build ./... && go vet ./... && go test ./...` green at every commit; `gofmt -l .` clean.
- Final Docker image (`FROM scratch`) stays binary-only: `/cronix`, `/usr/local/bin/curl`, certs.
- Module path stays `cronix`; import paths like `cronix/internal/model`.
- `ui/vite.config.js` keeps `emptyOutDir: true` and the `/api` dev proxy; `outDir` changes to `'../internal/spa/dist'`.
- Design: `docs/superpowers/specs/2026-08-17-go-project-structure-design.md` is committed (approved).

---

### Task 1: Create leaf packages `model`, `fsutil`, `store`, `runstore`, `runner`, `runlog`, `auth` + their tests

Create the bottom-of-the-dependency-graph packages as standalone new packages. The root `package main` files are untouched and keep compiling; the new packages are self-contained and import only `cronix/internal/*` or stdlib.

**Files:**
- Create: `internal/model/model.go`
- Create: `internal/fsutil/fsutil.go`
- Create: `internal/store/store.go`
- Create: `internal/store/store_test.go`
- Create: `internal/runstore/run_store.go`
- Create: `internal/runstore/run_store_test.go`
- Create: `internal/runner/runner.go`
- Create: `internal/runner/runner_test.go`
- Create: `internal/runlog/run_log.go`
- Create: `internal/runlog/run_log_test.go`
- Create: `internal/auth/auth.go`
- Create: `internal/auth/auth_test.go`

**Interfaces:**
- Consumes: nothing (stdlib + existing `shlex`/`cron` deps).
- Produces: `model.Job`, `model.Result`, `model.Run`, `model.RunSummary`, `model.ErrNotFound`, `model.ErrValidation`, `model.ErrStorage`, `model.ErrCommandFailed`; `fsutil.AtomicWriteFile(pattern, path string, data []byte) error`; `store.Store`, `store.FileStore`, `store.NewFileStore(path string) *FileStore`; `runstore.RunStore`, `runstore.FileRunStore`, `runstore.NewFileRunStore(path string) *FileRunStore`; `runner.ParseCommand(cmd string) ([]string, error)`, `runner.Run(job model.Job, curlPath string) ([]model.Result, error)`; `runlog.FormatRun(job model.Job, results []model.Result, err error) string`; `auth.IssueSessionToken(key []byte, username string, ttl time.Duration) (string, error)`, `auth.ValidateSessionToken(key []byte, token string) (string, bool)`.

- [ ] **Step 1: Create `internal/model/model.go`**

```go
package model

import (
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("job not found")
	ErrValidation     = errors.New("validation failed")
	ErrStorage        = errors.New("storage failed")
	ErrCommandFailed  = errors.New("command failed after exhausting retries")
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

type Result struct {
	Attempt  int    `json:"attempt"`
	Total    int    `json:"total"`
	ExitCode int    `json:"exit"`
	Output   string `json:"output"`
}

type Run struct {
	JobId    string    `json:"job_id"`
	Trigger  string    `json:"trigger"`
	Time     time.Time `json:"time"`
	Status   string    `json:"status"`
	ExitCode int       `json:"exit_code"`
	Results  []Result  `json:"results"`
}

type RunSummary struct {
	Status   string    `json:"status"`
	ExitCode int       `json:"exit_code"`
	Time     time.Time `json:"time"`
}
```

- [ ] **Step 2: Create `internal/fsutil/fsutil.go`**

```go
package fsutil

import (
	"os"
	"path/filepath"
)

func AtomicWriteFile(pattern, path string, data []byte) error {
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

- [ ] **Step 3: Create `internal/store/store.go`**

```go
package store

import (
	"encoding/json"
	"fmt"
	"os"

	"cronix/internal/fsutil"
	"cronix/internal/model"
)

type Store interface {
	Load() ([]model.Job, error)
	Save([]model.Job) error
}

type FileStore struct {
	Path string
}

func NewFileStore(path string) *FileStore {
	return &FileStore{Path: path}
}

func (f *FileStore) Load() ([]model.Job, error) {
	data, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var jobs []model.Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.Path, err)
	}
	return jobs, nil
}

func (f *FileStore) Save(jobs []model.Job) error {
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return fsutil.AtomicWriteFile("jobs-*.tmp", f.Path, data)
}
```

- [ ] **Step 4: Create `internal/store/store_test.go`** (moved from `store_test.go`, package `store`, `Job` → `model.Job`)

```go
package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cronix/internal/model"
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
	want := []model.Job{
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
	if err := NewFileStore(path).Save([]model.Job{{Id: "x", Enabled: true}}); err != nil {
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
	if err := NewFileStore(path).Save([]model.Job{{Id: "x", Enabled: true}}); err != nil {
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

- [ ] **Step 5: Create `internal/runstore/run_store.go`**

```go
package runstore

import (
	"encoding/json"
	"fmt"
	"os"

	"cronix/internal/fsutil"
	"cronix/internal/model"
)

type RunStore interface {
	LoadRuns() ([]model.Run, error)
	SaveRuns([]model.Run) error
}

type FileRunStore struct {
	Path string
}

func NewFileRunStore(path string) *FileRunStore {
	return &FileRunStore{Path: path}
}

func (f *FileRunStore) LoadRuns() ([]model.Run, error) {
	data, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []model.Run
	if err := json.Unmarshal(data, &runs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.Path, err)
	}
	return runs, nil
}

func (f *FileRunStore) SaveRuns(runs []model.Run) error {
	data, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return fsutil.AtomicWriteFile("runs-*.tmp", f.Path, data)
}
```

- [ ] **Step 6: Create `internal/runstore/run_store_test.go`** (moved from `run_store_test.go`, package `runstore`, `Run` → `model.Run`, `Result` → `model.Result`)

```go
package runstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cronix/internal/model"
)

func TestRunJSONKeys(t *testing.T) {
	r := model.Run{
		JobId: "a", Trigger: "manual", Time: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
		Status: "ok", ExitCode: 0,
		Results: []model.Result{{Attempt: 1, Total: 1, ExitCode: 0, Output: "hi"}},
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
	want := []model.Run{
		{JobId: "a", Trigger: "manual", Time: time.Now().UTC(), Status: "ok", ExitCode: 0},
		{JobId: "b", Trigger: "scheduled", Time: time.Now().UTC().Add(-time.Minute), Status: "failed", ExitCode: 6,
			Results: []model.Result{{Attempt: 1, Total: 1, ExitCode: 6, Output: "x"}}},
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
	if err := NewFileRunStore(path).SaveRuns([]model.Run{{JobId: "a", Status: "ok"}}); err != nil {
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
	if err := NewFileRunStore(path).SaveRuns([]model.Run{{JobId: "a", Status: "ok"}}); err != nil {
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

- [ ] **Step 7: Create `internal/runner/runner.go`** (moved from `runner.go`, package `runner`; `runJob` → exported `Run`; `Job` → `model.Job`; `Result` → `model.Result`; `ErrCommandFailed` → `model.ErrCommandFailed`)

```go
package runner

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/google/shlex"

	"cronix/internal/model"
)

const outputCap = 4096

func ParseCommand(cmd string) ([]string, error) {
	args, err := shlex.Split(cmd)
	if err != nil {
		return nil, fmt.Errorf("cannot parse command %q: %w", cmd, err)
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command %q", cmd)
	}
	if args[0] != "curl" {
		return nil, fmt.Errorf("command must start with curl, got %q", args[0])
	}
	return args, nil
}

func tail(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[len(b)-max:])
}

func Run(job model.Job, curlPath string) ([]model.Result, error) {
	args, err := ParseCommand(job.Curl)
	if err != nil {
		return nil, err
	}
	total := job.Retries + 1
	results := make([]model.Result, 0, total)
	var lastExit int
	for attempt := 1; attempt <= total; attempt++ {
		var buf bytes.Buffer
		cmd := exec.Command(curlPath, args[1:]...)
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		runErr := cmd.Run()
		exit := 0
		if runErr != nil {
			var ee *exec.ExitError
			if errors.As(runErr, &ee) {
				exit = ee.ExitCode()
			} else {
				return nil, fmt.Errorf("cannot start %s: %w", curlPath, runErr)
			}
		}
		lastExit = exit
		results = append(results, model.Result{
			Attempt:  attempt,
			Total:    total,
			ExitCode: exit,
			Output:   tail(buf.Bytes(), outputCap),
		})
		if exit == 0 {
			return results, nil
		}
		if attempt < total {
			time.Sleep(time.Duration(job.RetryDelay) * time.Second)
		}
	}
	return results, fmt.Errorf("%w: job %s failed after %d attempts (last exit=%d)", model.ErrCommandFailed, job.Id, total, lastExit)
}
```

- [ ] **Step 8: Create `internal/runner/runner_test.go`** (moved from `runner_test.go`, package `runner`; `Job` → `model.Job`, `Result` → `model.Result`, `runJob` → `Run`)

```go
package runner

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cronix/internal/model"
)

func fakeCurl(t *testing.T, successAt int) (path, logPath string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "curl")
	logPath = filepath.Join(dir, "log")
	script := `#!/bin/sh
log="${FAKE_LOG:?}"
echo "$@" >> "$log"
count="${FAKE_LOG}.count"
echo "$(( $(cat "$count" 2>/dev/null || echo 0) + 1 ))" > "$count"
if [ "$(cat "$count")" -lt "${SUCCESS_AT:-1}" ]; then
  echo "boom" >&2
  exit 1
fi
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	t.Setenv("FAKE_LOG", logPath)
	t.Setenv("SUCCESS_AT", strconv.Itoa(successAt))
	return path, logPath
}

func TestParseCommandTokensAndQuoting(t *testing.T) {
	args, err := ParseCommand(`curl -s -H "Content-Type: application/json" -d '{"a":1}' http://x`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"curl", "-s", "-H", "Content-Type: application/json", "-d", `{"a":1}`, "http://x"}
	if len(args) != len(want) {
		t.Fatalf("want %d tokens, got %d: %v", len(want), len(args), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("token %d: want %q, got %q", i, want[i], args[i])
		}
	}
}

func TestParseCommandRejectsBadQuoting(t *testing.T) {
	if _, err := ParseCommand(`curl 'unclosed`); err == nil {
		t.Fatal("want error for unterminated quote")
	}
}

func TestParseCommandRejectsNonCurl(t *testing.T) {
	if _, err := ParseCommand(`wget https://x`); err == nil {
		t.Fatal("want error for non-curl command")
	}
}

func TestParseCommandRejectsEmpty(t *testing.T) {
	if _, err := ParseCommand("   "); err == nil {
		t.Fatal("want error for empty command")
	}
}

func TestRunPassesArgs(t *testing.T) {
	curlPath, logPath := fakeCurl(t, 1)
	job := model.Job{Id: "j0", Schedule: "* * * * *", Curl: `curl -H "X-Test: abc" -d "hello world" http://example.com`, Retries: 0}
	_, err := Run(job, curlPath)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	b, _ := os.ReadFile(logPath)
	got := strings.TrimSpace(string(b))
	if got != `-H X-Test: abc -d hello world http://example.com` {
		t.Errorf("unexpected args: %q", got)
	}
}

func TestRunSucceedsFirstAttempt(t *testing.T) {
	curlPath, _ := fakeCurl(t, 1)
	job := model.Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 3}
	results, err := Run(job, curlPath)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 attempt, got %d", len(results))
	}
	if results[0].ExitCode != 0 || results[0].Attempt != 1 {
		t.Errorf("bad result: %+v", results[0])
	}
}

func TestRunRetriesUntilSuccess(t *testing.T) {
	curlPath, _ := fakeCurl(t, 2)
	job := model.Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 5, RetryDelay: 0}
	results, err := Run(job, curlPath)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 attempts, got %d", len(results))
	}
	if results[0].ExitCode != 1 || results[1].ExitCode != 0 {
		t.Errorf("bad result codes: %+v", results)
	}
}

func TestRunGivesUpAfterExhaustion(t *testing.T) {
	curlPath, _ := fakeCurl(t, 99)
	job := model.Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 2, RetryDelay: 0}
	results, err := Run(job, curlPath)
	if err == nil {
		t.Fatal("want error after retries exhausted")
	}
	if len(results) != 3 {
		t.Fatalf("want 3 attempts (1 + 2 retries), got %d", len(results))
	}
	for _, r := range results {
		if r.ExitCode != 1 {
			t.Errorf("all attempts should fail, got %+v", r)
		}
	}
}

func TestRunTruncatesOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "curl")
	script := "#!/bin/sh\nhead -c 9000 /dev/zero | tr '\\0' 'a'\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	job := model.Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 0}
	results, err := Run(job, path)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(results[0].Output) > 4096 {
		t.Fatalf("output should be capped at 4096, got %d", len(results[0].Output))
	}
	if !strings.HasSuffix(results[0].Output, strings.Repeat("a", 4096)) {
		t.Error("output tail should be preserved")
	}
}

func TestRunCannotStartBinary(t *testing.T) {
	job := model.Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 0}
	_, err := Run(job, filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("want error when curl binary missing")
	}
}

func TestRunHonorsRetryDelay(t *testing.T) {
	curlPath, _ := fakeCurl(t, 2)
	start := time.Now()
	job := model.Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 1, RetryDelay: 1}
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

- [ ] **Step 9: Create `internal/runlog/run_log.go`** (moved from `run_log.go`, package `runlog`; `formatRun` → exported `FormatRun`; `Job` → `model.Job`, `Result` → `model.Result`, `ErrCommandFailed` → `model.ErrCommandFailed`)

```go
package runlog

import (
	"errors"
	"fmt"
	"strings"

	"cronix/internal/model"
)

func FormatRun(job model.Job, results []model.Result, err error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "run job=%s", job.Id)
	if job.Name != "" {
		fmt.Fprintf(&b, " name=%s", job.Name)
	}
	fmt.Fprintf(&b, " schedule=%q command=%q\n", job.Schedule, job.Curl)
	for _, r := range results {
		out := strings.TrimSpace(r.Output)
		if out == "" {
			out = "(no output)"
		}
		fmt.Fprintf(&b, "  attempt %d/%d exit=%d\n    output: %s\n", r.Attempt, r.Total, r.ExitCode, out)
	}
	switch {
	case err == nil:
		fmt.Fprintf(&b, "  result: OK")
	case errors.Is(err, model.ErrCommandFailed):
		lastExit, total := 0, 1
		if len(results) > 0 {
			lastExit = results[len(results)-1].ExitCode
			total = results[len(results)-1].Total
		}
		fmt.Fprintf(&b, "  result: FAILED (last exit=%d, %d/%d attempts)", lastExit, len(results), total)
	default:
		fmt.Fprintf(&b, "  result: ERROR (%v)", err)
	}
	return b.String()
}
```

- [ ] **Step 10: Create `internal/runlog/run_log_test.go`** (moved from `run_log_test.go`, package `runlog`; `Job` → `model.Job`, `Result` → `model.Result`, `formatRun` → `FormatRun`, `ErrCommandFailed` → `model.ErrCommandFailed`)

```go
package runlog

import (
	"errors"
	"strings"
	"testing"

	"cronix/internal/model"
)

func TestFormatRunSuccess(t *testing.T) {
	job := model.Job{Id: "a", Name: "ping", Schedule: "* * * * *", Curl: "curl -s https://example.com"}
	results := []model.Result{{Attempt: 1, Total: 1, ExitCode: 0, Output: "hello"}}
	got := FormatRun(job, results, nil)
	for _, want := range []string{
		"run job=a",
		"name=ping",
		`schedule="* * * * *"`,
		`command="curl -s https://example.com"`,
		"attempt 1/1 exit=0",
		"output: hello",
		"result: OK",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatRun success missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "FAILED") || strings.Contains(got, "ERROR") {
		t.Errorf("success run should not mention FAILED/ERROR:\n%s", got)
	}
}

func TestFormatRunFailedExhaustedRetries(t *testing.T) {
	job := model.Job{Id: "a", Name: "", Schedule: "* * * * *", Curl: "curl http://x"}
	results := []model.Result{
		{Attempt: 1, Total: 2, ExitCode: 6, Output: "curl: (6) Could not resolve host"},
		{Attempt: 2, Total: 2, ExitCode: 6, Output: "curl: (6) Could not resolve host"},
	}
	err := errors.Join(model.ErrCommandFailed, errors.New("job a failed after 2 attempts (last exit=6)"))
	got := FormatRun(job, results, err)
	for _, want := range []string{
		"attempt 1/2 exit=6",
		"attempt 2/2 exit=6",
		"curl: (6) Could not resolve host",
		"result: FAILED (last exit=6, 2/2 attempts)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatRun failed missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "name=") {
		t.Errorf("empty name must omit the name= label:\n%s", got)
	}
}

func TestFormatRunNoOutput(t *testing.T) {
	job := model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}
	results := []model.Result{{Attempt: 1, Total: 1, ExitCode: 1, Output: ""}}
	err := errors.Join(model.ErrCommandFailed, errors.New("job a failed after 1 attempts (last exit=1)"))
	got := FormatRun(job, results, err)
	if !strings.Contains(got, "output: (no output)") {
		t.Errorf("empty output should render '(no output)':\n%s", got)
	}
}

func TestFormatRunNonCommandError(t *testing.T) {
	job := model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}
	results := []model.Result{}
	err := errors.New("cannot start /nonexistent/curl: no such file or directory")
	got := FormatRun(job, results, err)
	if !strings.Contains(got, "result: ERROR (cannot start /nonexistent/curl") {
		t.Errorf("generic error should render ERROR (<err>):\n%s", got)
	}
	if strings.Contains(got, "attempt ") {
		t.Errorf("zero results must omit attempt lines:\n%s", got)
	}
}
```

- [ ] **Step 11: Create `internal/auth/auth.go`** (moved from `auth.go`, package `auth`; `issueSessionToken` → exported `IssueSessionToken`, `validateSessionToken` → exported `ValidateSessionToken`; `sessionPayload`/`hmacSHA256` stay unexported)

```go
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type sessionPayload struct {
	User string `json:"u"`
	Exp  int64  `json:"exp"`
}

func IssueSessionToken(key []byte, username string, ttl time.Duration) (string, error) {
	payload, err := json.Marshal(sessionPayload{
		User: username,
		Exp:  time.Now().Add(ttl).Unix(),
	})
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	sig := hmacSHA256(key, []byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func ValidateSessionToken(key []byte, token string) (string, bool) {
	body, sigB64, ok := strings.Cut(token, ".")
	if !ok {
		return "", false
	}
	digest := hmacSHA256(key, []byte(body))
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || !hmac.Equal(sig, digest) {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", false
	}
	var p sessionPayload
	if err := json.Unmarshal(payload, &p); err != nil || p.User == "" {
		return "", false
	}
	if p.Exp <= time.Now().Unix() {
		return "", false
	}
	return p.User, true
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
```

- [ ] **Step 12: Create `internal/auth/auth_test.go`** (moved from `auth_test.go`, package `auth`; `issueSessionToken` → `IssueSessionToken`, `validateSessionToken` → `ValidateSessionToken`)

```go
package auth

import (
	"strings"
	"testing"
	"time"
)

func TestSessionTokenRoundTrip(t *testing.T) {
	token, err := IssueSessionToken([]byte("secret"), "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	user, ok := ValidateSessionToken([]byte("secret"), token)
	if !ok || user != "admin" {
		t.Fatalf("validate: ok=%v user=%q", ok, user)
	}
}

func TestSessionTokenTamperedSignature(t *testing.T) {
	token, err := IssueSessionToken([]byte("secret"), "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	body, _, _ := strings.Cut(token, ".")
	tampered := body + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, ok := ValidateSessionToken([]byte("secret"), tampered); ok {
		t.Fatal("tampered signature must be rejected")
	}
}

func TestSessionTokenWrongKey(t *testing.T) {
	token, err := IssueSessionToken([]byte("secret"), "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, ok := ValidateSessionToken([]byte("other"), token); ok {
		t.Fatal("wrong key must be rejected")
	}
}

func TestSessionTokenExpired(t *testing.T) {
	token, err := IssueSessionToken([]byte("secret"), "admin", -time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, ok := ValidateSessionToken([]byte("secret"), token); ok {
		t.Fatal("expired token must be rejected")
	}
}

func TestSessionTokenMalformed(t *testing.T) {
	for _, token := range []string{"", "abc", "a.b.c", "..", ".sig", "body."} {
		if _, ok := ValidateSessionToken([]byte("secret"), token); ok {
			t.Errorf("malformed token %q must be rejected", token)
		}
	}
	if _, ok := ValidateSessionToken([]byte("secret"), "AAAA.!!!not-base64!!!"); ok {
		t.Fatal("bad base64 signature must be rejected")
	}
}
```

- [ ] **Step 13: Verify the new leaf packages build and test green**

Run: `go build ./internal/... && go vet ./internal/... && go test ./internal/model ./internal/fsutil ./internal/store ./internal/runstore ./internal/runner ./internal/runlog ./internal/auth`
Expected: all PASS. Then run `go build ./... && go vet ./... && go test ./...` — the root package is untouched, so it must still pass (root test suite `ok cronix`).

- [ ] **Step 14: Commit**

```bash
git add internal/
git commit -m "refactor: extract model, fsutil, store, runstore, runner, runlog, auth packages"
```

---

### Task 2: Create `internal/spa` and relocate the UI embed to `internal/spa/dist/`

Move the embedded-UI package into `internal/spa` and retarget the Vite build output from repo-root `dist/` to `internal/spa/dist/`. The root's own `spa.go`/`dist/` stay until Task 7 (root must keep compiling), but the embed directory for the new package must be built now.

**Files:**
- Create: `internal/spa/spa.go`
- Create: `internal/spa/spa_test.go`
- Modify: `ui/vite.config.js:7`
- Modify: `.gitignore`
- Modify: `.dockerignore`
- Delete: none yet (root `dist/` stays until Task 7)

**Interfaces:**
- Consumes: the built UI assets (produced by `npm --prefix ui run build` into `internal/spa/dist/`).
- Produces: `spa.SPAHandler() http.Handler` — serves embedded `dist` with SPA fallback to `/` for unknown paths.

- [ ] **Step 1: Retarget Vite output to `internal/spa/dist`**

In `ui/vite.config.js`, replace line 7 (`outDir: '../dist',`) with:

```js
    outDir: '../internal/spa/dist',
```

- [ ] **Step 2: Update `.gitignore`**

Replace the line `/dist/` with `/internal/spa/dist/`. Keep `/web/`. Result:

```gitignore
/cronix
/bin/
/.data/
/internal/spa/dist/
/web/
```

- [ ] **Step 3: Update `.dockerignore`**

Append a safety line so the generated embed dir is never part of the git/build context copy (the Docker node stage generates it inside the container):

```gitignore
internal/spa/dist
```

- [ ] **Step 4: Build the UI into `internal/spa/dist`**

Run: `npm --prefix ui run build`
Expected: output lines mention `../internal/spa/dist/index.html` and `../internal/spa/dist/assets/index-*.{js,css}`. Confirm: `ls internal/spa/dist/` lists `index.html` and `assets/`.

- [ ] **Step 5: Create `internal/spa/spa.go`** (moved from `spa.go`, package `spa`, embed `dist`)

```go
package spa

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var webFS embed.FS

func SPAHandler() http.Handler {
	sub, err := fs.Sub(webFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if _, err := fs.Stat(sub, p); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 6: Create `internal/spa/spa_test.go`** (rewritten to test `SPAHandler` directly — no server/controller needed)

```go
package spa

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSpaServesIndex(t *testing.T) {
	h := SPAHandler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("want html content type, got %q", ct)
	}
}

func TestSpaServesReactIndex(t *testing.T) {
	h := SPAHandler()
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

func TestSpaServesIndexForClientRoute(t *testing.T) {
	h := SPAHandler()
	for _, path := range []string{"/jobs", "/jobs/abc123", "/runs/xyz"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("GET %s: want 200, got %d", path, w.Code)
		}
		ct := w.Header().Get("Content-Type")
		if !strings.Contains(ct, "text/html") {
			t.Errorf("GET %s: want html content type, got %q", path, ct)
		}
	}
}

func TestSpaServesAsset(t *testing.T) {
	h := SPAHandler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	b, _ := io.ReadAll(w.Result().Body)
	// src looks like "/assets/index-*.js" — derive the path robustly:
	idx := strings.Index(string(b), "/assets/")
	if idx < 0 {
		t.Fatalf("index should reference an asset: %s", b)
	}
	asset := string(b)[idx:]
	asset = asset[:strings.IndexByte(asset, '"')]
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", asset, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: want 200, got %d", asset, w.Code)
	}
}
```

- [ ] **Step 7: Verify the spa package and full suite**

Run: `go test ./internal/spa/...`
Expected: PASS (proves `internal/spa/dist` is embedded and served, including deep-route fallback and a real asset).
Run: `go build ./... && go vet ./... && go test ./...`
Expected: still green (root package untouched).

- [ ] **Step 8: Commit**

```bash
git add internal/spa ui/vite.config.js .gitignore .dockerignore
git commit -m "refactor: move SPA embed into internal/spa and retarget UI build output"
```

---

### Task 3: Create `internal/controller`

Create the scheduler package. The root's `controller.go` is untouched (still compiles); the new package is self-contained.

**Files:**
- Create: `internal/controller/controller.go`
- Create: `internal/controller/controller_test.go`

**Interfaces:**
- Consumes: `store.Store`, `runstore.RunStore`, `runner.ParseCommand`, `runner.Run`, `runlog.FormatRun`, `model.*` (all from Tasks 1-2).
- Produces: `controller.NewController(store store.Store, runStore runstore.RunStore, curlPath string) (*Controller, error)`; methods `Start()`, `Stop() <-chan struct{}`, `List() []model.Job`, `Get(id string) (model.Job, error)`, `Create(start model.Job) (model.Job, error)`, `Update(id string, next model.Job) (model.Job, error)`, `Delete(id string) error`, `Enable(id string) error`, `Disable(id string) error`, `Run(id string) ([]model.Result, error)`, `History(id string) ([]model.Run, error)`, `LastRun(id string) *model.RunSummary`.

- [ ] **Step 1: Create `internal/controller/controller.go`** (moved from `controller.go`, package `controller`; replace `Store`→`store.Store`, `RunStore`→`runstore.RunStore`, `Job`→`model.Job`, `Result`→`model.Result`, `Run`→`model.Run`, `RunSummary`→`model.RunSummary`, `ErrNotFound`/`ErrValidation`/`ErrStorage`→`model.*`, `ErrCommandFailed`→`model.ErrCommandFailed`, `runJob`→`runner.Run`, `ParseCommand`→`runner.ParseCommand`, `formatRun`→`runlog.FormatRun`)

```go
package controller

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

	"cronix/internal/model"
	"cronix/internal/runlog"
	"cronix/internal/runstore"
	"cronix/internal/runner"
	"cronix/internal/store"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

const runHistoryCap = 50

func wrapValidation(format string, a ...any) error {
	return fmt.Errorf("%w: %s", model.ErrValidation, fmt.Sprintf(format, a...))
}

type Controller struct {
	mu       sync.Mutex
	runMu    sync.Mutex
	store    store.Store
	runStore runstore.RunStore
	cron     *cron.Cron
	jobs     map[string]model.Job
	order    []string
	entries  map[string]cron.EntryID
	runs     map[string][]model.Run
	curlPath string
}

func NewController(store store.Store, runStore runstore.RunStore, curlPath string) (*Controller, error) {
	c := &Controller{
		store:    store,
		runStore: runStore,
		cron:     cron.New(),
		jobs:     map[string]model.Job{},
		entries:  map[string]cron.EntryID{},
		runs:     map[string][]model.Run{},
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

func (c *Controller) Start() { c.cron.Start() }

// Stop stops the scheduler and returns a channel that closes once running
// jobs have completed. robfig/cron v3.0.1 exposes Stop() as context.Context,
// so bridge it to the <-chan struct{} contract.
func (c *Controller) Stop() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		<-c.cron.Stop().Done()
		close(done)
	}()
	return done
}

func (c *Controller) List() []model.Job {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]model.Job, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.jobs[id])
	}
	return out
}

func (c *Controller) Get(id string) (model.Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	j, ok := c.jobs[id]
	if !ok {
		return model.Job{}, fmt.Errorf("%w: %s", model.ErrNotFound, id)
	}
	return j, nil
}

func (c *Controller) Create(start model.Job) (model.Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if start.Id == "" {
		start.Id = genID()
	}
	if _, exists := c.jobs[start.Id]; exists {
		return model.Job{}, wrapValidation("a job with id %q already exists", start.Id)
	}
	if err := c.validate(start); err != nil {
		return model.Job{}, err
	}
	c.jobs[start.Id] = start
	c.order = append(c.order, start.Id)
	if err := c.register(start); err != nil {
		delete(c.jobs, start.Id)
		c.removeOrder(start.Id)
		return model.Job{}, err
	}
	if err := c.saveLocked(); err != nil {
		delete(c.jobs, start.Id)
		c.removeOrder(start.Id)
		c.unregister(start.Id)
		return model.Job{}, err
	}
	log.Printf("job=%s added", start.Id)
	return start, nil
}

func (c *Controller) Update(id string, next model.Job) (model.Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.jobs[id]
	if !ok {
		return model.Job{}, fmt.Errorf("%w: %s", model.ErrNotFound, id)
	}
	next.Id = cur.Id
	if err := c.validate(next); err != nil {
		return model.Job{}, err
	}
	prevEnabled := cur.Enabled
	prevSchedule := cur.Schedule
	c.jobs[id] = next
	if prevEnabled != next.Enabled || prevSchedule != next.Schedule {
		c.unregister(id)
		if next.Enabled {
			if err := c.register(next); err != nil {
				c.jobs[id] = cur
				return model.Job{}, err
			}
		}
	}
	if err := c.saveLocked(); err != nil {
		// roll back to the original job and its cron registration
		c.jobs[id] = cur
		c.unregister(id)
		if cur.Enabled {
			if err := c.register(cur); err != nil {
				log.Printf("rollback: re-register %s: %v", id, err)
			}
		}
		return model.Job{}, err
	}
	log.Printf("job=%s updated", id)
	return next, nil
}

func (c *Controller) Delete(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.jobs[id]
	if !ok {
		return fmt.Errorf("%w: %s", model.ErrNotFound, id)
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
	delete(c.runs, id)
	if err := c.runStore.SaveRuns(c.runsSnapshot()); err != nil {
		log.Printf("run history save: %v", err)
	}
	log.Printf("job=%s deleted", id)
	return nil
}

func (c *Controller) Enable(id string) error  { return c.setEnabled(id, true) }
func (c *Controller) Disable(id string) error { return c.setEnabled(id, false) }

func (c *Controller) recordRun(job model.Job, trigger string, results []model.Result, runErr error) {
	status := "ok"
	switch {
	case runErr == nil:
	case errors.Is(runErr, model.ErrCommandFailed):
		status = "failed"
	default:
		status = "error"
	}
	exit := 0
	if n := len(results); n > 0 {
		exit = results[n-1].ExitCode
	}
	run := model.Run{
		JobId:    job.Id,
		Trigger:  trigger,
		Time:     time.Now().UTC(),
		Status:   status,
		ExitCode: exit,
		Results:  results,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.jobs[job.Id]; !ok {
		return
	}
	c.runs[job.Id] = append([]model.Run{run}, c.runs[job.Id]...)
	if len(c.runs[job.Id]) > runHistoryCap {
		c.runs[job.Id] = c.runs[job.Id][:runHistoryCap]
	}
	if err := c.runStore.SaveRuns(c.runsSnapshot()); err != nil {
		log.Printf("run history save: %v", err)
	}
}

func (c *Controller) History(id string) ([]model.Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.jobs[id]; !ok {
		return nil, fmt.Errorf("%w: %s", model.ErrNotFound, id)
	}
	out := make([]model.Run, len(c.runs[id]))
	copy(out, c.runs[id])
	return out, nil
}

func (c *Controller) LastRun(id string) *model.RunSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	runs := c.runs[id]
	if len(runs) == 0 {
		return nil
	}
	first := runs[0]
	return &model.RunSummary{Status: first.Status, ExitCode: first.ExitCode, Time: first.Time}
}

func (c *Controller) runsSnapshot() []model.Run {
	out := make([]model.Run, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.runs[id]...)
	}
	return out
}

func (c *Controller) setEnabled(id string, enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.jobs[id]
	if !ok {
		return fmt.Errorf("%w: %s", model.ErrNotFound, id)
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

func (c *Controller) Run(id string) ([]model.Result, error) {
	job, ok := c.jobByID(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", model.ErrNotFound, id)
	}
	c.runMu.Lock()
	defer c.runMu.Unlock()
	results, err := runner.Run(job, c.curlPath)
	c.recordRun(job, "manual", results, err)
	return results, err
}

func (c *Controller) validate(j model.Job) error {
	if !idPattern.MatchString(j.Id) {
		return wrapValidation("id must match %s", idPattern.String())
	}
	if _, err := cron.ParseStandard(j.Schedule); err != nil {
		return wrapValidation("schedule %q: %v", j.Schedule, err)
	}
	if j.Curl == "" {
		return wrapValidation("curl: command is required")
	}
	if _, err := runner.ParseCommand(j.Curl); err != nil {
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

func (c *Controller) register(job model.Job) error {
	if !job.Enabled {
		return nil
	}
	entryID, err := c.cron.AddFunc(job.Schedule, func() {
		c.fire(job.Id)
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
		return fmt.Errorf("%w: %v", model.ErrStorage, err)
	}
	return nil
}

func (c *Controller) snapshot() []model.Job {
	out := make([]model.Job, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.jobs[id])
	}
	return out
}

func (c *Controller) fire(id string) {
	job, ok := c.jobByID(id)
	if !ok {
		return
	}
	c.runMu.Lock()
	defer c.runMu.Unlock()
	results, err := runner.Run(job, c.curlPath)
	c.recordRun(job, "scheduled", results, err)
	if err != nil {
		log.Printf("%s", runlog.FormatRun(job, results, err))
		return
	}
	for _, r := range results {
		log.Printf("job=%s name=%s schedule=%q attempt=%d/%d exit=%d output=%s",
			job.Id, job.Name, job.Schedule, r.Attempt, r.Total, r.ExitCode, strings.TrimSpace(r.Output))
	}
}

// jobByID copies the live job under the controller mutex. It is used by both
// the manual run path and scheduled fires so the job's current curl/retries are
// always read at run time.
func (c *Controller) jobByID(id string) (model.Job, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	job, ok := c.jobs[id]
	return job, ok
}

func genID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("job%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
```

- [ ] **Step 2: Create `internal/controller/controller_test.go`** (moved from `controller_test.go`, package `controller`; fakes implement `store.Store`/`runstore.RunStore`; types `Job`→`model.Job`, `Run`→`model.Run`, `Result`→`model.Result`, `ErrNotFound`/`ErrValidation`/`ErrStorage`/`ErrCommandFailed`→`model.*`; `newTestController(t, store Store)`→`store store.Store`)

```go
package controller

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"cronix/internal/model"
	"cronix/internal/runstore"
	"cronix/internal/store"
)

type memStore struct {
	jobs     []model.Job
	loadErr  error
	numSaves int
}

func (m *memStore) Load() ([]model.Job, error) { return m.jobs, m.loadErr }
func (m *memStore) Save(j []model.Job) error   { m.jobs = j; m.numSaves++; return nil }

type failingStore struct{ memStore }

func (f *failingStore) Save([]model.Job) error { return errors.New("disk full") }

type memRunStore struct {
	runs     []model.Run
	loadErr  error
	saveErr  error
	numSaves int
}

func (m *memRunStore) LoadRuns() ([]model.Run, error) { return m.runs, m.loadErr }
func (m *memRunStore) SaveRuns(r []model.Run) error {
	m.runs = r
	m.numSaves++
	if m.saveErr != nil {
		return m.saveErr
	}
	return nil
}

var _ runstore.RunStore = (*memRunStore)(nil)

func newTestController(t *testing.T, store store.Store) *Controller {
	t.Helper()
	c, err := NewController(store, &memRunStore{}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}

func TestNewControllerLoadsStoredJobs(t *testing.T) {
	c := newTestController(t, &memStore{jobs: []model.Job{
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
	_, err := NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "not-cron", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{}, "/bin/true")
	if err == nil {
		t.Fatal("want error for invalid stored job")
	}
	if !strings.Contains(err.Error(), "a") {
		t.Errorf("error should name job id %q: %v", "a", err)
	}
}

func TestCreateGeneratesIdWhenEmpty(t *testing.T) {
	c := newTestController(t, &memStore{})
	j, err := c.Create(model.Job{Schedule: "* * * * *", Curl: "curl http://x", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if j.Id == "" {
		t.Fatal("id should be generated")
	}
	if !j.Enabled {
		t.Error("enabled should be preserved as true")
	}
	if _, err := c.Get(j.Id); err != nil {
		t.Errorf("created job should be gettable: %v", err)
	}
}

func TestCreateRejectsDuplicateId(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://y"})
	if !errors.Is(err, model.ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestCreateValidatesSchedule(t *testing.T) {
	c := newTestController(t, &memStore{})
	_, err := c.Create(model.Job{Id: "a", Schedule: "nope", Curl: "curl http://x"})
	if !errors.Is(err, model.ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestCreateValidatesCurlCommand(t *testing.T) {
	c := newTestController(t, &memStore{})
	_, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "wget http://x"})
	if !errors.Is(err, model.ErrValidation) {
		t.Fatalf("want ErrValidation for non-curl command, got %v", err)
	}
}

func TestUpdateReplacesFieldsAndReconciles(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := c.Update("a", model.Job{Schedule: "0 0 * * *", Curl: "curl http://y", Enabled: false})
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
	if _, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := c.Update("a", model.Job{Id: "other", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Id != "a" {
		t.Errorf("id should stay %q, got %q", "a", got.Id)
	}
}

func TestUpdateMissingJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	_, err := c.Update("nope", model.Job{Schedule: "* * * * *", Curl: "curl http://x", Enabled: true})
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteRemovesJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(c.List()) != 0 {
		t.Errorf("job should be gone, got %d", len(c.List()))
	}
	if _, err := c.Get("a"); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("want ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteMissingJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	if err := c.Delete("nope"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestEnableAndDisableToggle(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: false}); err != nil {
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
	_, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"})
	if !errors.Is(err, model.ErrStorage) {
		t.Fatalf("want ErrStorage, got %v", err)
	}
	if len(c.List()) != 0 {
		t.Errorf("in-memory list should be rolled back, got %d", len(c.List()))
	}
	if _, ok := c.entries["a"]; ok {
		t.Error("cron entry should be rolled back")
	}
}

func TestUpdateRollsBackOnSaveFailure(t *testing.T) {
	c := newTestController(t, &failingStore{memStore{jobs: []model.Job{
		{Id: "a", Schedule: "*/5 * * * *", Curl: "curl http://x", Enabled: true},
	}}})
	if _, ok := c.entries["a"]; !ok {
		t.Fatal("seeded enabled job should be registered with cron")
	}
	_, err := c.Update("a", model.Job{Schedule: "0 0 * * *", Curl: "curl http://y", Enabled: false})
	if !errors.Is(err, model.ErrStorage) {
		t.Fatalf("want ErrStorage, got %v", err)
	}
	got, err := c.Get("a")
	if err != nil {
		t.Fatalf("job should remain gettable after rollback: %v", err)
	}
	if got.Schedule != "*/5 * * * *" || got.Curl != "curl http://x" || !got.Enabled {
		t.Errorf("in-memory job should match original, got %+v", got)
	}
	if _, ok := c.entries["a"]; !ok {
		t.Error("cron entry should match original enabled job after rollback")
	}
}

func TestListPreservesOrder(t *testing.T) {
	c := newTestController(t, &memStore{})
	for _, id := range []string{"c", "a", "b"} {
		if _, err := c.Create(model.Job{Id: id, Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
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
				_, _ = c.Create(model.Job{Id: id, Schedule: "* * * * *", Curl: "curl http://x"})
			case 1:
				_, _ = c.Update(id, model.Job{Schedule: "*/5 * * * *", Curl: "curl http://y", Enabled: true})
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

// TestScheduledFireUsesLiveCurlAfterUpdate verifies that a fire reads the LIVE
// job from the controller at run time: updating ONLY curl (leaving schedule and
// enabled unchanged, so the cron entry is untouched) must change what a
// scheduled fire executes.
func TestScheduledFireUsesLiveCurlAfterUpdate(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	// /usr/bin/env acts as the "curl" binary: it execs its first argument, so
	// "curl true" exits 0 and "curl false" exits non-zero.
	c, err := NewController(&memStore{}, &memRunStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	if _, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := c.Update("a", model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl false", Enabled: true}); err != nil {
		t.Fatalf("update: %v", err)
	}
	entryID, ok := c.entries["a"]
	if !ok {
		t.Fatal("enabled job must stay registered after a curl-only update")
	}
	logs.Reset()
	c.cron.Entry(entryID).Job.Run()
	got := logs.String()
	if !strings.Contains(got, "attempt 1/1 exit=1") {
		t.Fatalf("scheduled fire did not run the live curl; logs:\n%s", got)
	}
	if !strings.Contains(got, "result: FAILED") {
		t.Errorf("failed scheduled fire should log the run block with result: FAILED:\n%s", got)
	}
}

// TestConcurrentRunsDoNotInterleave proves that two manual runs of the same job
// are serialized (per the design spec) rather than executing in parallel.
func TestConcurrentRunsDoNotInterleave(t *testing.T) {
	// /bin/sleep sleeps for its first argument, so each run takes ~600ms.
	c, err := NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl 0.6", Enabled: true},
	}}, &memRunStore{}, "/bin/sleep")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Run("a"); err != nil {
				t.Errorf("run: %v", err)
			}
		}()
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed < 1100*time.Millisecond {
		t.Errorf("two runs of a 600ms job must not interleave; want serialized ~1.2s, got %v", elapsed)
	}
}

func TestRunRecordsManualHistory(t *testing.T) {
	c, err := NewController(&memStore{jobs: []model.Job{
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
	c, err := NewController(&memStore{jobs: []model.Job{
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
	c := newTestController(t, &memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}})
	c.recordRun(model.Job{Id: "a"}, "manual", []model.Result{{Attempt: 1, Total: 1, ExitCode: 0}}, nil)
	c.recordRun(model.Job{Id: "a"}, "manual", []model.Result{{Attempt: 1, Total: 1, ExitCode: 6}},
		fmt.Errorf("%w: job a failed", model.ErrCommandFailed))
	c.recordRun(model.Job{Id: "a"}, "manual", nil, errors.New("cannot start /bin/true"))
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
	c := newTestController(t, &memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}})
	for i := 0; i < runHistoryCap+10; i++ {
		c.recordRun(model.Job{Id: "a"}, "manual", []model.Result{{Attempt: 1, Total: 1, ExitCode: 0}}, nil)
	}
	runs, _ := c.History("a")
	if len(runs) != runHistoryCap {
		t.Fatalf("want %d runs, got %d", runHistoryCap, len(runs))
	}
}

func TestRecordRunSaveFailureDoesNotFail(t *testing.T) {
	c, err := NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{saveErr: errors.New("disk full")}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	c.recordRun(model.Job{Id: "a"}, "manual", []model.Result{{Attempt: 1, Total: 1, ExitCode: 0}}, nil)
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
	if _, err := c.History("nope"); !errors.Is(err, model.ErrNotFound) {
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
	c, err := NewController(&memStore{jobs: []model.Job{
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

func TestRecordRunSkipsDeletedJob(t *testing.T) {
	c, err := NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	if err := c.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	c.recordRun(model.Job{Id: "a"}, "scheduled",
		[]model.Result{{Attempt: 1, Total: 1, ExitCode: 0, Output: "late fire"}}, nil)
	if ls := c.LastRun("a"); ls != nil {
		t.Errorf("a fire racing a delete must not retain a run: %+v", ls)
	}
}

func TestNewControllerLoadsAndPrunesRunHistory(t *testing.T) {
	store := &memRunStore{runs: []model.Run{
		{JobId: "a", Trigger: "manual", Time: time.Now().UTC(), Status: "ok", ExitCode: 0},
		{JobId: "ghost", Trigger: "manual", Time: time.Now().UTC(), Status: "ok", ExitCode: 0},
	}}
	c, err := NewController(&memStore{jobs: []model.Job{
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

- [ ] **Step 3: Verify the controller package and full suite**

Run: `go test ./internal/controller/...`
Expected: PASS.
Run: `go build ./... && go vet ./... && go test ./...`
Expected: still green (root package untouched).

- [ ] **Step 4: Commit**

```bash
git add internal/controller
git commit -m "refactor: extract scheduler into internal/controller"
```

---

### Task 4: Create `internal/api`

Create the HTTP server package. The root's `api.go`/`auth.go`/`spa.go` are untouched (still compile).

**Files:**
- Create: `internal/api/api.go`
- Create: `internal/api/api_test.go`

**Interfaces:**
- Consumes: `controller.Controller`, `controller.NewController`, `auth.IssueSessionToken`/`auth.ValidateSessionToken`, `spa.SPAHandler`, `model.*` (Tasks 1-3).
- Produces: `api.AuthConfig{Token, Username, Password string; SessionTTL time.Duration}`, `api.NewServer(ctrl *controller.Controller, cfg AuthConfig) http.Handler`.

- [ ] **Step 1: Create `internal/api/api.go`** (moved from `api.go`, package `api`; `Job`→`model.Job`, `Run`→`model.Run`, `Result`→`model.Result`, `RunSummary`→`model.RunSummary`, `ErrNotFound`/`ErrValidation`/`ErrStorage`/`ErrCommandFailed`→`model.*`, `issueSessionToken`→`auth.IssueSessionToken`, `validateSessionToken`→`auth.ValidateSessionToken`, `SPAHandler()`→`spa.SPAHandler()`, `formatRun`→`runlog.FormatRun`; `Server.ctrl` type → `*controller.Controller`; `NewServer` signature unchanged apart from the controller type)

```go
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"cronix/internal/auth"
	"cronix/internal/controller"
	"cronix/internal/model"
	"cronix/internal/runlog"
	"cronix/internal/spa"
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

func (r jobRequest) toJob() model.Job {
	j := model.Job{Enabled: true}
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

type AuthConfig struct {
	Token      string
	Username   string
	Password   string
	SessionTTL time.Duration
}

type Server struct {
	ctrl *controller.Controller
	cfg  AuthConfig
}

func NewServer(ctrl *controller.Controller, cfg AuthConfig) http.Handler {
	s := &Server{ctrl: ctrl, cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/login", s.handleLogin)
	mux.HandleFunc("GET /api/v1/jobs", s.auth(s.handleList))
	mux.HandleFunc("POST /api/v1/jobs", s.auth(s.handleCreate))
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.auth(s.handleGet))
	mux.HandleFunc("PUT /api/v1/jobs/{id}", s.auth(s.handleUpdate))
	mux.HandleFunc("DELETE /api/v1/jobs/{id}", s.auth(s.handleDelete))
	mux.HandleFunc("POST /api/v1/jobs/{id}/run", s.auth(s.handleRun))
	mux.HandleFunc("GET /api/v1/jobs/{id}/runs", s.auth(s.handleRuns))
	mux.Handle("GET /", spa.SPAHandler())
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
		if !s.validAuth(strings.TrimPrefix(h, prefix)) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (s *Server) validAuth(got string) bool {
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) == 1 {
		return true
	}
	_, ok := auth.ValidateSessionToken([]byte(s.cfg.Token), got)
	return ok
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var reqJSON struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqJSON); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: %v", err)
		return
	}
	if reqJSON.Username == "" || reqJSON.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	if subtle.ConstantTimeCompare([]byte(reqJSON.Username), []byte(s.cfg.Username)) != 1 ||
		subtle.ConstantTimeCompare([]byte(reqJSON.Password), []byte(s.cfg.Password)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, err := auth.IssueSessionToken([]byte(s.cfg.Token), reqJSON.Username, s.cfg.SessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%s", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

type jobResponse struct {
	model.Job
	LastRun *model.RunSummary `json:"last_run,omitempty"`
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	jobs := s.ctrl.List()
	out := make([]jobResponse, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobResponse{Job: j, LastRun: s.ctrl.LastRun(j.Id)})
	}
	writeJSON(w, http.StatusOK, out)
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
	id := r.PathValue("id")
	job, err := s.ctrl.Get(id)
	if err != nil {
		writeCtrlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobResponse{Job: job, LastRun: s.ctrl.LastRun(id)})
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
	id := r.PathValue("id")
	job, gerr := s.ctrl.Get(id)
	if gerr != nil {
		writeCtrlError(w, gerr)
		return
	}
	results, err := s.ctrl.Run(id)
	if err != nil && !errors.Is(err, model.ErrCommandFailed) {
		writeCtrlError(w, err)
		return
	}
	log.Printf("%s", runlog.FormatRun(job, results, err))
	writeJSON(w, http.StatusOK, struct {
		Steps []model.Result `json:"steps"`
	}{Steps: results})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.ctrl.History(r.PathValue("id"))
	if err != nil {
		writeCtrlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Runs []model.Run `json:"runs"`
	}{Runs: runs})
}

func writeCtrlError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, model.ErrNotFound):
		writeError(w, http.StatusNotFound, "%s", err.Error())
	case errors.Is(err, model.ErrValidation):
		writeError(w, http.StatusBadRequest, "%s", err.Error())
	case errors.Is(err, model.ErrStorage):
		writeError(w, http.StatusInternalServerError, "%s", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "%s", err.Error())
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

- [ ] **Step 2: Create `internal/api/api_test.go`** (moved from `api_test.go`, package `api`; fakes `memStore`/`memRunStore`/`failingStore` defined locally implementing `store.Store`/`runstore.RunStore`; `Job`→`model.Job`, `Run`→`model.Run`, `Result`→`model.Result`, `NewController`→`controller.NewController`, `issueSessionToken`→`auth.IssueSessionToken`; the `TestCLIListStillDecodesJobResponse` test moves to the cli package in Task 5)

```go
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"cronix/internal/auth"
	"cronix/internal/controller"
	"cronix/internal/model"
	"cronix/internal/runstore"
	"cronix/internal/store"
)

type memStore struct {
	jobs     []model.Job
	loadErr  error
	numSaves int
}

func (m *memStore) Load() ([]model.Job, error) { return m.jobs, m.loadErr }
func (m *memStore) Save(j []model.Job) error   { m.jobs = j; m.numSaves++; return nil }

type failingStore struct{ memStore }

func (f *failingStore) Save([]model.Job) error { return errors.New("disk full") }

type memRunStore struct {
	runs     []model.Run
	loadErr  error
	saveErr  error
	numSaves int
}

func (m *memRunStore) LoadRuns() ([]model.Run, error) { return m.runs, m.loadErr }
func (m *memRunStore) SaveRuns(r []model.Run) error {
	m.runs = r
	m.numSaves++
	if m.saveErr != nil {
		return m.saveErr
	}
	return nil
}

var _ runstore.RunStore = (*memRunStore)(nil)
var _ store.Store = (*memStore)(nil)

func newTestServer(t *testing.T, store store.Store) http.Handler {
	t.Helper()
	c, err := controller.NewController(store, &memRunStore{}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return NewServer(c, testAuth("secret"))
}

func testAuth(token string) AuthConfig {
	return AuthConfig{Token: token, Username: "admin", Password: "admin", SessionTTL: time.Hour}
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
	var created model.Job
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
	var jobs []model.Job
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
	var j model.Job
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
	c, err := controller.NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, testAuth("secret"))
	w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("run: want 200, got %d: %s", w.Code, b)
	}
	var resp struct {
		Steps []model.Result `json:"steps"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Steps) == 0 {
		t.Fatal("want at least one run step")
	}
}

func TestAPIRunExhaustedRetriesStillReturns200WithSteps(t *testing.T) {
	c, err := controller.NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Retries: 1, RetryDelay: 0, Enabled: true},
	}}, &memRunStore{}, "/usr/bin/false")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, testAuth("secret"))
	w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("run with exhausted retries: want 200 with completed steps, got %d: %s", w.Code, b)
	}
	var resp struct {
		Steps []model.Result `json:"steps"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Steps) != 2 {
		t.Fatalf("retries=1 should produce 2 steps, got %d: %+v", len(resp.Steps), resp.Steps)
	}
	last := resp.Steps[len(resp.Steps)-1]
	if last.ExitCode == 0 {
		t.Errorf("final step should have non-zero exit, got %+v", last)
	}
}

func TestAPIRunStartupFailureReturns500(t *testing.T) {
	c, err := controller.NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{}, "/nonexistent/curl")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, testAuth("secret"))
	w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("cannot-start-binary must stay a 500, got %d: %s", w.Code, b)
	}
}

func TestAPIRunManualLogsBlockOnSuccess(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	c, err := controller.NewController(&memStore{jobs: []model.Job{
		{Id: "a", Name: "ping", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, testAuth("secret"))
	w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("run: want 200, got %d: %s", w.Code, b)
	}
	got := logs.String()
	if !strings.Contains(got, "run job=a") || !strings.Contains(got, "result: OK") {
		t.Errorf("manual run should log the block:\n%s", got)
	}
}

func TestAPIRunManualLogsBlockOnFailure(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	c, err := controller.NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Retries: 1, RetryDelay: 0, Enabled: true},
	}}, &memRunStore{}, "/usr/bin/false")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, testAuth("secret"))
	w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("run with exhausted retries: want 200, got %d: %s", w.Code, b)
	}
	got := logs.String()
	if !strings.Contains(got, "run job=a") || !strings.Contains(got, "result: FAILED") || !strings.Contains(got, "2/2 attempts") {
		t.Errorf("manual run failure should log the block with FAILED:\n%s", got)
	}
}

func TestAPIRunsEndpoint(t *testing.T) {
	c, err := controller.NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, testAuth("secret"))
	w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("run: %d: %s", w.Code, b)
	}
	w, b = req(t, h, "GET", "/api/v1/jobs/a/runs", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("runs: want 200, got %d: %s", w.Code, b)
	}
	var resp struct {
		Runs []model.Run `json:"runs"`
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
	c, err := controller.NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true},
		{Id: "b", Schedule: "* * * * *", Curl: "curl true", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, testAuth("secret"))
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
	c, err := controller.NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, testAuth("secret"))
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

func TestAPILoginSuccessIssuesUsableToken(t *testing.T) {
	h := newTestServer(t, &memStore{})
	w, b := req(t, h, "POST", "/api/v1/login", "",
		map[string]any{"username": "admin", "password": "admin"})
	if w.Code != http.StatusOK {
		t.Fatalf("login: want 200, got %d: %s", w.Code, b)
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("login response missing token")
	}
	w, b = req(t, h, "GET", "/api/v1/jobs", resp.Token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list with session token: want 200, got %d: %s", w.Code, b)
	}
}

func TestAPILoginRejectsBadCredentials(t *testing.T) {
	h := newTestServer(t, &memStore{})
	for _, body := range []map[string]any{
		{"username": "admin", "password": "wrong"},
		{"username": "wrong", "password": "admin"},
		{"username": "wrong", "password": "wrong"},
	} {
		w, _ := req(t, h, "POST", "/api/v1/login", "", body)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%v: want 401, got %d", body, w.Code)
		}
	}
}

func TestAPILoginRejectsMalformedBody(t *testing.T) {
	h := newTestServer(t, &memStore{})
	w, _ := req(t, h, "POST", "/api/v1/login", "", map[string]any{"username": "admin"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing password: want 400, got %d", w.Code)
	}
	w, _ = req(t, h, "POST", "/api/v1/login", "", "garbage")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("wrong typed body: want 400, got %d", w.Code)
	}
}

func TestAPISessionTokenAuthorized(t *testing.T) {
	c, err := controller.NewController(&memStore{}, &memRunStore{}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, testAuth("secret"))
	token, err := auth.IssueSessionToken([]byte("secret"), "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	w, _ := req(t, h, "GET", "/api/v1/jobs", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("session token: want 200, got %d", w.Code)
	}
}

func TestAPIExpiredSessionTokenRejected(t *testing.T) {
	h := newTestServer(t, &memStore{})
	token, err := auth.IssueSessionToken([]byte("secret"), "admin", -time.Second)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	w, _ := req(t, h, "GET", "/api/v1/jobs", token, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expired session: want 401, got %d", w.Code)
	}
}
```

- [ ] **Step 3: Verify the api package and full suite**

Run: `go test ./internal/api/...`
Expected: PASS.
Run: `go build ./... && go vet ./... && go test ./...`
Expected: still green (root package untouched).

- [ ] **Step 4: Commit**

```bash
git add internal/api
git commit -m "refactor: extract HTTP API into internal/api"
```

---

### Task 5: Create `internal/cli`

Create the CLI client package. The root's `cli.go` is untouched (still compiles).

**Files:**
- Create: `internal/cli/cli.go`
- Create: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: `model.Job`, `model.Result`, `model.ErrCommandFailed`, `runlog.FormatRun` (Tasks 1-3).
- Produces: `cli.RunCLI(args []string) int`, plus unexported `runCLIIn(args []string, out io.Writer) int` (used by its own tests).

- [ ] **Step 1: Create `internal/cli/cli.go`** (moved from `cli.go`, package `cli`; `Job`→`model.Job`, `Result`→`model.Result`, `formatRun`→`runlog.FormatRun`, `ErrCommandFailed`→`model.ErrCommandFailed`)

```go
package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"

	"cronix/internal/model"
	"cronix/internal/runlog"
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
	var jobs []model.Job
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
		fmt.Fprintln(out, "usage: cronix cli get [--addr URL] [--token TOKEN] <id>")
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
	var j model.Job
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
		fmt.Fprintln(out, "usage: cronix cli update [--name N] [--schedule S] [--curl C] [--retries N] [--retry-delay S] [--enabled B] <id>")
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
		fmt.Fprintln(out, "usage: cronix cli delete [--addr URL] [--token TOKEN] <id>")
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
		fmt.Fprintf(out, "usage: cronix cli %s [--addr URL] [--token TOKEN] <id>\n", cmd)
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

func cliGetJob(opts cliOpts, id string) (model.Job, error) {
	status, body, err := cliHTTP("GET", opts.addr+"/api/v1/jobs/"+id, opts.token, nil)
	if err != nil {
		return model.Job{}, err
	}
	if status != http.StatusOK {
		return model.Job{}, fmt.Errorf("cannot fetch job %s (status %d)", id, status)
	}
	var j model.Job
	if err := json.Unmarshal(body, &j); err != nil {
		return model.Job{}, fmt.Errorf("cannot decode job: %w", err)
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
		fmt.Fprintln(out, "usage: cronix cli run [--addr URL] [--token TOKEN] <id>")
		return 1
	}
	id := fs.Arg(0)
	job, err := cliGetJob(opts, id)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	status, resp, err := cliHTTP("POST", opts.addr+"/api/v1/jobs/"+id+"/run", opts.token, nil)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusOK {
		return cliErr(out, status, resp)
	}
	var runResp struct {
		Steps []model.Result `json:"steps"`
	}
	if err := json.Unmarshal(resp, &runResp); err != nil {
		fmt.Fprintf(out, "error: cannot decode run: %v\n", err)
		return 2
	}
	var runErr error
	if n := len(runResp.Steps); n > 0 && runResp.Steps[n-1].ExitCode != 0 {
		runErr = model.ErrCommandFailed
	}
	fmt.Fprintln(out, runlog.FormatRun(job, runResp.Steps, runErr))
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
Flags must precede the positional <id> (Go's flag package stops parsing at the
first non-flag argument), e.g. "get --addr URL --token TOKEN <id>".

Commands:
  list                              list jobs
  get [--addr URL] [--token TOKEN] <id>
                                    show one job
  add ...                           create a job
  update [--name N] [--schedule S] [--curl C] [--retries N] [--retry-delay S] [--enabled B] <id>
                                    replace a job
  delete [--addr URL] [--token TOKEN] <id>
                                    delete a job
  enable [--addr URL] [--token TOKEN] <id>
                                    enable a paused job
  disable [--addr URL] [--token TOKEN] <id>
                                    disable a job
  run [--addr URL] [--token TOKEN] <id>
                                    run a job now

Common flags: --addr URL (default %s)  --token TOKEN (default $CRONIX_API_TOKEN)
add/update flags: --id --name --schedule --curl --retries --retry-delay --enabled
`, defaultCLIAddr)
}
```

- [ ] **Step 2: Create `internal/cli/cli_test.go`** (moved from `cli_test.go` + the `TestCLIListStillDecodesJobResponse` test from `api_test.go`, package `cli`; local fakes + a helper that builds a real `api.NewServer` over a `controller.NewController` with in-memory stores; `Job`→`model.Job`)

```go
package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cronix/internal/api"
	"cronix/internal/controller"
	"cronix/internal/model"
	"cronix/internal/runstore"
	"cronix/internal/store"
)

type memStore struct {
	jobs []model.Job
}

func (m *memStore) Load() ([]model.Job, error) { return m.jobs, nil }
func (m *memStore) Save(j []model.Job) error   { m.jobs = j; return nil }

type memRunStore struct{}

func (m *memRunStore) LoadRuns() ([]model.Run, error) { return nil, nil }
func (m *memRunStore) SaveRuns([]model.Run) error     { return nil }

var _ store.Store = (*memStore)(nil)
var _ runstore.RunStore = (*memRunStore)(nil)

func withCLIServer(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func cliTestServer(t *testing.T, jobs []model.Job) string {
	t.Helper()
	c, err := controller.NewController(&memStore{jobs: jobs}, &memRunStore{}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	cfg := api.AuthConfig{Token: "tok", Username: "admin", Password: "admin", SessionTTL: time.Hour}
	return withCLIServer(t, api.NewServer(c, cfg))
}

func TestRunCLC_listAndAddFlow(t *testing.T) {
	addr := cliTestServer(t, nil)
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
	addr := cliTestServer(t, nil)
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

func TestRunCLIUsageListsFlagsBeforeID(t *testing.T) {
	out := new(bytes.Buffer)
	if code := runCLIIn([]string{"help"}, out); code != 0 {
		t.Fatalf("help: want exit 0, got %d", code)
	}
	usage := out.String()
	for _, want := range []string{
		"get [--addr",
		"update [--name",
		"delete [--addr",
		"enable [--addr",
		"disable [--addr",
		"run [--addr",
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage must list flags before <id> (Go flag stops at the first positional arg), missing %q:\n%s", want, usage)
		}
	}
}

func TestRunCLIRunPrintsBlock(t *testing.T) {
	addr := cliTestServer(t, []model.Job{
		{Id: "a", Name: "ping", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	})
	out := new(bytes.Buffer)
	code := runCLIIn([]string{"run", "--addr", addr, "--token", "tok", "a"}, out)
	if code != 0 {
		t.Fatalf("run: want exit 0, got %d: %s", code, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "run job=a") || !strings.Contains(got, "name=ping") {
		t.Errorf("run output should include job context:\n%s", got)
	}
	if !strings.Contains(got, "result: OK") {
		t.Errorf("run output should include result: OK:\n%s", got)
	}
}

func TestRunCLIRunFailedPrintsFailedBlock(t *testing.T) {
	c, err := controller.NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/false")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	cfg := api.AuthConfig{Token: "tok", Username: "admin", Password: "admin", SessionTTL: time.Hour}
	addr := withCLIServer(t, api.NewServer(c, cfg))
	out := new(bytes.Buffer)
	code := runCLIIn([]string{"run", "--addr", addr, "--token", "tok", "a"}, out)
	if code != 0 {
		t.Fatalf("run: want exit 0 (domain failure), got %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "result: FAILED") {
		t.Errorf("failed run output should include result: FAILED:\n%s", out.String())
	}
}

func TestRunCLIGetIdFirstWithFlagsPrintsFlagFirstUsage(t *testing.T) {
	out := new(bytes.Buffer)
	// `get <id> --token X` hits Go's flag parser which stops at the positional
	// arg; the resulting usage text must honestly show flags before <id>.
	code := runCLIIn([]string{"get", "someid", "--token", "x"}, out)
	if code != 1 {
		t.Fatalf("want exit 1 for id-before-flags, got %d", code)
	}
	if got := out.String(); !strings.Contains(got, "usage: cronix cli get [--addr") {
		t.Errorf("usage should be flag-first, got: %s", got)
	}
}

func TestCLIListStillDecodesJobResponse(t *testing.T) {
	c, err := controller.NewController(&memStore{jobs: []model.Job{
		{Id: "a", Name: "ping", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	cfg := api.AuthConfig{Token: "tok", Username: "admin", Password: "admin", SessionTTL: time.Hour}
	addr := withCLIServer(t, api.NewServer(c, cfg))
	out := new(bytes.Buffer)
	if code := runCLIIn([]string{"list", "--addr", addr, "--token", "tok"}, out); code != 0 {
		t.Fatalf("list: exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "ping") {
		t.Errorf("list output broken by last_run field: %s", out.String())
	}
}
```

- [ ] **Step 3: Verify the cli package and full suite**

Run: `go test ./internal/cli/...`
Expected: PASS.
Run: `go build ./... && go vet ./... && go test ./...`
Expected: still green (root package untouched).

- [ ] **Step 4: Commit**

```bash
git add internal/cli
git commit -m "refactor: extract CLI client into internal/cli"
```

---

### Task 6: Create `cmd/cronix/main.go`

Create the new binary entrypoint that wires the packages together. The root `main.go` is untouched for now (still compiles — it is its own `package main` in the module root).

**Files:**
- Create: `cmd/cronix/main.go`

**Interfaces:**
- Consumes: `store.NewFileStore`, `runstore.NewFileRunStore`, `controller.NewController`, `api.NewServer`, `api.AuthConfig`, `cli.RunCLI` (Tasks 1-5).
- Produces: the `cronix` binary.

- [ ] **Step 1: Create `cmd/cronix/main.go`** (wiring only; behavior identical to the root `main.go`)

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

	"cronix/internal/api"
	"cronix/internal/cli"
	"cronix/internal/controller"
	"cronix/internal/runstore"
	"cronix/internal/store"
)

const (
	defaultStorePath = "/data/jobs.json"
	defaultRunsPath  = "/data/runs.json"
	defaultHTTPAddr  = ":8080"
	defaultCurlPath  = "/usr/local/bin/curl"
)

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "cli" {
		os.Exit(cli.RunCLI(os.Args[2:]))
	}

	token := os.Getenv("CRONIX_API_TOKEN")
	if strings.TrimSpace(token) == "" {
		log.Fatalf("CRONIX_API_TOKEN is required; refusing to start")
	}

	storePath := envOr("CRONIX_STORE_PATH", defaultStorePath)
	runsPath := envOr("CRONIX_RUNS_PATH", defaultRunsPath)
	httpAddr := envOr("CRONIX_HTTP_ADDR", defaultHTTPAddr)
	curlPath := envOr("CURL_PATH", defaultCurlPath)

	ctrl, err := controller.NewController(store.NewFileStore(storePath), runstore.NewFileRunStore(runsPath), curlPath)
	if err != nil {
		log.Fatalf("startup error: %v", err)
	}

	jobs := ctrl.List()
	log.Printf("cronix started with %d job(s)", len(jobs))
	if len(jobs) == 0 {
		log.Printf("no jobs configured; add one with '/cronix cli add' or the web UI at http://%s", httpAddr)
	}
	ctrl.Start()

	srv := &http.Server{
		Addr: httpAddr,
		Handler: api.NewServer(ctrl, api.AuthConfig{
			Token:      token,
			Username:   envOr("CRONIX_USERNAME", "admin"),
			Password:   envOr("CRONIX_PASSWORD", "admin"),
			SessionTTL: envDuration("CRONIX_SESSION_TTL", 24*time.Hour),
		}),
	}
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
	case <-done:
	case <-time.After(10 * time.Second):
		log.Printf("in-flight jobs did not finish in 10s; exiting")
	}
	log.Printf("bye")
}
```

- [ ] **Step 2: Verify the new binary builds and runs**

Run: `go build ./cmd/cronix && go vet ./cmd/cronix`
Expected: compiles clean.
Run: `go build ./... && go vet ./... && go test ./...`
Expected: still green.

- [ ] **Step 3: Commit**

```bash
git add cmd/
git commit -m "refactor: add cmd/cronix binary wiring"
```

---

### Task 7: Delete the root `package main` files, root `dist/`, and finish the build config

Remove all root-level Go files (now superseded by `internal/*` + `cmd/cronix`), remove the root `dist/` (superseded by `internal/spa/dist/`), and update the Makefile, Dockerfile, README, and `.dockerignore` to the new layout. This is the commit where the old flat layout disappears.

**Files:**
- Delete: `main.go`, `controller.go`, `controller_test.go`, `store.go`, `store_test.go`, `run_store.go`, `run_store_test.go`, `runner.go`, `runner_test.go`, `run_log.go`, `run_log_test.go`, `auth.go`, `auth_test.go`, `api.go`, `api_test.go`, `cli.go`, `cli_test.go`, `spa.go`, `spa_test.go`
- Delete (from disk only, not gitignored): `dist/`
- Modify: `Makefile`
- Modify: `Dockerfile`
- Modify: `.dockerignore`
- Modify: `README.md`

**Interfaces:**
- Consumes: all packages from Tasks 1-6.
- Produces: a repo whose only Go code lives under `cmd/` and `internal/`, with `make build`, `make test`, `make vet`, `make run`, and Docker all wired to the new binary path.

- [ ] **Step 1: Delete the root Go files and root `dist/`**

Run:
```bash
git rm main.go controller.go controller_test.go store.go store_test.go run_store.go run_store_test.go runner.go runner_test.go run_log.go run_log_test.go auth.go auth_test.go api.go api_test.go cli.go cli_test.go spa.go spa_test.go
rm -rf dist
```
Expected: `git status` shows the deletions staged and `dist/` gone from disk.

- [ ] **Step 2: Update the Makefile to build the new binary**

Replace `go build -o bin/$(BINARY) .` (build target) with:

```make
	go build -o bin/$(BINARY) ./cmd/cronix
```

(`test`, `vet`, `lint`, `run`, `ui` targets are unchanged.)

- [ ] **Step 3: Update the Dockerfile**

In the Go build stage, replace `RUN CGO_ENABLED=0 go build -o /cronix .` with:

```dockerfile
RUN CGO_ENABLED=0 go build -o /cronix ./cmd/cronix
```

And replace `COPY --from=web /dist ./dist` with:

```dockerfile
COPY --from=web /internal/spa/dist ./internal/spa/dist
```

- [ ] **Step 4: Update `.dockerignore`**

Replace the `dist` line with `internal/spa/dist` (keep `web`, `bin`, and the rest):

```gitignore
.git
docs
Dockerfile
.dockerignore
ui/node_modules
.superpowers
.data
internal/spa/dist
web
bin
```

- [ ] **Step 5: Update README references to the new layout**

- In the "Web UI" section, change `serving the built `dist/` directory` to `serving the built `internal/spa/dist/` directory`.
- Update the `go:embed` mention in the Web UI section to reference `internal/spa/spa.go` (was `spa.go`).
- Add a short "Project layout" note under the intro listing the top-level directories (`cmd/cronix`, `internal/*`, `ui/`), describing that the Go code lives under `cmd/` and `internal/` and the UI source under `ui/`.
- Remove any remaining references to root-level `dist/` or `web/` build outputs.

- [ ] **Step 6: Verify the full suite from a clean state**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS (all packages). Confirm no `package main` remains at the module root: `go list ./...` shows only `cronix/cmd/cronix` and `cronix/internal/*`.

Run: `gofmt -l .`
Expected: no output.

Run: `git status --short`
Expected: staged deletions only; `internal/spa/dist/` ignored.

- [ ] **Step 7: Commit**

```bash
git add Makefile Dockerfile .dockerignore README.md
git add -u
git commit -m "refactor: remove flat package-main layout, wire cmd/cronix build"
```

---

### Task 8: Verify the end-to-end gate

Final verification across UI, Go, git, and Docker.

**Files:**
- None (verification only).

- [ ] **Step 1: Rebuild everything from a clean state**

Run: `npm --prefix ui run build && go build ./... && go vet ./... && go test ./...`
Expected: UI emits into `internal/spa/dist/`; all packages build and all tests pass.

- [ ] **Step 2: Confirm formatting and repo state**

Run: `gofmt -l .`
Expected: no output.
Run: `git status --short`
Expected: no tracked changes; `internal/spa/dist/` absent (ignored).

- [ ] **Step 3: Confirm `make build` and `make test` work**

Run: `make build`
Expected: runs `npm ci` + `npm --prefix ui run build` (into `internal/spa/dist/`), then `go build -o bin/cronix ./cmd/cronix`. Exit 0.
Run: `make test`
Expected: PASS.

- [ ] **Step 4: Confirm the binary serves the SPA and deep routes**

Run:
```sh
go build -o /tmp/cronix-struct-test ./cmd/cronix
CRONIX_API_TOKEN=t CRONIX_STORE_PATH=/tmp/cj.json CRONIX_RUNS_PATH=/tmp/cr.json \
  CRONIX_HTTP_ADDR=127.0.0.1:18098 /tmp/cronix-struct-test &>/tmp/cronix.log &
sleep 1
for p in / /jobs /jobs/abc123; do
  echo "GET $p -> $(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18098$p)"
done
kill %1
```
Expected: `GET / -> 200`, `GET /jobs -> 200`, `GET /jobs/abc123 -> 200`.

- [ ] **Step 5: Confirm the Docker image builds and stays binary-only**

Run: `docker build -t cronix-struct-test .`
Expected: node stage builds UI into `/internal/spa/dist`; Go stage embeds it; final scratch stage copies only `/cronix`, curl, certs.

Run:
```sh
docker create --name cronix-struct-inspect cronix-struct-test && docker export cronix-struct-inspect | tar -tv 2>/dev/null | grep -v -E '^.* (cronix|bin/curl|etc/ssl/certs/)' || true; docker rm cronix-struct-inspect
```
Expected: only `/cronix`, `/usr/local/bin/curl`, `/etc/ssl/certs/...` entries.

Then: `docker rmi cronix-struct-test`

- [ ] **Step 6: Confirm `go test -run TestSpa ./internal/spa/...` and the SPA fallback**

Run: `go test ./internal/spa/...`
Expected: PASS (index, React index, deep-route fallback, asset serving).

---

## Self-Review Notes

- **Spec coverage:** All spec sections map to tasks: model (Task 1), fsutil (Task 1), store/runstore (Task 1), runner/runlog/auth (Task 1), spa+dist relocation (Task 2), controller (Task 3), api (Task 4), cli (Task 5), cmd/cronix (Task 6), root deletion + build config (Task 7), end-to-end gate (Task 8).
- **No placeholders:** every production file has full content; every test file has full content.
- **Type consistency:** exported symbols match the Interfaces blocks across tasks (`runner.Run`, `runlog.FormatRun`, `auth.IssueSessionToken`, `spa.SPAHandler`, `controller.NewController`, `api.NewServer`, `cli.RunCLI`). `api` imports `runlog` for the manual-run log block (identical output to pre-refactor).
