# Cronix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build Cronix — a Go application, packaged as a `scratch`-based Docker image, that reads cron+curl jobs from environment variables and shells out to `curl` on schedule, with retry-on-failure.

**Architecture:** `main.go` loads and validates jobs at startup, schedules them with `robfig/cron/v3`, and on each fire a runner tokenizes the curl command with `shlex`, `exec`s the `curl` binary directly (no shell), captures output (capped at 4 KiB), retries on failure per job config, and logs one line per attempt to stdout. Docker builds a statically-linked `curl` from source in a `golang:1.24-alpine` stage and copies just the Go binary, the curl binary, and a CA bundle into `scratch`.

**Tech Stack:** Go 1.24, `github.com/robfig/cron/v3`, `github.com/google/shlex`, Docker multi-stage build (`golang:1.24-alpine` → `scratch`), curl 8.21.0 from source.

## Global Constraints

- Runtime image is `scratch`: no shell, no libc, no OS packages. No `sh -c` anywhere in the Go code.
- Every env-provided curl command is parsed (not shell-executed); `tokens[0]` must be `curl`.
- Captured subprocess output capped at 4 KiB (last 4 KiB kept).
- Log format per attempt: `timestamp job=<i> schedule=<expr> attempt=<n>/<total> exit=<code> output=<tail>`.
- Env discovery: index from 0, stop at first missing `CRON_SCHEDULE_<i>`; a present schedule without `CRON_CURL_<i>` is a startup error.
- Defaults: `CRON_RETRIES_<i>` = `0`, `CRON_RETRY_DELAY_<i>` = `5` seconds, `CURL_PATH` = `/usr/local/bin/curl`.
- All startup validation errors abort with non-zero exit.
- Go version floor: 1.24.

---

### Task 1: Module scaffold + job config loading

**Files:**
- Create: `go.mod`
- Create: `config.go`
- Test: `config_test.go`

**Interfaces:**
- Produces: `type Job struct { Index int; Schedule string; Curl string; Retries int; RetryDelay time.Duration }`
- Produces: `func LoadJobs(lookup func(string) (string, bool)) ([]Job, error)` — returns validated jobs for `CRON_SCHEDULE_<i>`/`CRON_CURL_<i>` pairs scanned from index 0 until a gap; errors name the offending env var.
- Consumes: none.

- [ ] **Step 1: Initialize the module and add dependencies**

Run:
```bash
go mod init cronix
go get github.com/robfig/cron/v3@latest
go get github.com/google/shlex@latest
```
Expected: `go.mod` created with module `cronix` and both requires pinned; `go.sum` generated.

- [ ] **Step 2: Write the failing tests**

Create `config_test.go`:

```go
package main

import (
	"strings"
	"testing"
	"time"
)

func lookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestLoadJobsParsesPairs(t *testing.T) {
	m := map[string]string{
		"CRON_SCHEDULE_0": "*/5 * * * *",
		"CRON_CURL_0":     "curl -s https://example.com",
		"CRON_SCHEDULE_1": "* * * * *",
		"CRON_CURL_1":     "curl -X POST https://example.com/api",
	}
	jobs, err := LoadJobs(lookup(m))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	if jobs[0].Index != 0 || jobs[0].Schedule != "*/5 * * * *" || jobs[0].Curl != "curl -s https://example.com" {
		t.Errorf("job 0 wrong: %+v", jobs[0])
	}
	if jobs[1].Index != 1 || jobs[1].Schedule != "* * * * *" {
		t.Errorf("job 1 wrong: %+v", jobs[1])
	}
}

func TestLoadJobsStopsAtGap(t *testing.T) {
	m := map[string]string{
		"CRON_SCHEDULE_0": "* * * * *",
		"CRON_CURL_0":     "curl -s https://example.com",
		"CRON_SCHEDULE_2": "* * * * *",
		"CRON_CURL_2":     "curl -s https://example.com/2",
	}
	jobs, err := LoadJobs(lookup(m))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job (scan stops at gap), got %d", len(jobs))
	}
}

func TestLoadJobsMissingCurlIsError(t *testing.T) {
	m := map[string]string{"CRON_SCHEDULE_0": "* * * * *"}
	_, err := LoadJobs(lookup(m))
	if err == nil {
		t.Fatal("want error for schedule without curl")
	}
	if !strings.Contains(err.Error(), "CRON_CURL_0") {
		t.Errorf("error should name CRON_CURL_0: %v", err)
	}
}

func TestLoadJobsInvalidScheduleIsError(t *testing.T) {
	m := map[string]string{
		"CRON_SCHEDULE_0": "not a cron",
		"CRON_CURL_0":     "curl -s https://example.com",
	}
	_, err := LoadJobs(lookup(m))
	if err == nil {
		t.Fatal("want error for invalid cron expression")
	}
	if !strings.Contains(err.Error(), "CRON_SCHEDULE_0") {
		t.Errorf("error should name CRON_SCHEDULE_0: %v", err)
	}
}

func TestLoadJobsRetryDefaults(t *testing.T) {
	m := map[string]string{
		"CRON_SCHEDULE_0": "* * * * *",
		"CRON_CURL_0":     "curl -s https://example.com",
	}
	jobs, err := LoadJobs(lookup(m))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jobs[0].Retries != 0 {
		t.Errorf("default retries want 0, got %d", jobs[0].Retries)
	}
	if jobs[0].RetryDelay != 5*time.Second {
		t.Errorf("default retry delay want 5s, got %v", jobs[0].RetryDelay)
	}
}

func TestLoadJobsRetryParsing(t *testing.T) {
	m := map[string]string{
		"CRON_SCHEDULE_0":  "* * * * *",
		"CRON_CURL_0":      "curl -s https://example.com",
		"CRON_RETRIES_0":   "3",
		"CRON_RETRY_DELAY_0": "10",
	}
	jobs, err := LoadJobs(lookup(m))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jobs[0].Retries != 3 {
		t.Errorf("retries want 3, got %d", jobs[0].Retries)
	}
	if jobs[0].RetryDelay != 10*time.Second {
		t.Errorf("retry delay want 10s, got %v", jobs[0].RetryDelay)
	}
}

func TestLoadJobsInvalidRetryValues(t *testing.T) {
	for _, tc := range []struct {
		name     string
		retries  string
		delay    string
		wantPart string
	}{
		{"non-numeric retries", "abc", "", "CRON_RETRIES_0"},
		{"negative retries", "-1", "", "CRON_RETRIES_0"},
		{"non-numeric delay", "", "xyz", "CRON_RETRY_DELAY_0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := map[string]string{
				"CRON_SCHEDULE_0": "* * * * *",
				"CRON_CURL_0":     "curl -s https://example.com",
			}
			if tc.retries != "" {
				m["CRON_RETRIES_0"] = tc.retries
			}
			if tc.delay != "" {
				m["CRON_RETRY_DELAY_0"] = tc.delay
			}
			_, err := LoadJobs(lookup(m))
			if err == nil {
				t.Fatal("want error")
			}
			if !strings.Contains(err.Error(), tc.wantPart) {
				t.Errorf("error should mention %s: %v", tc.wantPart, err)
			}
		})
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./...`
Expected: FAIL — compile error (no `LoadJobs`, no `Job`).

- [ ] **Step 4: Write minimal implementation**

Create `config.go`:

```go
package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/robfig/cron/v3"
)

type Job struct {
	Index      int
	Schedule   string
	Curl       string
	Retries    int
	RetryDelay time.Duration
}

func LoadJobs(lookup func(string) (string, bool)) ([]Job, error) {
	var jobs []Job
	for i := 0; ; i++ {
		schedName := "CRON_SCHEDULE_" + strconv.Itoa(i)
		schedule, ok := lookup(schedName)
		if !ok {
			break
		}
		if _, err := cron.ParseStandard(schedule); err != nil {
			return nil, fmt.Errorf("%s: invalid cron expression %q: %w", schedName, schedule, err)
		}
		curlName := "CRON_CURL_" + strconv.Itoa(i)
		cmd, ok := lookup(curlName)
		if !ok {
			return nil, fmt.Errorf("missing %s for %s", curlName, schedName)
		}
		retries, err := nonNegativeIntEnv(lookup, "CRON_RETRIES_"+strconv.Itoa(i), 0)
		if err != nil {
			return nil, err
		}
		delaySec, err := nonNegativeIntEnv(lookup, "CRON_RETRY_DELAY_"+strconv.Itoa(i), 5)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, Job{
			Index:      i,
			Schedule:   schedule,
			Curl:       cmd,
			Retries:    retries,
			RetryDelay: time.Duration(delaySec) * time.Second,
		})
	}
	return jobs, nil
}

func nonNegativeIntEnv(lookup func(string) (string, bool), name string, def int) (int, error) {
	v, ok := lookup(name)
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s: must be a non-negative integer, got %q", name, v)
	}
	return n, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS (all 7 test functions).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum config.go config_test.go
git commit -m "feat: add env-driven job config loading"
```

---

### Task 2: Command parsing and runner with retries

**Files:**
- Create: `runner.go`
- Test: `runner_test.go`

**Interfaces:**
- Consumes: `type Job`, `LoadJobs` from Task 1.
- Produces:
  - `func ParseCommand(cmd string) ([]string, error)` — shlex-splits `cmd`, errors if unparseable/empty or if first token is not `curl`.
  - `type Result struct { Attempt int; Total int; ExitCode int; Output string }`
  - `func Run(job Job, curlPath string) ([]Result, error)` — runs up to `job.Retries+1` attempts, sleeping `job.RetryDelay` between failures, stopping early on exit 0; returns per-attempt results and an error if the final attempt exited non-zero or curl could not be started.

- [ ] **Step 1: Write the failing tests**

Create `runner_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func fakeCurl(t *testing.T, successAt int) (path string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "curl")
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
	t.Setenv("SUCCESS_AT", strconv.Itoa(successAt))
	return path
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
	curlPath := fakeCurl(t, 1)
	logPath := filepath.Join(t.TempDir(), "log")
	t.Setenv("FAKE_LOG", logPath)

	job := Job{Index: 0, Schedule: "* * * * *", Curl: `curl -H "X-Test: abc" -d "hello world" http://example.com`, Retries: 0}
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
	curlPath := fakeCurl(t, 1)
	job := Job{Index: 0, Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 3}
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
	curlPath := fakeCurl(t, 2)
	job := Job{Index: 0, Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 5, RetryDelay: 0}
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
	curlPath := fakeCurl(t, 99)
	job := Job{Index: 0, Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 2, RetryDelay: 0}
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
	job := Job{Index: 0, Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 0}
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
	job := Job{Index: 0, Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 0}
	_, err := Run(job, filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("want error when curl binary missing")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./...`
Expected: FAIL — compile error (no `ParseCommand`, `Result`, `Run`).

- [ ] **Step 3: Write minimal implementation**

Create `runner.go`:

```go
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/google/shlex"
)

const outputCap = 4096

type Result struct {
	Attempt  int
	Total    int
	ExitCode int
	Output   string
}

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

func Run(job Job, curlPath string) ([]Result, error) {
	args, err := ParseCommand(job.Curl)
	if err != nil {
		return nil, err
	}
	total := job.Retries + 1
	results := make([]Result, 0, total)
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
		results = append(results, Result{
			Attempt:  attempt,
			Total:    total,
			ExitCode: exit,
			Output:   tail(buf.Bytes(), outputCap),
		})
		if exit == 0 {
			return results, nil
		}
		if attempt < total {
			time.Sleep(job.RetryDelay)
		}
	}
	return results, fmt.Errorf("job %d failed after %d attempts (last exit=%d)", job.Index, total, lastExit)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS (all runner + config tests).

- [ ] **Step 5: Commit**

```bash
git add runner.go runner_test.go
git commit -m "feat: add curl command parsing and retrying runner"
```

---

### Task 3: Main wiring, logging, and graceful shutdown

**Files:**
- Create: `main.go`

**Interfaces:**
- Consumes: `Job`, `LoadJobs`, `ParseCommand`, `Run`, `Result` from Tasks 1–2.
- Produces: `func envLookup(name string) (string, bool)` — thin wrapper over `os.LookupEnv`.

- [ ] **Step 1: Write the implementation**

Create `main.go`:

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
)

func envLookup(name string) (string, bool) {
	return os.LookupEnv(name)
}

func main() {
	curlPath := os.Getenv("CURL_PATH")
	if curlPath == "" {
		curlPath = "/usr/local/bin/curl"
	}

	jobs, err := LoadJobs(envLookup)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	if len(jobs) == 0 {
		log.Fatalf("no jobs configured: set CRON_SCHEDULE_0 and CRON_CURL_0")
	}
	for _, job := range jobs {
		if _, err := ParseCommand(job.Curl); err != nil {
			log.Fatalf("job %d: %v", job.Index, err)
		}
	}

	c := cron.New()
	for _, job := range jobs {
		j := job
		if _, err := c.AddFunc(j.Schedule, func() {
			results, err := Run(j, curlPath)
			for _, r := range results {
				log.Printf("job=%d schedule=%q attempt=%d/%d exit=%d output=%s",
					j.Index, j.Schedule, r.Attempt, r.Total, r.ExitCode, strings.TrimSpace(r.Output))
			}
			if err != nil {
				log.Printf("job=%d failed: %v", j.Index, err)
			}
		}); err != nil {
			log.Fatalf("job %d: %v", j.Index, err)
		}
	}

	log.Printf("cronix started with %d job(s)", len(jobs))
	c.Start()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	<-ctx.Done()

	log.Printf("shutting down")
	done := c.Stop()
	select {
	case <-done.Done():
	case <-time.After(10 * time.Second):
		log.Printf("in-flight jobs did not finish in 10s; exiting")
	}
	log.Printf("bye")
}
```

- [ ] **Step 2: Verify it compiles and vets clean**

Run:
```bash
go build ./...
go vet ./...
go test ./...
```
Expected: build and vet succeed; all tests PASS.

- [ ] **Step 3: Commit**

```bash
git add main.go
git commit -m "feat: wire up scheduler, logging, and graceful shutdown"
```

---

### Task 4: Dockerfile (static curl build → scratch runtime)

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`

**Interfaces:**
- Produces nothing for other tasks; this is the deployable artifact.
- `CURL_VERSION=8.21.0` and `CURL_SHA256=d9b327997999045a24cda50f3983e69e51c516bd8be6ef9842fc7f99135e33bb` are pinned.

- [ ] **Step 1: Write the Dockerfile**

Create `Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS build

ARG CURL_VERSION=8.21.0
ARG CURL_SHA256=d9b327997999045a24cda50f3983e69e51c516bd8be6ef9842fc7f99135e33bb

RUN apk add --no-cache gcc musl-dev make perl openssl-dev zlib-dev ca-certificates \
 && curl -fsSL "https://curl.se/download/curl-${CURL_VERSION}.tar.gz" -o /curl.tar.gz \
 && echo "${CURL_SHA256}  /curl.tar.gz" | sha256sum -c - \
 && mkdir /src \
 && tar xzf /curl.tar.gz -C /src --strip-components=1 \
 && cd /src \
 && ./configure --prefix=/out \
      --disable-shared --enable-static \
      --with-openssl --with-zlib \
      --disable-ldap --disable-ldaps \
      --without-libidn2 --without-librtmp --without-libpsl \
      --without-nghttp2 --without-brotli \
      --disable-docs --disable-manual \
 && make -j"$(nproc)" \
 && make install \
 && cd / \
 && rm -rf /src /curl.tar.gz

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /cronix .

FROM scratch

COPY --from=build /out/bin/curl /usr/local/bin/curl
COPY --from=build /cronix /cronix
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENV CURL_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["/cronix"]
```

- [ ] **Step 2: Write .dockerignore**

Create `.dockerignore`:

```
.git
docs
Dockerfile
.dockerignore
```

- [ ] **Step 3: Validate the Dockerfile parses**

Run: `docker build --check .`
Expected: Docker validates the build context without errors.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile .dockerignore
git commit -m "feat: add scratch-based Dockerfile with static curl"
```

---

### Task 5: README and end-to-end verification

**Files:**
- Create: `README.md`

**Interfaces:**
- Consumes: everything.

- [ ] **Step 1: Write the README**

Create `README.md`:

```markdown
# Cronix

Runs cron-style scheduled jobs in a Docker container. Each job shells out to the
`curl` binary to make an HTTP call. Runtime image is `scratch` with static
binaries only — no shell, no OS packages.

## Usage

Build:

```sh
docker build -t cronix .
```

Run:

```sh
docker run --rm \
  -e CRON_SCHEDULE_0='*/5 * * * *' \
  -e 'CRON_CURL_0=curl -s https://example.com/api/health' \
  cronix
```

## Environment Variables

| Variable | Example | Required | Notes |
|---|---|---|---|
| `CRON_SCHEDULE_<i>` | `*/5 * * * *` | yes (per job) | standard 5-field cron |
| `CRON_CURL_<i>` | `curl -s https://example.com/api` | yes (per job) | full curl command; must start with `curl`; quoted args supported, shell features are not |
| `CRON_RETRIES_<i>` | `3` | no | extra attempts on failure, default `0` |
| `CRON_RETRY_DELAY_<i>` | `10` | no | seconds between retries, default `5` |
| `CURL_PATH` | `/usr/local/bin/curl` | no | curl binary path |
| `TZ` | `America/New_York` | no | container timezone |

Jobs are discovered by index from `0` until a gap. A schedule without its curl
command is a startup error. Config is read once at startup only.

## Logs

One line per attempt on stdout, visible via `docker logs`:

```
2026/08/15 12:00:00 job=0 schedule="*/5 * * * *" attempt=1/1 exit=0 output=...
```

Non-zero final exit is logged as a failure; the scheduler continues.
```

- [ ] **Step 2: Build the image**

Run: `docker build -t cronix .`
Expected: build succeeds. (First build takes several minutes while curl
compiles; subsequent builds cache it.) A `docker image inspect -f '{{.Size}}' cronix` on the order of 10–20 MB.

- [ ] **Step 3: Verify static binaries in the runtime image**

Run:
```sh
docker run --rm --entrypoint /usr/local/bin/curl cronix --version
docker run --rm --entrypoint /cronix cronix
```
Expected: `curl 8.21.0 (...)` output from the first; the second exits non-zero
with a "no jobs configured" log line (proves the Go binary runs standalone on
scratch).

- [ ] **Step 4: End-to-end run against a real HTTPS API**

Run:
```sh
docker run --rm \
  -e CRON_SCHEDULE_0='* * * * *' \
  -e 'CRON_CURL_0=curl -s https://example.com' \
  -it cronix
```
Wait for two fires, then Ctrl-C. Expected: two `attempt=1/1 exit=0` log lines
with `example.com` HTML in `output=` (proves HTTPS + CA bundle + scheduling
work), and clean shutdown lines on Ctrl-C.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: add Cronix README"
```