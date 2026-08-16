package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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
	job := Job{Id: "j0", Schedule: "* * * * *", Curl: `curl -H "X-Test: abc" -d "hello world" http://example.com`, Retries: 0}
	_, err := runJob(job, curlPath)
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
	job := Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 3}
	results, err := runJob(job, curlPath)
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
	job := Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 5, RetryDelay: 0}
	results, err := runJob(job, curlPath)
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
	job := Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 2, RetryDelay: 0}
	results, err := runJob(job, curlPath)
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
	job := Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 0}
	results, err := runJob(job, path)
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
	job := Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 0}
	_, err := runJob(job, filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("want error when curl binary missing")
	}
}

func TestRunHonorsRetryDelay(t *testing.T) {
	curlPath, _ := fakeCurl(t, 2)
	start := time.Now()
	job := Job{Id: "j0", Schedule: "* * * * *", Curl: "curl http://example.com", Retries: 1, RetryDelay: 1}
	results, err := runJob(job, curlPath)
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
