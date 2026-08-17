# Cronix v3.1 — Web Login (Username/Password) Design

Date: 2026-08-17
Status: Approved (approach B)

## Problem

The web UI and API authenticate with a single shared static bearer token
(`CRONIX_API_TOKEN`). The user wants a username/password login for the web UI,
configured via environment variables, defaulting to `admin`/`admin`. The
existing token remains for API/CLI clients.

## Decisions (from brainstorming)

- API and CLI keep using `CRONIX_API_TOKEN`.
- The web UI login switches to username/password (`CRONIX_USERNAME` /
  `CRONIX_PASSWORD`, default `admin`/`admin`).
- Login issues a **stateless HMAC-signed session token** (approach B), not the
  static API token and not a server-side session store.

## Configuration (env)

| Variable | Default | Notes |
|---|---|---|
| `CRONIX_API_TOKEN` | — | Unchanged. Still required at startup; static bearer for CLI/API. Also the HMAC signing key for session tokens. |
| `CRONIX_USERNAME` | `admin` | Accepted username for the web login. |
| `CRONIX_PASSWORD` | `admin` | Accepted password for the web login. |
| `CRONIX_SESSION_TTL` | `24h` | Lifetime of an issued session token. Parsed with `time.ParseDuration`. |

## Design

### Signed session token (new `auth.go`, package main)

Hand-rolled, no new dependencies. Format:

```
<base64url(payload)>.<base64url(hmac_sha256(key, payload))>
```

- `payload` is compact JSON: `{"u":"<username>","exp":<unix seconds>}`.
- `key` is the `CRONIX_API_TOKEN` bytes.
- Issue time is not signed separately (not needed).

`issueSessionToken(username string, ttl time.Duration) (string, error)` —
builds payload, signs, returns the token string.

`validateSessionToken(token string) (username string, ok bool)` —
splits on the single `.`, rejects non-matching segment counts,
recomputes the signature and compares constant-time, rejects expired
(`exp <= now`).

Why the API token as key: it is the one secret already required at startup,
so forgery requires the same secret that already grants full access. Trade-off
documented in README: rotating `CRONIX_API_TOKEN` invalidates all issued web
sessions, and with default `admin`/`admin` creds the API token remains the
effective secret protecting session signing — deploy with a real password.

### API (`api.go`)

- `Server` gains an `AuthConfig`:

  ```go
  type AuthConfig struct {
      Token    string // static bearer for CLI/API
      Username string
      Password string
      SessionTTL time.Duration
  }
  ```

  `NewServer(ctrl *Controller, auth AuthConfig) http.Handler`. Existing tests
  update their `NewServer(...)` calls.

- New unauthenticated route: `POST /api/v1/login`.

  - Body `{"username": "...", "password": "..."}`.
  - Malformed JSON or missing fields → `400` `{"error": "..."}`.
  - Constant-time compares both username and password against config; mismatch
    → `401` `{"error": "invalid credentials"}` (same response whether the
    username or password is wrong).
  - Success → `200` `{"token": "<session token>"}`.

- Auth middleware changed to accept **either**:
  - a `Bearer` header equal (constant-time) to the static `AuthConfig.Token`,
    or
  - a `Bearer` header passing `validateSessionToken`.

  A `Bearer` that fails both → `401`.

- No server-side logout endpoint (stateless); the web client drops the token.

### Web UI (`ui/src/`)

- `api.js`: add `login(username, password)` → `POST /api/v1/login` without an
  `Authorization` header; returns the token on success; the existing 401 path
  throws `unauthorized`.
- `App.jsx` `Login` component: two fields — username and password (password
  type) — submit calls `api.login`, then `setToken(result.token)` + `setToken`
  state; inline error message on failure. Existing `sessionStorage` key
  (`cronix_token`) and the rest of the app are unchanged.

### Wiring and docs

- `main.go`: read `CRONIX_USERNAME`/`CRONIX_PASSWORD` (defaults `admin`),
  `CRONIX_SESSION_TTL` (default `24h`), pass `AuthConfig{...}` to `NewServer`.
- `Makefile`: add `CRONIX_USERNAME`/`CRONIX_PASSWORD` (`$${...:-admin}`) to
  the `run` and `docker-run` targets.
- `README.md`: env table rows; REST API `/api/v1/login` row + example; Web UI
  section (username/password login); Security Note about default creds and the
  token-as-signing-key rotation caveat.

## Testing

- `auth_test.go` (new): token round-trip verifies the username; tampered
  signature rejected; expired token rejected; malformed token (no `.`, extra
  segments, bad base64) rejected.
- `api_test.go`: login success (token usable as Bearer on `/api/v1/jobs`);
  wrong username/password → `401`; malformed body → `400`; missing fields →
  `400`; static token still authorized; session token authorized; both wrong →
  `401`; session token expired → `401`. Update `newTestServer`/`NewServer`
  call sites to the `AuthConfig` signature.
- Frontend tests: no existing login-form tests; `api.login` lives in the
  untested `api.js` — no new frontend test required for this change.
- Gate: `go build ./...`, `go vet ./...`, `go test ./...` green, `gofmt -l .`
  clean. Rebuild `web/` (`npm --prefix ui run build`) so the new login UI is
  embedded.

## Out of scope

- No server-side session store, expiry beyond `exp` claim, revocation, or
  logout endpoint.
- No CSRF, rate limiting, or brute-force protection (documented as a proxy /
  external concern as today).
- No Basic-auth support for the API; CLI/auth headers unchanged.
- No multi-user support — a single configured username/password pair.