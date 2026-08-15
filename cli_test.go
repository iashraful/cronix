package main

import (
	"bytes"
	"net/http"
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
