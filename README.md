# Cronix

A cron-style scheduler for HTTP jobs that runs in a Docker container. Jobs are
managed at runtime — through a CLI, a REST API, or a small web UI — and persist
to a JSON file. Every job shells out to a statically-linked `curl` binary (with
TLS) to make an API call: no shell, no OS packages, no heavy runtimes. The
runtime image is built on `scratch` and contains exactly three things: the
Cronix binary, a statically-built `curl`, and a CA bundle.

## Modes

The image's entrypoint is `/cronix`. It runs in one of two modes:

- **Server** (`/cronix`): loads persisted jobs, starts the scheduler and the
  HTTP API, and serves the web UI. Requires `CRONIX_API_TOKEN`.
- **CLI** (`/cronix cli <command> ...`): a client that talks to the server's
  REST API. Exits `0` on success, `1` on usage errors, and `2` on API errors.

## Quick Start

```sh
# scaffold a data directory and run the server
mkdir -p /tmp/cronix-data
docker run --rm -d --name cronix \
  -e CRONIX_API_TOKEN=sekrit \
  -v /tmp/cronix-data:/data \
  -p 8080:8080 \
  cronix

# add and list jobs through the CLI (executed inside the container)
docker exec cronix /cronix cli add --name ping --schedule '* * * * *' --curl 'curl -s https://example.com' --token sekrit
docker exec cronix /cronix cli list --token sekrit
```

On the first start the server logs that no jobs are configured; add one with
`/cronix cli add` or the web UI at `http://localhost:8080`.

## Environment Variables

| Variable | Default | Notes |
|---|---|---|
| `CRONIX_API_TOKEN` | — | **Required.** Bearer token for the REST API and web UI. The server refuses to start without it. |
| `CRONIX_USERNAME` | `admin` | Username for the web UI login. |
| `CRONIX_PASSWORD` | `admin` | Password for the web UI login. |
| `CRONIX_SESSION_TTL` | `24h` | Lifetime of a web session token. Unparseable values silently fall back to the default. |
| `CRONIX_STORE_PATH` | `/data/jobs.json` | Path to the JSON store. The directory is created if missing. |
| `CRONIX_RUNS_PATH` | `/data/runs.json` | Path to the run-history store. Keeps the last 50 runs per job. |
| `CRONIX_HTTP_ADDR` | `:8080` | Address the HTTP server listens on. |
| `CURL_PATH` | `/usr/local/bin/curl` | Path to the `curl` binary used to run jobs. |
| `TZ` | container local time | Timezone the cron scheduler uses. Set e.g. `TZ=America/New_York` (see below). |

### Timezone

Schedules are evaluated in the container's local time, so set `TZ` to match your
schedules:

```sh
docker run --rm -d --name cronix \
  -e CRONIX_API_TOKEN=sekrit \
  -e TZ=America/New_York \
  -v /tmp/cronix-data:/data \
  -p 8080:8080 \
  cronix
```

### Migration note: `CRON_*` is gone

Cronix v1 configured jobs through `CRON_SCHEDULE_<i>` / `CRON_CURL_<i>` env
variables. These **no longer exist**. Jobs are now first-class objects stored at
`CRONIX_STORE_PATH` and managed via the CLI, REST API, or web UI. To migrate, add
each env-configured job once with `/cronix cli add` (or the API) and mount the
store volume for persistence.

## JSON Store

Jobs are persisted as a JSON array at `CRONIX_STORE_PATH`
(`/data/jobs.json` by default):

```json
[
  {
    "id": "3f8a1c2b0d4e",
    "name": "ping",
    "schedule": "* * * * *",
    "curl": "curl -s https://example.com",
    "retries": 0,
    "retry_delay": 5,
    "enabled": true
  }
]
```

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Unique identifier (`[A-Za-z0-9_-]{1,128}`); generated if omitted. |
| `name` | string | Human-readable label. |
| `schedule` | string | Standard 5-field cron expression. |
| `curl` | string | The `curl` command to run. Must start with `curl`. |
| `retries` | int | Extra attempts on failure (default `0`). |
| `retry_delay` | int | Seconds between retries (default `5`). |
| `enabled` | bool | Whether the job is scheduled (default `true`). |

Use a volume so the store survives container restarts:

```sh
docker run --rm -d --name cronix \
  -e CRONIX_API_TOKEN=sekrit \
  -v my-cronix-data:/data \
  -p 8080:8080 \
  cronix
```

## CLI

`/cronix cli <command> [flags] [args]`

Flags must precede the positional `<id>`: Go's `flag` package stops parsing at
the first non-flag argument, so `/cronix cli get --token sekrit <id>` works but
`/cronix cli get <id> --token sekrit` is a usage error.

Common flags:

| Flag | Default | Notes |
|---|---|---|
| `--addr` | `http://127.0.0.1:8080` | API base URL. |
| `--token` | `$CRONIX_API_TOKEN` | API bearer token. |

Run it from your host with `docker exec <container> /cronix cli ...`, e.g.
`docker exec cronix /cronix cli list --token sekrit`.

### Commands

| Command | Description |
|---|---|
| `list [--addr URL] [--token TOKEN]` | List jobs as a table. |
| `get [--addr URL] [--token TOKEN] <id>` | Show one job as JSON. |
| `add [flags]` | Create a job; prints `created <id>`. |
| `update [flags] <id>` | Replace a job (partial: only flags you set change). |
| `delete [--addr URL] [--token TOKEN] <id>` | Delete a job; prints `deleted <id>`. |
| `enable [--addr URL] [--token TOKEN] <id>` | Resume a disabled job. |
| `disable [--addr URL] [--token TOKEN] <id>` | Pause a job (stops firing). |
| `run [--addr URL] [--token TOKEN] <id>` | Run a job now; prints one line per attempt. |
| `help` | Print usage. |

`add`/`update` flags:

| Flag | Notes |
|---|---|
| `--id` | Job id (add only; generated if empty). |
| `--name` | Job name. |
| `--schedule` | 5-field cron expression (**required** for `add`). |
| `--curl` | `curl` command (**required** for `add`). |
| `--retries` | Extra attempts on failure (default `0`). |
| `--retry-delay` | Seconds between retries (default `5`). |
| `--enabled` | Enabled flag (default `true`). |

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success. |
| `1` | Usage error (bad arguments or unknown command) — `cronix cli` with no command also prints usage and exits `1`. |
| `2` | API error (network failure, non-2xx response). |

## REST API

`BASE = http://HOST:8080`. All `/api/v1/*` routes require
`Authorization: Bearer <token>` (constant-time compared); otherwise `401`. The
token is either the static `CRONIX_API_TOKEN` or a session token from
`POST /api/v1/login`.
Errors return `{"error": "..."}` with status `400` (validation), `404`
(not found), or `500` (storage/other).

| Method & Path | Description | Success |
|---|---|---|
| `POST /api/v1/login` | Log in with the configured username/password; returns a session token. | `200` `{"token":"..."}` |
| `GET /api/v1/jobs` | List all jobs. | `200` JSON array |
| `POST /api/v1/jobs` | Create a job. | `201` created job |
| `GET /api/v1/jobs/{id}` | Get one job. | `200` job |
| `PUT /api/v1/jobs/{id}` | Replace a job (full body). | `200` updated job |
| `DELETE /api/v1/jobs/{id}` | Delete a job. | `204` empty |
| `GET /api/v1/jobs/{id}/runs` | List a job's run history, newest first (capped at 50). | `200` `{"runs":[{...}]}` |
| `POST /api/v1/jobs/{id}/run` | Run a job now. | `200` `{"steps":[{...}]}` |

Job body (create and update):

```json
{
  "name": "ping",
  "schedule": "* * * * *",
  "curl": "curl -s https://example.com",
  "retries": 2,
  "retry_delay": 10,
  "enabled": true
}
```

`POST /run` returns the attempts performed, for example:

```json
{
  "steps": [
    {"attempt": 1, "total": 1, "exit": 0, "output": ""}
  ]
}
```

### Examples

```sh
TOKEN=sekrit
BASE=http://127.0.0.1:8080

curl -s -X POST -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin"}' \
  "$BASE/api/v1/login"
# → {"token":"<session token>"} — use it as Authorization: Bearer below

curl -s -H "Authorization: Bearer $TOKEN"        "$BASE/api/v1/jobs"
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"ping","schedule":"* * * * *","curl":"curl -s https://example.com"}' \
  "$BASE/api/v1/jobs"
curl -s -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/jobs/<id>/run"
```

## Web UI

Get the UI at `/` from the host: `http://localhost:8080`. Sign in with the
configured username and password (default `admin`/`admin`). On success the UI
stores a signed session token in session storage and uses it for the REST API;
then create, edit, enable/disable, run, and delete jobs as before. The UI is a
small static single-page app embedded into the binary via
`go:embed` (in `spa.go`, serving the built `web/` directory).

The UI source lives in `ui/` (Vite + React). Rebuild it with `make ui` (runs
`npm ci` and the production build). For development, `npm --prefix ui run dev`
starts the Vite dev server with a `/api` proxy to `http://127.0.0.1:8080`, so
you get hot reload while the running server serves the API.

## Command Syntax and Limits

- Each job's command **must begin with `curl`** followed by options — anything
  else is rejected on create/update.
- Arguments are parsed like a POSIX shell would: single quotes, double quotes,
  and backslash escapes are honored (e.g. quoted JSON bodies with spaces work).
- There is **no shell** involved: no `&&`, pipes, variable expansion (`$VAR`),
  redirects, or globs inside commands. Cronix tokenizes the string with
  `shlex` and `exec`s `curl` with the resulting arguments.
- On a non-zero exit Cronix retries after `retry_delay` seconds, up to `retries`
  extra attempts. Each attempt is logged and capped at the last 4 KiB of output.

## Logs

Every scheduled fire produces a timestamped line on stdout, visible with
`docker logs`:

```
2026/08/16 10:40:00 job=3f8a1c2b0d4e schedule="* * * * *" attempt=1/1 exit=0 output=
```

Field-by-field: `job` id, cron `schedule`, `attempt=<n>/<total>`, `exit` code,
and `output` (last 4 KiB of combined stdout+stderr). Disabled jobs do not fire.

## Exit Behavior

| Trigger | Behavior |
|---|---|
| Missing `CRONIX_API_TOKEN` / invalid persisted job | Logs an error, exits non-zero before scheduling. |
| Job failure (even after retries) | Logged as failure; scheduler and API keep running. |
| `SIGTERM` / `SIGINT` (e.g. `docker stop`) | Stops scheduling, waits up to 10s for in-flight jobs, exits. |

## Security Notes

- The REST API is protected by a single bearer token (`CRONIX_API_TOKEN`), sent
  as `Authorization: Bearer`. There is no TLS inside the container; put a proxy
  in front for anything beyond localhost.
- The web UI logs in with a single configured username/password pair
  (`CRONIX_USERNAME`/`CRONIX_PASSWORD`). The defaults are `admin`/`admin` — set
  real credentials for anything beyond local use. Session tokens are
  HMAC-signed with the `CRONIX_API_TOKEN`, so rotating the API token invalidates
  all issued web sessions.
- The container runs as root and the image is intentionally minimal. For
  elevated hardening, run with `--user` (e.g. `docker run --user 65534:65534`)
  — curl needs no special privileges.
- Curl commands are trusted input by design. There is no shell, so no
  shell-injection surface beyond what a regular `curl` invocation allows.
- The static curl is built from a pinned, checksum-verified release with
  trimming of unused features (LDAP, HTTP/2, brotli, etc.).
- `CURL_CA_BUNDLE` is preset so HTTPS certificate verification works out of
  the box.

## Building from Source

```sh
git clone <your-repo-url>
cd cronix
docker build -t cronix .
```

The `Dockerfile` compiles `curl` statically (pinned version + SHA256) in a
`golang:1.24-alpine` build stage and copies only the resulting binary, the
Cronix binary, and certificates into `scratch`. A `node:22-alpine` stage runs
`npm ci` + `vite build` and copies the output into the Go stage, so the image
embeds the UI. The first build takes a few minutes to compile curl; later
builds reuse the cache.

The built `web/` assets are committed, so a plain `go build` needs no Node
toolchain; only the Docker image (or `make ui`) invokes npm.

### Tests

```sh
go test ./...
```

Runs unit tests for the controller, store, runner, CLI, API, and SPA handlers.

## License

MIT