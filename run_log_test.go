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
