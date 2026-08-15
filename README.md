# Cronix

Run cron-style scheduled HTTP jobs in a Docker container. Every job shells out
to the `curl` binary to make an API call — no shell, no OS packages, no heavy
runtimes. The runtime image is built on `scratch` and contains exactly three
things: the Cronix binary, a statically-linked `curl` (with TLS), and a CA
bundle.

```
docker pull iashraful/cronix
```

## Features

- Standard 5-field cron expressions, one job per env-var pair.
- Each job is a full `curl` command — URL, method, headers, JSON body, anything
  `curl` supports.
- `curl` is a fully static binary built from source with TLS (mbedTLS); HTTPS
  works out of the box.
- Configurable retries with backoff delay on failure.
- Timestamped results to stdout for `docker logs`.
- Graceful shutdown: drains in-flight jobs, then exits cleanly.
- Scratch runtime: ~3 MB final image. No shell, no libc, nothing to patch.
- Multi-arch friendly (arm64/amd64 static binaries).
- Set the timezone once with `TZ` and schedules follow it.

## Quick Start

```sh
docker run --rm \
  -e CRON_SCHEDULE_0='*/5 * * * *' \
  -e 'CRON_CURL_0=curl -s https://example.com/api/health' \
  iashraful/cronix
```

Every 5 minutes the container runs `curl -s https://example.com/api/health`
and logs the result:

```
2026/08/15 10:40:00 cronix started with 1 job(s)
2026/08/15 10:40:00 job=0 schedule="*/5 * * * *" attempt=1/1 exit=0 output=ok
```

## Environment Variables

Jobs are numbered with an index `i` starting at `0`. Discovery stops at the
first missing `CRON_SCHEDULE_<i>` (a gap). A schedule without its matching
curl command is a startup error and the container exits non-zero.

| Variable | Example | Required | Notes |
|---|---|---|---|
| `CRON_SCHEDULE_<i>` | `*/5 * * * *` | yes (per job) | Standard 5-field cron: `min hour dom month dow` |
| `CRON_CURL_<i>` | `curl -s https://example.com/api` | yes (per job) | Full curl command. Must start with `curl`. Quoted arguments are supported; shell features are not. |
| `CRON_RETRIES_<i>` | `3` | no | Extra attempts on failure. Default `0` (no retries). |
| `CRON_RETRY_DELAY_<i>` | `10` | no | Delay in seconds between retries. Default `5`. |
| `CURL_PATH` | `/usr/local/bin/curl` | no | Override the curl binary path in the image. |
| `TZ` | `America/New_York` | no | Container timezone. `CRON_SCHEDULE_*` uses local time. |

Cron field reference (standard 5-field):

| Field | Allowed |
|---|---|
| minute | `0-59` |
| hour | `0-23` |
| day of month | `1-31` |
| month | `1-12` or `JAN-DEC` |
| day of week | `0-6` (0 = Sunday) or `SUN-SAT` |

Supported syntax: `*`, ranges (`1-5`), lists (`1,15,30`), steps (`*/15`,
`1-30/5`), and named months/weekdays.

### Platform / arch

Cronix binaries and curl are built statically, so the same tag runs on
`linux/amd64` and `linux/arm64`. Docker handles the selection automatically.

## Configuration Examples

### POST a JSON body with headers

```sh
docker run --rm \
  -e CRON_SCHEDULE_0='0 0 * * *' \
  -e 'CRON_CURL_0=curl -s -X POST -H "Content-Type: application/json" -d "{\"event\":\"daily\",\"source\":\"cronix\"}" https://api.example.com/webhooks/ingest' \
  iashraful/cronix
```

> Keep commands single-quoted in the shell so Docker receives them verbatim;
> use double quotes for arguments inside the command itself. Backslash-escape
> characters as you would on the command line.

### Multiple jobs

```sh
docker run --rm \
  -e CRON_SCHEDULE_0='*/5 * * * *' \
  -e 'CRON_CURL_0=curl -s https://api.example.com/health' \
  -e CRON_SCHEDULE_1='0 9 * * 1-5' \
  -e 'CRON_CURL_1=curl -s https://api.example.com/reports/daily' \
  iashraful/cronix
```

### Retry a flaky endpoint

```sh
docker run --rm \
  -e CRON_SCHEDULE_0='*/10 * * * *' \
  -e 'CRON_CURL_0=curl -s https://api.example.com/fragile' \
  -e CRON_RETRIES_0=3 \
  -e CRON_RETRY_DELAY_0=15 \
  iashraful/cronix
```

On failure Cronix waits 15s and tries again, up to 3 extra attempts. Every
attempt is logged (`attempt=1/4`, `2/4`, ...); when all fail the job is logged
as failed and the scheduler keeps going.

### Timezone-aware schedule

```sh
docker run --rm \
  -e TZ='Europe/Berlin' \
  -e CRON_SCHEDULE_0='0 8 * * 1-5' \
  -e 'CRON_CURL_0=curl -s https://api.example.com/office/open' \
  iashraful/cronix
```

This job fires at 08:00 Berlin time, Monday to Friday.

## Using with docker compose

`compose.yaml`:

```yaml
services:
  cronix:
    image: iashraful/cronix
    restart: unless-stopped
    environment:
      TZ: America/New_York
      CRON_SCHEDULE_0: "*/5 * * * *"
      CRON_CURL_0: 'curl -s https://api.example.com/health'
      CRON_RETRIES_0: "2"
```

```sh
docker compose up -d
docker compose logs -f cronix
```

## Command Syntax and Limits

- The command **must begin with `curl`** followed by options — anything else is
  rejected at startup.
- Arguments are parsed like a POSIX shell would: single quotes, double quotes,
  and backslash escapes are honored. For example, JSON bodies with spaces and
  quotes work as long as you quote them.
- There is **no shell** involved. That means no `&&`, no pipes, no variable
  expansion (`$VAR`), no redirects, and no globs inside commands. Cronix
  tokenizes the string and `exec`s `curl` with the resulting arguments.
- Configuration is read once at startup. Restart the container to apply
  changes.
- Schedules use the container's local time; change it with `TZ`.

## Logs

Each run attempt produces one line on stdout, visible with `docker logs`:

```
<timestamp> job=<index> schedule="<cron>" attempt=<n>/<total> exit=<code> output=<last 4 KiB of combined output>
```

You can pipe these anywhere: filetail, `docker logs --follow`, a log shipper,
or simply watch with `watch docker logs <container>`.

## Exit Behavior

| Trigger | Behavior |
|---|---|
| Invalid config (bad cron, missing curl, non-curl command) | Logs an error, exits non-zero before scheduling |
| No jobs at all | Logs error, exits non-zero |
| Job failure (even after retries) | Logged as failure; scheduler continues |
| `SIGTERM` / `SIGINT` (e.g. `docker stop`) | Stops scheduling, waits up to 10s for in-flight jobs, exits 0 |

## Security Notes

- The container runs as root and the image is intentionally minimal. For
  elevated hardening, run with `--user`, e.g.
  `docker run --user 65534:65534 ...` — curl needs no special privileges.
- Commands come from your configuration and are trusted input by design. There
  is no shell, so there is no shell-injection surface beyond what a regular
  `curl` invocation allows.
- The static curl is built from a pinned, checksum-verified release with
  trimming of unused features (LDAP, HTTP/2, brotli, etc.).
- `CURL_CA_BUNDLE` is preset so HTTPS certificate verification works out of
  the box.

## Building from Source

```sh
git clone <your-repo-url>
cd cronix
docker build -t iashraful/cronix .
```

The `Dockerfile` compiles `curl` statically (pinned version + SHA256) in a
`golang:1.24-alpine` build stage and copies only the resulting binary, the
Cronix binary, and certificates into `scratch`. The first build takes a few
minutes to compile curl; later builds reuse the cache.

### Tests

```sh
go test ./...
```

Runs unit tests for config parsing (job discovery, validation, retries) and the
runner (argument passing, retry/backoff, output truncation) using a fake curl.

## How It Works

1. On startup Cronix reads `CRON_SCHEDULE_<i>` / `CRON_CURL_<i>` pairs from
   the environment, validates each schedule and command, and fails fast on any
   error.
2. Each valid job is registered with a 5-field cron scheduler
   (`robfig/cron`).
3. On every fire, Cronix tokenizes the command, `fork`s `curl` with the
   parsed arguments, and captures combined stdout+stderr (last 4 KiB).
4. A non-zero exit triggers a retry after the configured delay (if any).
5. The result line is written to stdout.

## License

MIT