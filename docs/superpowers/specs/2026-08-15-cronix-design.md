# Cronix Design

Date: 2026-08-15
Status: Approved

## Overview

Cronix is a small Go application that runs cron-style scheduled jobs inside a
Docker container. Each job shells out to the `curl` binary (as a subprocess) to
make an HTTP call. Configuration comes entirely from environment variables,
read once at startup. The runtime image is `scratch` with only static binaries
— no shell, no OS packages, no libc.

## Goal / Success Criteria

- A user can run `docker run` with cron+curl config via env vars and the
  container fires the curl commands on schedule.
- Scheduled runs log a timestamped result line to stdout (visible via
  `docker logs`).
- The final image contains the Go binary, a statically-linked `curl`, and a CA
  bundle — nothing else.
- HTTPS API calls work out of the box (CA bundle wired up).

## Non-Goals

- No web UI or control surface.
- No runtime reload (config is read once at startup; restart to change).
- No retries, backoff, or alerting on failures.
- No shell features (`&&`, pipes, `$VAR` expansion, globs) inside curl
  commands — commands are parsed rather than run through a shell.

## Architecture

```
env vars ──> main.go ──> robfig/cron scheduler
                          │  (on each fire)
                          ▼
                  tokenizer (shlex) ──> exec curl subprocess ──> log result
```

Components:

1. **Job discovery (`config.go`)**
   - Iterate `i` from 0 upward, reading `CRON_SCHEDULE_<i>` and
     `CRON_CURL_<i>` pairs. Stop at the first missing schedule/gap.
   - `CRON_SCHEDULE_<i>`: standard 5-field cron expression.
   - `CRON_CURL_<i>`: full curl command string, e.g.
     `curl -s -X POST -H 'Content-Type: application/json' -d '{"foo":1}' https://example.com/api`.

2. **Validation (fail-fast at startup)**
   - Invalid cron expression → log error and exit non-zero.
   - Missing `CRON_CURL_<i>` for a present schedule → exit non-zero.
   - Unparseable command (bad quoting) → exit non-zero.
   - First token not `curl` → exit non-zero.
   - Scheduler uses container local time; timezone controlled via the
     standard `TZ` env var passed to the container.

3. **Scheduler (`scheduler.go`)**
   - `github.com/robfig/cron/v3` with the standard (5-field) parser.
   - Default policy: fires are concurrent (a slow job does not block the next
     fire of the same or other jobs).

4. **Runner (`runner.go`)**
   - Tokenize the command string with `github.com/google/shlex`.
   - Verify `tokens[0] == "curl"` (enforced again at runtime for safety; the
     curl binary path is `CURL_PATH`, default `/usr/local/bin/curl`).
   - `exec.Command(curlPath, tokens[1:]...)`, capture combined stdout+stderr.
   - Cap captured output at the last 4 KiB.

5. **Logging (`main.go`)**
   - One plain-text line per run written to stdout:
     `timestamp job=<i> schedule=<expr> exit=<code> output=<tail>`.
   - Non-zero exit → logged as a failure, no retry.

6. **Shutdown (`main.go`)**
   - On `SIGTERM`/`SIGINT`: stop the scheduler (no new fires), wait up to a
     bounded time (e.g. 10s) for in-flight runs, then exit 0.

## Docker Image

Multi-stage build with a static, `scratch` runtime.

1. **Build stage** — `golang:1.24-alpine` with build tools:
   - Compile `curl` **statically from source** from a pinned curl.se release
     with `--disable-shared --enable-static`, openssl static, zlib, and heavy
     trimming (`--disable-ldap --disable-ldaps --without-libidn2
     --without-librtmp --without-libpsl --without-nghttp2 --without-brotli`).
     Installs to `/out/bin/curl`.
   - Copy `ca-certificates.crt` out of the build stage.
   - Build the Go binary with `CGO_ENABLED=0 go build` → `/cronix`.

2. **Runtime stage** — `scratch`:
   - `COPY /out/bin/curl` → `/usr/local/bin/curl`
   - `COPY /cronix` → `/cronix`
   - `COPY /etc/ssl/certs/ca-certificates.crt` → `/etc/ssl/certs/ca-certificates.crt`
   - `ENV CURL_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt`
   - `ENTRYPOINT ["/cronix"]`

Final image ≈ static Go binary + static curl + CA bundle (~10–20 MB).

## Environment Variables

| Variable | Example | Required | Notes |
|---|---|---|---|
| `CRON_SCHEDULE_<i>` | `*/5 * * * *` | yes (per job) | 5-field cron |
| `CRON_CURL_<i>` | `curl -s https://example.com/api` | yes (per job) | full curl command; tokens[0] must be `curl` |
| `CURL_PATH` | `/usr/local/bin/curl` | no | curl binary path, default above |
| `TZ` | `America/New_York` | no | container timezone |

## Error Handling

- Startup validation errors abort the process with a non-zero exit and a clear
  log line naming the offending job index.
- A curl call that exits non-zero logs the result as a failure with the output
  tail; the scheduler continues unaffected.
- In-flight jobs are drained (bounded) on shutdown.

## Testing

- Unit tests:
  - `config_test.go` — discovery with indexed pairs, gap stops scanning,
    missing curl for a schedule, invalid cron expression.
  - `runner_test.go` — uses a fake executable in a temp dir (or `CURL_PATH`
    pointed at a fake `curl` script) to assert args, exit codes, and output
    truncation; quoting via shlex.
- Manual verification: build image, run with a schedule hitting a local test
  server (or `curl` to `https://example.com`), observe logs.