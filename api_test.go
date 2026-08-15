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
	}}, "/usr/bin/true")
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

func TestAPIRunExhaustedRetriesStillReturns200WithSteps(t *testing.T) {
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Retries: 1, RetryDelay: 0, Enabled: true},
	}}, "/usr/bin/false")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, "secret")
	w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("run with exhausted retries: want 200 with completed steps, got %d: %s", w.Code, b)
	}
	var resp struct {
		Steps []Result `json:"steps"`
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
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, "/nonexistent/curl")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, "secret")
	w, b := req(t, h, "POST", "/api/v1/jobs/a/run", "secret", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("cannot-start-binary must stay a 500, got %d: %s", w.Code, b)
	}
}
