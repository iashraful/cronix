# Web Login (Username/Password) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a username/password web login (`CRONIX_USERNAME`/`CRONIX_PASSWORD`, default `admin`/`admin`) that issues a stateless HMAC-signed session token, while the existing `CRONIX_API_TOKEN` stays in place for the CLI and API.

**Architecture:** A `POST /api/v1/login` endpoint (unauthenticated) verifies credentials in constant time and returns a hand-rolled signed token (`payload.signature`, HMAC-SHA256 keyed by the API token). The auth middleware now accepts either the static API token or a valid signed session token. The web UI login form collects username/password, exchanges them for a session token, and reuses the existing `sessionStorage` storage path.

**Tech Stack:** Go 1.24 (stdlib only — no new deps), React/Vite UI, Makefile, README.

## Global Constraints

- No new Go module dependencies. `go.mod` keeps only `github.com/google/shlex` and `github.com/robfig/cron/v3`.
- `CRONIX_API_TOKEN` stays required at startup and is the HMAC signing key for session tokens.
- Defaults: `CRONIX_USERNAME=admin`, `CRONIX_PASSWORD=admin`, `CRONIX_SESSION_TTL=24h` (parsed with `time.ParseDuration`).
- Login only for the web UI. CLI/API bearer header and `--token` flag are unchanged.
- Session tokens are stateless: `base64url(payload).base64url(hmac)` where payload is `{"u":"<username>","exp":<unix>}`. No server-side store, no logout endpoint.
- Web UI stores the session token under the existing `sessionStorage` key `cronix_token`.
- Gate: `go build ./...`, `go vet ./...`, `go test ./...` green; `gofmt -l .` clean; `web/` bundle rebuilt and committed.

---

### Task 1: Session token core — `auth.go` + `auth_test.go`

Hand-rolled signed token: issue and validate. No other code depends on it yet.

**Files:**
- Create: `auth.go`, `auth_test.go`

**Interfaces:**
- Consumes: nothing (stdlib only: `crypto/hmac`, `crypto/sha256`, `encoding/base64`, `encoding/json`, `strings`, `time`).
- Produces (used by Task 2):
  - `func issueSessionToken(key []byte, username string, ttl time.Duration) (string, error)`
  - `func validateSessionToken(key []byte, token string) (string, bool)`

- [ ] **Step 1: Write the failing tests**

Create `auth_test.go`:

```go
package main

import (
	"strings"
	"testing"
	"time"
)

func TestSessionTokenRoundTrip(t *testing.T) {
	token, err := issueSessionToken([]byte("secret"), "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	user, ok := validateSessionToken([]byte("secret"), token)
	if !ok || user != "admin" {
		t.Fatalf("validate: ok=%v user=%q", ok, user)
	}
}

func TestSessionTokenTamperedSignature(t *testing.T) {
	token, err := issueSessionToken([]byte("secret"), "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	body, _, _ := strings.Cut(token, ".")
	tampered := body + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, ok := validateSessionToken([]byte("secret"), tampered); ok {
		t.Fatal("tampered signature must be rejected")
	}
}

func TestSessionTokenWrongKey(t *testing.T) {
	token, err := issueSessionToken([]byte("secret"), "admin", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, ok := validateSessionToken([]byte("other"), token); ok {
		t.Fatal("wrong key must be rejected")
	}
}

func TestSessionTokenExpired(t *testing.T) {
	token, err := issueSessionToken([]byte("secret"), "admin", -time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, ok := validateSessionToken([]byte("secret"), token); ok {
		t.Fatal("expired token must be rejected")
	}
}

func TestSessionTokenMalformed(t *testing.T) {
	for _, token := range []string{"", "abc", "a.b.c", "..", ".sig", "body."} {
		if _, ok := validateSessionToken([]byte("secret"), token); ok {
			t.Errorf("malformed token %q must be rejected", token)
		}
	}
	if _, ok := validateSessionToken([]byte("secret"), "AAAA.!!!not-base64!!!"); ok {
		t.Fatal("bad base64 signature must be rejected")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestSessionToken' ./...`
Expected: FAIL — compile error `undefined: issueSessionToken`.

- [ ] **Step 3: Write the implementation**

Create `auth.go`:

```go
package main

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

func issueSessionToken(key []byte, username string, ttl time.Duration) (string, error) {
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

func validateSessionToken(key []byte, token string) (string, bool) {
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

Note: `base64.RawURLEncoding` never emits `.`, so `strings.Cut` on `.` splits exactly body and signature. Tokens with extra `.` segments fail the signature/base64 checks.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run 'TestSessionToken' ./...`
Expected: PASS. Then `gofmt -l .` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add auth.go auth_test.go
git commit -m "feat: add signed session token issue/validate for web login"
```

---

### Task 2: Login endpoint, dual-auth middleware, and wiring

Add `AuthConfig`, change `NewServer`/`Server`, add `POST /api/v1/login`, accept static *or* session tokens in the middleware. Update all call sites (`main.go`, tests) and the Makefile so the build stays green.

**Files:**
- Modify: `api.go`, `main.go`, `Makefile`
- Modify (tests): `api_test.go`, `cli_test.go`, `spa_test.go`

**Interfaces:**
- Consumes: `issueSessionToken([]byte, string, time.Duration) (string, error)` and `validateSessionToken([]byte, string) (string, bool)` from Task 1.
- Produces (used by Task 3):
  - `type AuthConfig struct { Token string; Username string; Password string; SessionTTL time.Duration }`
  - `func NewServer(ctrl *Controller, cfg AuthConfig) http.Handler` — signature changed from `NewServer(ctrl *Controller, token string)`
  - Endpoint `POST /api/v1/login` → `200 {"token":"..."}` | `400 {"error":"..."}` | `401 {"error":"invalid credentials"}`

- [ ] **Step 1: Write the failing tests**

In `api_test.go`, add `"time"` to the import block. Add the `testAuth` helper right after `newTestServer`. Append the new tests at the end of the file. Do not update existing `NewServer(...)` call sites yet — they will fail to compile, which is the expected red state.

Add after `newTestServer` (keep `req` unchanged):

```go
func testAuth(token string) AuthConfig {
	return AuthConfig{Token: token, Username: "admin", Password: "admin", SessionTTL: time.Hour}
}
```

Append these tests:

```go
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
	c, err := NewController(&memStore{}, &memRunStore{}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	h := NewServer(c, testAuth("secret"))
	token, err := issueSessionToken([]byte("secret"), "admin", time.Hour)
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
	token, err := issueSessionToken([]byte("secret"), "admin", -time.Second)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	w, _ := req(t, h, "GET", "/api/v1/jobs", token, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expired session: want 401, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./...`
Expected: FAIL — compile error (`AuthConfig` undefined / `NewServer(c, "secret")` has too few arguments / `testAuth` unused).

- [ ] **Step 3: Implement the API changes**

In `api.go`, replace the `Server` struct and `NewServer` (lines 51-68) with:

```go
type AuthConfig struct {
	Token      string
	Username   string
	Password   string
	SessionTTL time.Duration
}

type Server struct {
	ctrl *Controller
	cfg  AuthConfig
}

func NewServer(ctrl *Controller, cfg AuthConfig) http.Handler {
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
	mux.Handle("GET /", SPAHandler())
	return mux
}
```

Replace the `auth` method (lines 70-85) with dual-acceptance and add the login handler:

```go
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
	_, ok := validateSessionToken([]byte(s.cfg.Token), got)
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
	token, err := issueSessionToken([]byte(s.cfg.Token), reqJSON.Username, s.cfg.SessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%s", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}
```

- [ ] **Step 4: Update main.go call site and add env wiring**

Replace the `srv` construction (line 55) so the build compiles:

```go
	srv := &http.Server{
		Addr: httpAddr,
		Handler: NewServer(ctrl, AuthConfig{
			Token:      token,
			Username:   envOr("CRONIX_USERNAME", "admin"),
			Password:   envOr("CRONIX_PASSWORD", "admin"),
			SessionTTL: envDuration("CRONIX_SESSION_TTL", 24*time.Hour),
		}),
	}
```

Add this helper next to `envOr`:

```go
func envDuration(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
```

`time` and `os` are already imported in `main.go`.

- [ ] **Step 5: Update the remaining test call sites**

In `api_test.go`:
- Line 21 (`newTestServer`): `return NewServer(c, testAuth("secret"))`.
- Lines 158, 181, 208, 226, 248, 266, 302, 340: `NewServer(c, "secret")` → `NewServer(c, testAuth("secret"))`.
- Line 361: `NewServer(c, "tok")` → `NewServer(c, testAuth("tok"))`.

In `cli_test.go` (lines 23, 53, 99, 121): `NewServer(c, "tok")` → `NewServer(c, testAuth("tok"))`.

In `spa_test.go` (line 17): `NewServer(c, "secret")` → `NewServer(c, testAuth("secret"))`.

- [ ] **Step 6: Update the Makefile**

In the `run` target, add two env lines before `CRONIX_STORE_PATH`:

```make
	CRONIX_USERNAME=$${CRONIX_USERNAME:-admin} \
	CRONIX_PASSWORD=$${CRONIX_PASSWORD:-admin} \
```

In the `docker-run` target, add before the `-v` flag:

```make
		-e CRONIX_USERNAME=$${CRONIX_USERNAME:-admin} \
		-e CRONIX_PASSWORD=$${CRONIX_PASSWORD:-admin} \
```

- [ ] **Step 7: Run the full verification**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all pass. Existing `TestAPIRequiresToken` still passes because a wrong value fails both the static compare and `validateSessionToken`.

Run: `gofmt -l .`
Expected: no output.

- [ ] **Step 8: Commit**

```bash
git add api.go main.go Makefile api_test.go cli_test.go spa_test.go
git commit -m "feat: add username/password web login issuing signed session tokens"
```

---

### Task 3: Web UI login form + rebuilt bundle

Swap the token entry box for username/password, call `/api/v1/login`, and reuse the existing token storage. Rebuild `web/` so the embed picks up the new UI.

**Files:**
- Modify: `ui/src/api.js`, `ui/src/App.jsx`
- Rebuild: `web/` (generated — commit the results, `web/index.html` + `web/assets/*`)

**Interfaces:**
- Consumes: `POST /api/v1/login` from Task 2 (`{"username","password"}` → `{"token"}`), existing `api.setToken`/`api.getToken` (`sessionStorage` key `cronix_token`).
- Produces: `api.login(username, password)` used by `App.jsx`.

- [ ] **Step 1: Add the login API helper**

In `ui/src/api.js`, add after the `request` function:

```js
export async function login(username, password) {
  const resp = await fetch('/api/v1/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  const data = await resp.json().catch(() => null)
  if (!resp.ok) {
    throw new Error((data && data.error) || `request failed (${resp.status})`)
  }
  return data
}
```

- [ ] **Step 2: Replace the Login component in `App.jsx`**

Replace the entire existing `Login` function (lines 7-39) with:

```jsx
function Login({ onToken }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  return (
    <div className="main">
      <form
        className="card"
        style={{ maxWidth: 360, margin: '48px auto' }}
        onSubmit={async (e) => {
          e.preventDefault()
          setError('')
          try {
            const res = await api.login(username.trim(), password)
            api.setToken(res.token)
            onToken(res.token)
          } catch (err) {
            setError(err.message)
          }
        }}
      >
        <h1 style={{ margin: '0 0 4px' }}>Cronix</h1>
        <p className="muted" style={{ margin: '0 0 16px' }}>Sign in to manage your jobs.</p>
        <div className="field">
          <label htmlFor="login-username">Username</label>
          <input
            id="login-username"
            className="input"
            type="text"
            value={username}
            autoComplete="username"
            autoFocus
            onChange={(e) => setUsername(e.target.value)}
          />
        </div>
        <div className="field">
          <label htmlFor="login-password">Password</label>
          <input
            id="login-password"
            className="input"
            type="password"
            value={password}
            autoComplete="current-password"
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        {error && (
          <p className="muted" style={{ margin: '0 0 12px' }}>{error}</p>
        )}
        <button className="btn primary" type="submit" style={{ width: '100%' }}>Sign in</button>
      </form>
    </div>
  )
}
```

`api` is already imported in `App.jsx` (`import * as api from './api'`).

- [ ] **Step 3: Rebuild the web bundle**

Run: `npm --prefix ui run build`
Expected: succeeds; `git status` shows changes under `web/` (`index.html`, `assets/index-*.js`, `assets/index-*.css`).

- [ ] **Step 4: Verify the Go build still embeds and tests pass**

Run: `go test ./...`
Expected: PASS (SPA tests serve the rebuilt `web/`). Then `gofmt -l .` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add ui/src/api.js ui/src/App.jsx web/
git commit -m "feat: username/password login form in web UI"
```

---

### Task 4: README documentation

Document the new env vars, the login endpoint, the new web login, and the security notes.

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: nothing code-side. Mirrors the behavior implemented in Tasks 2-3.

- [ ] **Step 1: Update the environment variables table**

In `README.md`, after the `CRONIX_API_TOKEN` row add:

```md
| `CRONIX_USERNAME` | `admin` | Username for the web UI login. |
| `CRONIX_PASSWORD` | `admin` | Password for the web UI login. |
| `CRONIX_SESSION_TTL` | `24h` | Lifetime of a web session token. Unparseable values silently fall back to the default. |
```

- [ ] **Step 2: Update the REST API section**

In the paragraph after `BASE = http://HOST:8080`, replace:

```md
All `/api/v1/*` routes require
`Authorization: Bearer <token>` (constant-time compared); otherwise `401`.
```

with:

```md
All `/api/v1/*` routes require
`Authorization: Bearer <token>` (constant-time compared); otherwise `401`. The
token is either the static `CRONIX_API_TOKEN` or a session token from
`POST /api/v1/login`.
```

Add a first row to the routes table:

```md
| `POST /api/v1/login` | Log in with the configured username/password; returns a session token. | `200` `{"token":"..."}` |
```

In the `### Examples` block, after `BASE=...`, add:

```sh
curl -s -X POST -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin"}' \
  "$BASE/api/v1/login"
```

and note that the returned `token` can be used as `Authorization: Bearer` in the job calls below.

- [ ] **Step 3: Update the Web UI section**

Replace:

```md
Get the UI at `/` from the host: `http://localhost:8080`. Enter the API token
(saved in session storage), then create, edit, enable/disable, run, and delete
jobs.
```

with:

```md
Get the UI at `/` from the host: `http://localhost:8080`. Sign in with the
configured username and password (default `admin`/`admin`). On success the UI
stores a signed session token in session storage and uses it for the REST API;
then create, edit, enable/disable, run, and delete jobs as before.
```

- [ ] **Step 4: Update the Security Notes**

After the existing first bullet about `CRONIX_API_TOKEN`, add:

```md
- The web UI logs in with a single configured username/password pair
  (`CRONIX_USERNAME`/`CRONIX_PASSWORD`). The defaults are `admin`/`admin` — set
  real credentials for anything beyond local use. Session tokens are
  HMAC-signed with the `CRONIX_API_TOKEN`, so rotating the API token invalidates
  all issued web sessions.
```

- [ ] **Step 5: Verify and commit**

Run: `git diff --stat` to confirm only `README.md` changed for this task.
Run: `gofmt -l .` (no output expected).
Run: `go build ./... && go test ./...` (passes — no code changed, regression check).

```bash
git add README.md
git commit -m "docs: document username/password web login"
```

---