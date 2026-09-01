# Cronix Feature Ideas

## Scheduling & Execution

- [ ] **Webhook triggers** -- allow jobs to be triggered via incoming HTTP POST (not just cron)
- [ ] **Job chaining** -- run job B after job A succeeds (DAG/pipeline support)
- [ ] **Concurrency limits** -- max N jobs running at once, queue the rest
- [ ] **Timeout per job** -- kill curl if it takes too long (currently no timeout)
- [ ] **Custom commands beyond curl** -- optionally allow arbitrary executables/scripts

## Observability

- [ ] **Run statistics/aggregation** -- p50/p95/p99 latencies, success rate over time
- [ ] **Export run history** -- CSV/JSON download from the UI or API
- [ ] **Webhooks for notifications** -- send Slack/Discord/email alerts on failure
- [ ] **Prometheus/Grafana metrics** -- expose `/metrics` endpoint

## Persistence & Scale

- [ ] **SQLite or PostgreSQL backend** -- replace JSON files for better durability and queryability
- [ ] **Multi-tenant support** -- namespaces or API keys per user/team
- [ ] **Job tags/labels** -- organize and filter jobs by category

## UI Improvements

- [ ] **Gantt/timeline view** -- visualize job schedules and overlaps
- [ ] **Inline cron builder** -- visual cron expression editor instead of raw syntax
- [ ] **Run history charts** -- success/failure rate sparklines per job
- [ ] **Dark mode improvements** -- more accessible contrast, system preference detection

## DevOps & Deployment

- [ ] **Helm chart / Kubernetes manifests** -- for k8s deployment
- [ ] **Systemd service file** -- run natively without Docker
- [ ] **Health check endpoint** -- `/healthz` for orchestrator readiness
- [ ] **Config file support** -- YAML/TOML config instead of only env vars
- [ ] **Hot-reload config** -- restart or reconfigure without downtime

## Security

- [ ] **RBAC** -- role-based access (admin vs read-only vs operator)
- [ ] **Audit log** -- track who created/modified/deleted jobs
- [ ] **Rate limiting** on the API
- [ ] **CORS configuration** -- for external integrations

## Quick Wins

- [ ] Add a job timeout (context with `time.AfterFunc`)
- [ ] Add a `/healthz` endpoint (trivial)
- [ ] Add a `--config` flag for YAML config
- [ ] Add run history export (API + UI button)
