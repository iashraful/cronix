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
