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
