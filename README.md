# Cronix

A cron-style scheduler for HTTP jobs that runs in a Docker container. Jobs are
managed at runtime — through a CLI, a REST API, or a small web UI — and persist
to JSON files. Every job shells out to a statically-linked `curl` binary (with
TLS) to make an API call: no shell, no OS packages, no heavy runtimes. The
runtime image is built on `scratch` and contains exactly three things: the
Cronix binary, a statically-built `curl`, and a CA bundle.

## Project layout

The Go code lives under `cmd/` and `internal/`: `cmd/cronix` holds the
`main` package (entrypoint), and `internal/*` holds the packages it uses
(API, CLI, controller, store, runner, auth, SPA embed, ...). The UI source
lives under `ui/` (React + Vite) and builds into `internal/spa/dist/`,
which `go:embed` packs into the binary.

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
  -e CRONIX_USERNAME=admin \
  -e CRONIX_PASSWORD=admin \
  -v /tmp/cronix-data:/data \
  -p 8080:8080 \
  cronix

# add and list jobs through the CLI (executed inside the container)
docker exec cronix /cronix cli add --name ping --schedule '* * * * *' --curl 'curl -s https://example.com' --token sekrit
docker exec cronix /cronix cli list --token sekrit
```

On the first start the server logs that no jobs are configured; add one with
`/cronix cli add` or the web UI at `http://localhost:8080`.

## Jobs

A job is a JSON object stored in the jobs file. This is the full model:

```json
{
  "id": "3f8a1c2b0d4e",
  "name": "ping",
  "schedule": "* * * * *",
  "curl": "curl -s https://example.com",
  "retries": 0,
  "retry_delay": 5,
  "enabled": true
}
```

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Unique identifier. Must match `[A-Za-z0-9_-]{1,128}`; generated randomly if omitted. |
| `name` | string | Human-readable label (optional). |
| `schedule` | string | Standard 5-field cron expression (see [Cron Schedules](#cron-schedules)). |
| `curl` | string | The `curl` command to run. Must start with `curl` (see [Curl Commands](#curl-commands)). |
| `retries` | int | Extra attempts on failure (default `0`). Must be `>= 0`. |
| `retry_delay` | int | Seconds to wait between attempts (default `5`). Must be `>= 0`. |
| `enabled` | bool | Whether the job is scheduled (default `true`). |

`retries` and `retry_delay` are validated on create and update. An invalid
`schedule`, a `curl` command that does not start with `curl`, or a bad `id` is
rejected with a `400`.

### JSON Store

Jobs are persisted as a JSON array at `CRONIX_STORE_PATH`
(`/data/jobs.json` by default) and run history at `CRONIX_RUNS_PATH`
(`/data/runs.json`). Both are written atomically (temp file + rename) on every
change, so a crash mid-write never corrupts the store.

Use a volume so the data survives container restarts:

```sh
docker run --rm -d --name cronix \
  -e CRONIX_API_TOKEN=sekrit \
  -v my-cronix-data:/data \
  -p 8080:8080 \
  cronix
```

### Cron Schedules

Schedules are standard 5-field cron expressions, evaluated in the container's
local time:

```
minute hour day-of-month month day-of-week
```

| Field | Allowed values |
|---|---|
| minute | `0-59` |
| hour | `0-23` |
| day-of-month | `1-31` |
| month | `1-12` or `JAN-DEC` |
| day-of-week | `0-6` or `SUN-SAT` |

Supported syntax: ranges (`1-5`), lists (`1,3,5`), steps (`*/5`, `10-30/5`),
and names for months and weekdays. Examples:

| Expression | Meaning |
|---|---|
| `* * * * *` | Every minute |
| `*/5 * * * *` | Every 5 minutes |
| `0 9 * * *` | Daily at 09:00 |
| `0 0 * * 1-5` | Weekdays at midnight |
| `30 2 1 * *` | First of every month at 02:30 |

Set `TZ` to match your schedules:

```sh
docker run --rm -d --name cronix \
  -e CRONIX_API_TOKEN=sekrit \
  -e TZ=America/New_York \
  -v /tmp/cronix-data:/data \
  -p 8080:8080 \
  cronix
```

### Curl Commands

Each job runs one `curl` command. The command **must begin with `curl`**
followed by options; anything else is rejected on create/update.

- Arguments are parsed like a POSIX shell would: single quotes, double quotes,
  and backslash escapes are honored (e.g. quoted JSON bodies with spaces work).
- There is **no shell** involved: no `&&`, pipes, variable expansion (`$VAR`),
  redirects, or globs inside commands. Cronix tokenizes the string with
  `shlex` and `exec`s `curl` with the resulting arguments.
- `CURL_CA_BUNDLE` is preset so HTTPS certificate verification works out of
  the box.

Examples:

```sh
# simple GET
curl -s https://example.com

# POST JSON (note the single-quoted body)
curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"name":"Alice"}' https://api.example.com/users
```

### Retries and Run History

Each run makes `retries + 1` attempts. If an attempt exits non-zero, Cronix
waits `retry_delay` seconds and tries again, up to `retries` extra attempts;
the first attempt that exits `0` stops the run. Each attempt's combined
stdout+stderr is captured and capped at the last 4 KiB of output.

Every run — scheduled or manual — is recorded in the run history, newest
first, capped at the last **50 runs per job**. A run record looks like:

```json
{
  "job_id": "3f8a1c2b0d4e",
  "trigger": "scheduled",
  "time": "2026-08-17T12:34:56Z",
  "status": "failed",
  "exit_code": 6,
  "results": [
    {"attempt": 1, "total": 2, "exit": 6, "output": "curl: (6) Could not resolve host"},
    {"attempt": 2, "total": 2, "exit": 6, "output": "curl: (6) Could not resolve host"}
  ]
}
```

`trigger` is `scheduled` or `manual`; `status` is `ok`, `failed` (command ran
but exited non-zero after all attempts), or `error` (the command could not be
started). Deleting a job also deletes its run history.

## CLI

`/cronix cli <command> [flags] [args]`

Flags must precede the positional `<id>`: Go's `flag` package stops parsing at
the first non-flag argument, so `/cronix cli get --token sekrit <id>` works but
`/cronix cli get <id> --token sekrit` is a usage error.

Run it from your host with `docker exec <container> /cronix cli ...`.

### Common flags

| Flag | Default | Notes |
|---|---|---|
| `--addr` | `http://127.0.0.1:8080` | API base URL. |
| `--token` | `$CRONIX_API_TOKEN` | API bearer token. |

### Commands

| Command | Description |
|---|---|
| `list` | List jobs as a table. |
| `get <id>` | Show one job as JSON (includes `last_run` summary). |
| `add` | Create a job; prints `created <id>`. |
| `update <id>` | Replace a job (partial: only flags you set change). |
| `delete <id>` | Delete a job; prints `deleted <id>`. |
| `enable <id>` | Resume a disabled job. |
| `disable <id>` | Pause a job (stops firing). |
| `run <id>` | Run a job now; prints one line per attempt. |
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

### Examples

```sh
# list all jobs
docker exec cronix /cronix cli list --token sekrit

# add a job that runs every 5 minutes
docker exec cronix /cronix cli add \
  --name healthcheck \
  --schedule '*/5 * * * *' \
  --curl 'curl -s https://example.com/health' \
  --retries 2 --retry-delay 10 \
  --token sekrit
# → created <id>

# add a job with an explicit id
docker exec cronix /cronix cli add --id ping1 --name ping \
  --schedule '* * * * *' --curl 'curl -s https://example.com' --token sekrit

# get one job
docker exec cronix /cronix cli get --token sekrit ping1

# update only the schedule (other fields preserved)
docker exec cronix /cronix cli update --schedule '0 9 * * *' --token sekrit ping1

# pause and resume
docker exec cronix /cronix cli disable --token sekrit ping1
docker exec cronix /cronix cli enable --token sekrit ping1

# run now and see the attempts
docker exec cronix /cronix cli run --token sekrit ping1
# → run job=ping1 name=ping schedule="0 9 * * *" command="curl -s https://example.com"
#     attempt 1/1 exit=0
#       output: (no output)
#     result: OK

# delete
docker exec cronix /cronix cli delete --token sekrit ping1
```

`cli run` always exits `0` when the API call itself succeeds, even if the job's
command failed — the failure is shown in the output (`result: FAILED`).

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
Errors return `{"error": "..."}` with status `401` (bad/missing credentials),
`400` (validation), `404` (not found), or `500` (storage/other).

### Endpoints

| Method & Path | Description | Success |
|---|---|---|
| `POST /api/v1/login` | Log in with the configured username/password; returns a session token. | `200` `{"token":"..."}` |
| `GET /api/v1/jobs` | List all jobs (each includes `last_run` summary). | `200` JSON array |
| `POST /api/v1/jobs` | Create a job. | `201` created job |
| `GET /api/v1/jobs/{id}` | Get one job (includes `last_run`). | `200` job |
| `PUT /api/v1/jobs/{id}` | Replace a job (full body). | `200` updated job |
| `DELETE /api/v1/jobs/{id}` | Delete a job. | `204` empty |
| `GET /api/v1/jobs/{id}/runs` | List a job's run history, newest first (capped at 50). | `200` `{"runs":[{...}]}` |
| `POST /api/v1/jobs/{id}/run` | Run a job now. | `200` `{"steps":[{...}]}` |

### Job body

Create and update take the job body; fields are validated the same way as the
CLI (`id` pattern, parseable cron, `curl`-prefixed command, non-negative
`retries`/`retry_delay`):

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

`PUT` replaces the job entirely (unchanged fields must be re-sent); `POST` with
no `id` generates one.

### Run response

`POST /api/v1/jobs/{id}/run` returns the attempts performed, one `steps`
object per attempt — `200` even if the command failed, so callers can inspect
the output:

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

# log in and capture a session token
curl -s -X POST -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin"}' \
  "$BASE/api/v1/login"
# → {"token":"<session token>"} — use it as Authorization: Bearer below

# list jobs
curl -s -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/jobs"

# create a job
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"ping","schedule":"* * * * *","curl":"curl -s https://example.com"}' \
  "$BASE/api/v1/jobs"

# get one job
curl -s -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/jobs/<id>"

# update a job
curl -s -X PUT -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"ping","schedule":"*/5 * * * *","curl":"curl -s https://example.com","enabled":true}' \
  "$BASE/api/v1/jobs/<id>"

# run a job now and see the attempts
curl -s -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/jobs/<id>/run"

# run history
curl -s -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/jobs/<id>/runs"

# delete
curl -s -X DELETE -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/jobs/<id>"
# → 204
```

## Web UI

Get the UI at `/` from the host: `http://localhost:8080`. Sign in with the
configured username and password (default `admin`/`admin`). On success the UI
stores a signed session token in session storage and uses it for the REST API.

### Dashboard (`/`)

- **Stat cards** at the top summarize your jobs: Total, Enabled, Healthy, and
  Failing.
- **Filter jobs** by typing in the search box (matches name, id, schedule).
- **New job** opens the editor to create a job.
- Each row shows the job name/id, schedule, last-run status and relative time,
  a status badge, and an enable/disable toggle.
- Row actions: **run** (fires now and shows the attempts inline), **edit**
  (opens the editor), and **delete** (asks for confirmation).
- Clicking a row opens the job detail page.

### Job detail (`/jobs/:id`)

- Job card with schedule, retries, retry delay, enable/disable toggle, and the
  full curl command (with a copy button).
- Actions: **Run now**, **Edit**, **Delete**.
- **History** tab lists past runs (newest first) with trigger, relative time,
  status dot, and exit code; click a run to expand its attempt-by-attempt
  output.
- **Run output** tab shows the output of the most recent "run now".

### Notes

- A light/dark **theme toggle** is in the header; the choice is remembered in
  `localStorage`.
- The session token lives in `sessionStorage` (`cronix_token`) and is cleared
  by **Sign out**; closing the tab signs you out automatically.
- Client-side routes (`/jobs/:id`) reload fine — the server serves the SPA
  shell for any path that is not a real asset or API route.

The UI is a small static single-page app embedded into the binary via
`go:embed` (in `internal/spa/spa.go`, serving the built
`internal/spa/dist/` directory).

## Environment Variables

| Variable | Default | Notes |
|---|---|---|
| `CRONIX_API_TOKEN` | — | **Required.** Bearer token for the REST API and web UI. The server refuses to start without it. |
| `CRONIX_USERNAME` | `admin` | Username for the web UI login. |
| `CRONIX_PASSWORD` | `admin` | Password for the web UI login. |
| `CRONIX_SESSION_TTL` | `24h` | Lifetime of a web session token. Unparseable values silently fall back to the default. |
| `CRONIX_STORE_PATH` | `/data/jobs.json` | Path to the jobs store. The directory is created if missing. |
| `CRONIX_RUNS_PATH` | `/data/runs.json` | Path to the run-history store. Keeps the last 50 runs per job. |
| `CRONIX_HTTP_ADDR` | `:8080` | Address the HTTP server listens on. |
| `CURL_PATH` | `/usr/local/bin/curl` | Path to the `curl` binary used to run jobs. |
| `TZ` | container local time | Timezone the cron scheduler uses. Set e.g. `TZ=America/New_York`. |

### Migration note: `CRON_*` is gone

Cronix v1 configured jobs through `CRON_SCHEDULE_<i>` / `CRON_CURL_<i>` env
variables. These **no longer exist**. Jobs are now first-class objects stored at
`CRONIX_STORE_PATH` and managed via the CLI, REST API, or web UI. To migrate, add
each env-configured job once with `/cronix cli add` (or the API) and mount the
store volume for persistence.

## Logs

Every scheduled fire produces a timestamped line on stdout, visible with
`docker logs`:

```
2026/08/16 10:40:00 job=3f8a1c2b0d4e name=ping schedule="* * * * *" attempt=1/1 exit=0 output=
```

Field-by-field: `job` id, optional `name`, cron `schedule`, `attempt=<n>/<total>`,
`exit` code, and `output` (last 4 KiB of combined stdout+stderr). Disabled jobs
do not fire.

Manual runs (`cli run`, the web UI "Run now", or `POST /run`) log a fuller
block per run:

```
run job=ping1 name=ping schedule="0 9 * * *" command="curl -s https://example.com"
  attempt 1/2 exit=6
    output: curl: (6) Could not resolve host
  attempt 2/2 exit=6
    output: curl: (6) Could not resolve host
  result: FAILED (last exit=6, 2/2 attempts)
```

`result:` is `OK`, `FAILED` (exhausted retries with a non-zero exit), or
`ERROR` (the command could not be started).

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

## License

MIT
