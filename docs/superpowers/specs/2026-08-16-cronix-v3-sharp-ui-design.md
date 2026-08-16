# Cronix v3 — Sharp UI Redesign

**Date:** 2026-08-16
**Status:** Approved (brainstorming session)
**Branch:** `feat/live-scheduler`

## Summary

Redesign the Cronix web UI from the current plain GitHub-style table into a modern
SaaS dashboard: layered neutrals, crisp borders, dot + pill status indicators,
tabular numerics, a stat strip, and a dedicated job detail page. Pure hand-rolled
CSS design system — no Tailwind, no component library, no icon library. No backend
changes.

## Goals

- Sharper visual identity: higher-contrast neutrals, refined spacing/radius/shadow,
  deliberate typography.
- Multi-surface layout: list dashboard → dedicated `/jobs/:id` detail page with the
  first real navigation (react-router).
- Better data visibility: stat strip, status pills with tinted backgrounds, colored
  status dots, run history + run output as separate tabs.
- Keep constraints from v3: React 18 + Vite 5 + vitest 2 only, committed `web/`
  build, Go suite untouched and green, tiny embedded bundle.

## Non-Goals

- No backend/API changes of any kind.
- No new runtime dependencies beyond `react-router-dom`.
- No animation framework, no data-fetching framework, no state library.
- No beta/experimental design; light and dark themes both polished.

## Design Decisions (from brainstorming)

| Topic | Decision |
|-------|----------|
| Direction | Modern SaaS dashboard (Linear/Notion quality) |
| Scope | Polish + small layout upgrade (stat strip + detail page) |
| Detail surface | Dedicated `/jobs/:id` page via `react-router-dom` |
| Summary stats | Stat strip computed from the existing list response (no backend change) |
| Navigation | BrowserRouter with real URLs, back button works |
| Styling | Hand-rolled CSS design-token system (option A) |

## Architecture

### File layout

- `ui/src/App.jsx` — shell only: token gate, theme state, auth state, error helper,
  `BrowserRouter` with routes `/` (Dashboard) and `/jobs/:id` (JobDetail). Unknown
  routes redirect to `/`.
- `ui/src/views/Dashboard.jsx` — stat strip, filter toolbar, job table, empty states.
- `ui/src/views/JobDetail.jsx` — job card (meta, enabled switch, actions, command
  block), history/output tabs, inline JobEditor, not-found/loading states.
- `ui/src/components/` — `StatCard.jsx`, `Badge.jsx`, `StatusDot.jsx`, `Button.jsx`,
  `JobEditor.jsx`.
- `ui/src/lib.js` — add pure `summarizeJobs(jobs)`:
  `{ total, enabled, healthy, failing }` where healthy = `last_run.status === 'ok'`,
  failing = status `failed` or `error`. `relativeTime`/`filterJobs` unchanged.
- `ui/src/style.css` — the design system (Section: Design tokens).

### State flow

- Token + theme live in `App` (no change to how they persist: sessionStorage token,
  localStorage theme).
- `Dashboard` owns `jobs` + `refresh()`; `JobDetail` loads its own job via
  `getJob(id)` and history via `listRuns(id)`, refreshing both after edit/run/delete.
- Shared errors: auth 401 clears the token → login screen (extracted from current
  App.jsx `handleError` into a shared helper used by both views).

### Data flow

- All data from existing endpoints: list, get, `/runs`, run, create, update, delete.
- Stats computed client-side from the list response.
- Route `:id` drives `getJob`/`listRuns` on the detail page.

## Design tokens & visual language

### Color roles

```css
:root { /* light */
  --bg: #f6f7f9;
  --bg-subtle: #eef0f3;
  --bg-raised: #ffffff;
  --bg-hover: #f2f3f5;

  --text: #171c23;
  --text-secondary: #50575f;
  --text-muted: #8b939c;

  --border-subtle: #e4e7ec;
  --border: #d3d7de;
  --border-strong: #aeb4bd;

  --accent: #2b5cf0;
  --accent-hover: #2453d8;
  --accent-soft: rgba(43, 92, 240, 0.1);

  --ok: #16a34a;   --ok-soft: rgba(22, 163, 74, 0.12);
  --warn: #d97706; --warn-soft: rgba(217, 119, 6, 0.12);
  --fail: #dc2626; --fail-soft: rgba(220, 38, 38, 0.12);

  --ring: rgba(43, 92, 240, 0.35);
}
[data-theme='dark'] { /* dark: deep near-black, higher-contrast */
  --bg: #0b0e13;
  --bg-subtle: #141821;
  --bg-raised: #131720;
  --bg-hover: #1a1f2a;

  --text: #eef1f6;
  --text-secondary: #b6bdc8;
  --text-muted: #7c8794;

  --border-subtle: #232934;
  --border: #2c3441;
  --border-strong: #3d4655;

  --accent: #5b8cff;
  --accent-hover: #74a0ff;
  --accent-soft: rgba(91, 140, 255, 0.14);

  --ok: #34d399;   --ok-soft: rgba(52, 211, 153, 0.14);
  --warn: #fbbf24; --warn-soft: rgba(251, 191, 36, 0.14);
  --fail: #f87171; --fail-soft: rgba(248, 113, 113, 0.14);

  --ring: rgba(91, 140, 255, 0.4);
}
```

### Typography, shape, depth, motion

- System font stack (unchanged base), `--mono` stack for schedule/curl/output.
- Tabular numerics (`font-variant-numeric: tabular-nums`) for timestamps, counts,
  exit codes.
- Muted table headers: 12px, 0.04em letter-spacing, uppercase.
- Radius: `--radius-sm 6px`, `--radius-md 10px`, `--radius-lg 14px`.
- Shadow: layered subtle shadow on cards and popovers; page and stat strip flat.
- Hairline `--border-subtle` between table rows.
- Motion: 120ms transitions on hover/focus only.

### Status identity

- Status dots: 8px circles, `--ok`/`--warn`/`--fail`/`--text-muted` (never-run).
- Pills: tinted background (`--ok-soft` etc.) + status-colored text; never = muted.
- Messaging: `last_run` with status + exit code tooltip (relative time).

## Page layouts

### App shell

- `App.jsx` renders either the login card or a `BrowserRouter` provider.
- Theme toggle persists `data-theme` on `<html>` (existing mechanism unchanged).

### Dashboard (`/`)

- Sticky header bar: wordmark "Cronix" (left), theme toggle + sign-out (right).
- Stat strip: 4 `StatCard`s (Total, Enabled, Healthy, Failing) in a responsive
  grid from `summarizeJobs`. Empty state: cards show 0; table area shows a
  centered "No jobs yet" panel with a New-job CTA.
- Toolbar: filter input (search icon + clear) left, "New job" primary button right.
- Job table columns:
  - Job: name + muted mono id
  - Schedule: mono
  - Last run: dot + relative time (+ absolute in tooltip)
  - Status: pill (ok / failed / error / never)
  - Toggle: animated pill switch (green when on)
  - Actions: run, edit, delete (text/icon buttons)
- Row hover `--bg-hover`; failing jobs get a red-tinted left edge.
- Row click → `/jobs/:id`; action buttons stop propagation.
- Filter empty result: "No jobs match '<query>'" muted panel.

### Job detail (`/jobs/:id`)

- Back link ("← Jobs"), then responsive two-column grid (single column on narrow).
- Left column — job card:
  - Header: name (h1) + status pill, mono id below.
  - Meta rows: schedule (mono), retries, retry delay.
  - Enabled switch.
  - Action row: Run now (primary), Edit, Delete (danger + confirm).
  - Command block: `--bg-subtle` mono block with a copy button.
- Right column — tabs: History | Run output.
  - History: newest-first rows — status dot, trigger (scheduled/manual), relative
    time (+ absolute tooltip), exit code; click expands the run's attempt steps
    inline (current HistoryPanel content, restyled).
  - Run output: most recent run's steps (attempt/exit/output blocks);
    empty state "Run a job to see output."
- Edit: `JobEditor` replaces the card body inline; Save/Cancel; save updates via
  `updateJob`, then refreshes job + history.
- Delete: confirm → `deleteJob` → navigate back to `/`.
- States: muted "Loading…" while fetching; "Job not found" panel with back link.
- Inline error banner (tinted `--fail` background + Retry) for failed loads.

## Components

- `Badge` — status pill (ok/failed/error/never), tinted bg + colored text.
- `StatusDot` — colored 8px dot.
- `Button` — variants `primary`/`ghost`/`danger`; hamburger-free, inline SVG icons
  where an icon is needed (sun/moon, search, copy, arrow-left, refresh).
- `JobEditor` — refactor of current `Editor` with new styling and inline form error.

## Error handling

- 401 anywhere → clear token → login screen (shared helper).
- API failures: inline tinted banner with Retry.
- Job missing on detail: "Job not found" + back link.
- Form validation errors (invalid cron / non-curl command) surface as the existing
  inline form error from the API error body.

## Scope guards

- No Tailwind, no component library, no icon library (inline SVG only).
- No backend/API changes.
- No changes to Go source, tests, Dockerfile, or README beyond anything strictly
  needed (expected: none).
- `relativeTime`, `filterJobs` unchanged; only new lib function is `summarizeJobs`.
- `TestSpaServesReactIndex` and the Go suite stay green.

## Testing

- vitest: `summarizeJobs` — empty list; all-never; mixed ok/failed/error; enabled
  vs total accounting.
- vitest: existing `relativeTime`/`filterJobs` untouched, still passing.
- `npm --prefix ui run build` clean; rebuilt `web/` committed.
- Full Go gate (`go build ./... && go vet ./... && go test ./... -count=1`,
  `gofmt -l .`) green.
- Optional manual E2E: run container, verify stat strip, navigation, tabs, theme.