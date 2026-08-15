# Cronix

Runs cron-style scheduled jobs in a Docker container. Each job shells out to the
`curl` binary to make an HTTP call. Runtime image is `scratch` with static
binaries only — no shell, no OS packages.

## Usage

Build:

```sh
docker build -t cronix .
```

Run:

```sh
docker run --rm \
  -e CRON_SCHEDULE_0='*/5 * * * *' \
  -e 'CRON_CURL_0=curl -s https://example.com/api/health' \
  cronix
```

## Environment Variables

| Variable | Example | Required | Notes |
|---|---|---|---|
| `CRON_SCHEDULE_<i>` | `*/5 * * * *` | yes (per job) | standard 5-field cron |
| `CRON_CURL_<i>` | `curl -s https://example.com/api` | yes (per job) | full curl command; must start with `curl`; quoted args supported, shell features are not |
| `CRON_RETRIES_<i>` | `3` | no | extra attempts on failure, default `0` |
| `CRON_RETRY_DELAY_<i>` | `10` | no | seconds between retries, default `5` |
| `CURL_PATH` | `/usr/local/bin/curl` | no | curl binary path |
| `TZ` | `America/New_York` | no | container timezone |

Jobs are discovered by index from `0` until a gap. A schedule without its curl
command is a startup error. Config is read once at startup only.

## Logs

One line per attempt on stdout, visible via `docker logs`:

```
2026/08/15 12:00:00 job=0 schedule="*/5 * * * *" attempt=1/1 exit=0 output=...
```

Non-zero final exit is logged as a failure; the scheduler continues.
