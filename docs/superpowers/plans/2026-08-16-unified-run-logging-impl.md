# Unified Run Logging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render one human-readable, identical run-result block across server logs, the API `run` handler's log output, and CLI `run` output.

**Architecture:** A single shared renderer `formatRun(job, results, err) string` in a new `run_log.go`. `Controller.fire` logs it on scheduled-failure (compact line on success); the API `handleRun` logs it for every manual run while keeping the `200 {steps}` response contract; CLI `run` fetches the job, derives the failure state from the last step's exit code, and prints the same block.

**Tech Stack:** Go 1.24, stdlib `log`, `errors`, `strings`; existing `ErrCommandFailed` sentinel (runner.go), `Result` struct (runner.go), `Job` struct (store.go), `cliGetJob` (cli.go).

## Global Constraints

- API `run` response contract is UNCHANGED: `200` `{steps:[...]}` for both success and exhausted-retries; only genuine faults → `500`. Do not alter `Result` or `Job` JSON tags.
- Scheduled fires: success keeps the existing compact one-liner (`job=... schedule=... attempt=1/1 exit=0 output=...`); only failures log the full block.
- Do NOT change go.mod. Go 1.24. No new dependencies.
- Output cap is the existing `outputCap` (4096); `Result.Output` is already capped at run time by `tail`.
- `name=` is omitted when the job name is empty; `schedule=` and `command=` are always present.
- Host note: `/bin/true` does NOT exist on this host — use `/usr/bin/true` and `/usr/bin/false` in tests.
- Gate before every commit: `go build ./... && go vet ./... && go test ./...` all green and `gofmt -l .` clean.

---

### Task 1: `formatRun` renderer

**Files:**
- Create: `run_log.go`
- Test: `run_log_test.go`

**Interfaces:**
- Consumes: `Job` (store.go), `Result` (runner.go), `ErrCommandFailed` (runner.go).
- Produces: `func formatRun(job Job, results []Result, err error) string` — the single block renderer used by Tasks 2 and 3.

Block shape (no trailing newline):

```
run job=<id> name=<name> schedule="<expr>" command="<curl>"
  attempt 1/1 exit=6
    output: curl: (6) Could not resolve host: ...
  result: FAILED (last exit=6, 1/1 attempts)
```

- `name=` omitted when `job.Name == ""`.
- Per-attempt: `output:` shows `strings.TrimSpace(r.Output)`, or `(no output)` when empty. Attempt lines omitted when `results` is empty.
- `result:` line:
  - `err == nil` → `OK`
  - `errors.Is(err, ErrCommandFailed)` → `FAILED (last exit=<last step exit>, <len(results)>/<total> attempts)` where `total` is `results[0].Total` (or the last step's Total if present), `last exit` is the final step's `ExitCode`.
  - any other non-nil error → `ERROR (<err>)`

- [ ] **Step 1: Write the failing tests**

Create `run_log_test.go`:

```go
package main

import (
	"errors"
	"strings"
	"testing"
)

func TestFormatRunSuccess(t *testing.T) {
	job := Job{Id: "a", Name: "ping", Schedule: "* * * * *", Curl: "curl -s https://example.com"}
	results := []Result{{Attempt: 1, Total: 1, ExitCode: 0, Output: "hello"}}
	got := formatRun(job, results, nil)
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
			t.Errorf("formatRun success missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "FAILED") || strings.Contains(got, "ERROR") {
		t.Errorf("success run should not mention FAILED/ERROR:\n%s", got)
	}
}

func TestFormatRunFailedExhaustedRetries(t *testing.T) {
	job := Job{Id: "a", Name: "", Schedule: "* * * * *", Curl: "curl http://x"}
	results := []Result{
		{Attempt: 1, Total: 2, ExitCode: 6, Output: "curl: (6) Could not resolve host"},
		{Attempt: 2, Total: 2, ExitCode: 6, Output: "curl: (6) Could not resolve host"},
	}
	err := errors.Join(ErrCommandFailed, errors.New("job a failed after 2 attempts (last exit=6)"))
	got := formatRun(job, results, err)
	for _, want := range []string{
		"attempt 1/2 exit=6",
		"attempt 2/2 exit=6",
		"curl: (6) Could not resolve host",
		"result: FAILED (last exit=6, 2/2 attempts)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatRun failed missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "name=") {
		t.Errorf("empty name must omit the name= label:\n%s", got)
	}
}

func TestFormatRunNoOutput(t *testing.T) {
	job := Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}
	results := []Result{{Attempt: 1, Total: 1, ExitCode: 1, Output: ""}}
	err := errors.Join(ErrCommandFailed, errors.New("job a failed after 1 attempts (last exit=1)"))
	got := formatRun(job, results, err)
	if !strings.Contains(got, "output: (no output)") {
		t.Errorf("empty output should render '(no output)':\n%s", got)
	}
}

func TestFormatRunNonCommandError(t *testing.T) {
	job := Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}
	results := []Result{}
	err := errors.New("cannot start /nonexistent/curl: no such file or directory")
	got := formatRun(job, results, err)
	if !strings.Contains(got, "result: ERROR (cannot start /nonexistent/curl") {
		t.Errorf("generic error should render ERROR (<err>):\n%s", got)
	}
	if strings.Contains(got, "attempt ") {
		t.Errorf("zero results must omit attempt lines:\n%s", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestFormatRun' ./...`
Expected: FAIL — compile error (`formatRun` undefined).

- [ ] **Step 3: Write minimal implementation**

Create `run_log.go`:

```go
package main

import (
	"errors"
	"fmt"
	"strings"
)

func formatRun(job Job, results []Result, err error) string {
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
	case errors.Is(err, ErrCommandFailed):
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

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run 'TestFormatRun' ./...`
Expected: PASS (all 4 `TestFormatRun*`).

- [ ] **Step 5: Commit**

```bash
git add run_log.go run_log_test.go
git commit -m "feat: add unified run result formatter"
```

---

### Task 2: Wire scheduled-fire and manual-run logging

**Files:**
- Modify: `controller.go:311-326` (`fire`)
- Modify: `api.go:135-153` (`handleRun`)
- Test: `controller_test.go` (update `TestScheduledFireUsesLiveCurlAfterUpdate`), `api_test.go` (verify existing tests still pass)

**Interfaces:**
- Consumes: `formatRun(job Job, results []Result, err error) string` (Task 1), `ErrCommandFailed` (runner.go), existing `Controller.fire`/`handleRun` bodies.
- Produces: none (log-side only; response contract unchanged).

- [ ] **Step 1: Write/update the failing tests**

Update `TestScheduledFireUsesLiveCurlAfterUpdate` in `controller_test.go`. The failed scheduled fire now logs the block (`attempt 1/1 exit=1` and `result: FAILED`) instead of the old `attempt=1/1 exit=1` compact line, so the assertion `strings.Contains(got, "attempt=1/1")` breaks. Replace lines 303-305 with:

```go
	if !strings.Contains(got, "attempt 1/1 exit=1") {
		t.Fatalf("scheduled fire did not run the live curl; logs:\n%s", got)
	}
	if !strings.Contains(got, "result: FAILED") {
		t.Errorf("failed scheduled fire should log the run block with result: FAILED:\n%s", got)
	}
```

Run: `go test -run 'TestScheduledFireUsesLiveCurlAfterUpdate' ./...`
Expected: FAIL — the log no longer contains `attempt=1/1`.

Add to `api_test.go`:

```go
func TestAPIRunManualLogsBlockOnSuccess(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Name: "ping", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, "/usr/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, "secret")
	w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("run: want 200, got %d: %s", w.Code, b)
	}
	got := logs.String()
	if !strings.Contains(got, "run job=a") || !strings.Contains(got, "result: OK") {
		t.Errorf("manual run should log the block:\n%s", got)
	}
}
```

Add to `api_test.go`:

```go
func TestAPIRunManualLogsBlockOnFailure(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Retries: 1, RetryDelay: 0, Enabled: true},
	}}, "/usr/bin/false")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, "secret")
	w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("run with exhausted retries: want 200, got %d: %s", w.Code, b)
	}
	got := logs.String()
	if !strings.Contains(got, "result: FAILED") || !strings.Contains(got, "2/2 attempts") {
		t.Errorf("manual run failure should log the block with FAILED:\n%s", got)
	}
}
```

Add the needed imports (`bytes`, `os`, `strings`, `testing` already? check — `api_test.go` imports `bytes`, `encoding/json`, `io`, `net/http`, `net/http/httptest`, `testing`). Add `os` and `strings` to the import block.

Run: `go test -run 'TestAPIRunManual' ./...`
Expected: FAIL — `handleRun` does not yet log the block.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestScheduledFireUsesLiveCurlAfterUpdate|TestAPIRunManualLogs' ./...`
Expected: FAIL (log assertions).

- [ ] **Step 3: Implement**

In `controller.go`, replace the body of `fire` (lines 311-326) with:

```go
func (c *Controller) fire(id string) {
	job, ok := c.jobByID(id)
	if !ok {
		return
	}
	c.runMu.Lock()
	defer c.runMu.Unlock()
	results, err := Run(job, c.curlPath)
	if err != nil {
		log.Printf("%s", formatRun(job, results, err))
		return
	}
	for _, r := range results {
		log.Printf("job=%s schedule=%q attempt=%d/%d exit=%d output=%s",
			job.Id, job.Schedule, r.Attempt, r.Total, r.ExitCode, strings.TrimSpace(r.Output))
	}
}
```

In `api.go`, replace the body of `handleRun` (lines 135-153) with:

```go
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, gerr := s.ctrl.Get(id)
	if gerr != nil {
		writeCtrlError(w, gerr)
		return
	}
	results, err := s.ctrl.Run(id)
	if err != nil && !errors.Is(err, ErrCommandFailed) {
		writeCtrlError(w, err)
		return
	}
	log.Printf("%s", formatRun(job, results, err))
	writeJSON(w, http.StatusOK, struct {
		Steps []Result `json:"steps"`
	}{Steps: results})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS — `TestScheduledFireUsesLiveCurlAfterUpdate`, `TestAPIRunManualLogsBlockOnSuccess`, `TestAPIRunManualLogsBlockOnFailure`, and all prior run tests (`TestAPIRunManual`, `TestAPIRunExhaustedRetriesStillReturns200WithSteps`, `TestAPIRunStartupFailureReturns500`, `TestRunCLIBadTokenExit2`) stay green (contract unchanged).

- [ ] **Step 5: Commit**

```bash
git add controller.go controller_test.go api.go api_test.go
git commit -m "feat: log unified run block for scheduled failures and manual runs"
```

---

### Task 3: CLI `run` prints the block

**Files:**
- Modify: `cli.go:302-333` (`cliRun`)
- Test: `cli_test.go`

**Interfaces:**
- Consumes: `formatRun` (Task 1), `cliGetJob(opts, id) (Job, error)` (cli.go), `ErrCommandFailed` (runner.go).
- Produces: none.

- [ ] **Step 1: Write the failing test**

Add to `cli_test.go`:

```go
func TestRunCLIRunPrintsBlock(t *testing.T) {
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Name: "ping", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, "/usr/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	addr := withCLIServer(t, NewServer(c, "tok"))
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
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, "/usr/bin/false")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	addr := withCLIServer(t, NewServer(c, "tok"))
	out := new(bytes.Buffer)
	code := runCLIIn([]string{"run", "--addr", addr, "--token", "tok", "a"}, out)
	if code != 0 {
		t.Fatalf("run: want exit 0 (domain failure), got %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "result: FAILED") {
		t.Errorf("failed run output should include result: FAILED:\n%s", out.String())
	}
}
```

`cli_test.go` already imports `bytes`, `net/http/httptest`, `strings`, `testing`. `NewServer` and `memStore` are in the same package.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestRunCLIRun' ./...`
Expected: FAIL — `cliRun` still prints compact `attempt=...` lines, not the block (`result: OK` / `result: FAILED` absent).

- [ ] **Step 3: Implement**

In `cli.go`, replace the body of `cliRun` (lines 302-333) with:

```go
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
		Steps []Result `json:"steps"`
	}
	if err := json.Unmarshal(resp, &runResp); err != nil {
		fmt.Fprintf(out, "error: cannot decode run: %v\n", err)
		return 2
	}
	var runErr error
	if n := len(runResp.Steps); n > 0 && runResp.Steps[n-1].ExitCode != 0 {
		runErr = ErrCommandFailed
	}
	fmt.Fprintln(out, formatRun(job, runResp.Steps, runErr))
	return 0
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS — the two new CLI tests and all prior tests.

- [ ] **Step 5: Commit**

```bash
git add cli.go cli_test.go
git commit -m "feat: print unified run block from cronix cli run"
```

---

### Task 4: Full-suite verification

**Files:** none.

- [ ] **Step 1: Run the full gate**

```bash
go build ./... && go vet ./... && go test ./... -count=1
gofmt -l .
```

Expected: build/vet green, all tests PASS, `gofmt -l .` prints nothing.

- [ ] **Step 2: Commit any stragglers**

If `gofmt` or lint surfaced anything, fix it and `git commit -m "style: gofmt"`. Otherwise nothing to commit.
