# Sharp UI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the Cronix web UI into a modern SaaS dashboard: a hand-rolled CSS design system, a stat strip, a polished job table, and a dedicated job-detail page with history/output tabs, navigating with react-router.

**Architecture:** `App.jsx` shrinks to a shell (token gate, theme, auth, router). New `views/Dashboard.jsx` (stat strip + filtered table) and `views/JobDetail.jsx` (`/jobs/:id` with tabs) hold the data logic. Presentational components live in `components/`. All styles move into a design-token system in `style.css`. No backend changes; statistics derive from the existing list API.

**Tech Stack:** React 18.3, Vite 5, vitest 2, `react-router-dom` (new runtime dep). Hand-rolled CSS (no Tailwind, no component/icon library).

## Global Constraints

- No Go source, Go test, Dockerfile, or README changes. `spa.go` unchanged.
- Only new dependency: `react-router-dom` (pin `^6.28.0`). Everything else stays at the versions in `ui/package.json`.
- React 18.3.1, Vite >=5.4, vitest >=2.1.8. Do not change other versions.
- Inline SVG icons only — no icon library.
- Demo/dev host note: macOS. `npm --prefix ui` works from `/Users/ashraful/workspace/Cronix`. `node_modules` is gitignored.
- Built `web/` output is COMMITTED (Go scratch builds need no Node); every task that changes `ui/src` must rebuild `web/` via `npm --prefix ui run build` and commit it.
- Gate before every commit: `npm --prefix ui test` green, `npm --prefix ui run build` clean, full Go suite green (`go build ./... && go vet ./... && go test ./... -count=1`), `gofmt -l .` clean (Go untouck changed, but run to be safe).
- Job JSON shape used across tasks: `{ id, name, schedule, curl, retries, retry_delay, enabled, last_run? }` where `last_run = { status: "ok"|"failed"|"error", exit_code, time } | null`. Runs response: `{ runs: [ { job_id, trigger: "manual"|"scheduled", time, status, exit_code, results: [ { attempt, total, exit, output } ] } ] }`.
- Status mapping: `ok`→green, `error`→amber (`--warn`), `failed`→red, no last_run→muted ("never").

---

### Task 1: `summarizeJobs` pure helper

**Files:**
- Modify: `ui/src/lib.js` (add export)
- Test: `ui/src/lib.test.js`

**Interfaces:**
- Consumes: nothing.
- Produces: `summarizeJobs(jobs) -> { total, enabled, healthy, failing }` — healthy = `last_run.status === 'ok'`; failing = status `failed` or `error`; disabled jobs still count toward healthy/failing (status is about last run, not enabled state). Used by Task 4's stat strip.

- [ ] **Step 1: Write the failing tests**

Append to `ui/src/lib.test.js`:

```js
describe('summarizeJobs', () => {
  it('returns zeroed stats for empty input', () => {
    expect(summarizeJobs([])).toEqual({ total: 0, enabled: 0, healthy: 0, failing: 0 })
  })

  it('counts total and enabled', () => {
    const jobs = [
      { id: 'a', enabled: true },
      { id: 'b', enabled: false },
      { id: 'c', enabled: true },
    ]
    expect(summarizeJobs(jobs)).toEqual({ total: 3, enabled: 2, healthy: 0, failing: 0 })
  })

  it('classifies healthy as ok and failing as failed/error regardless of enabled', () => {
    const jobs = [
      { id: 'a', enabled: true, last_run: { status: 'ok' } },
      { id: 'b', enabled: false, last_run: { status: 'failed' } },
      { id: 'c', enabled: true, last_run: { status: 'error' } },
      { id: 'd', enabled: false, last_run: null },
    ]
    expect(summarizeJobs(jobs)).toEqual({ total: 4, enabled: 2, healthy: 1, failing: 2 })
  })

  it('tolerates jobs that lack last_run entirely', () => {
    expect(summarizeJobs([{ id: 'x', enabled: true }])).toEqual({ total: 1, enabled: 1, healthy: 0, failing: 0 })
  })
})
```

Update the import line `import { filterJobs, relativeTime } from './lib'` to add `summarizeJobs`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm --prefix ui test`
Expected: FAIL — `summarizeJobs is not a function` (or "does not provide an export named 'summarizeJobs'").

- [ ] **Step 3: Implement the helper**

Append to `ui/src/lib.js`:

```js
export function summarizeJobs(jobs) {
  const list = Array.isArray(jobs) ? jobs : []
  let enabled = 0
  let healthy = 0
  let failing = 0
  for (const j of list) {
    if (j.enabled) enabled++
    const status = j.last_run && j.last_run.status
    if (status === 'ok') healthy++
    else if (status === 'failed' || status === 'error') failing++
  }
  return { total: list.length, enabled, healthy, failing }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm --prefix ui test`
Expected: PASS — all four new `summarizeJobs` cases plus existing `relativeTime`/`filterJobs` green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/lib.js ui/src/lib.test.js
git commit -m "feat: add summarizeJobs helper for dashboard stats"
```

---

### Task 2: Design-system stylesheet

**Files:**
- Modify: `ui/src/style.css` (full replacement)
- Test: `ui/src/style.test.js` (new — verifies tokens/classes exist so the design system stays intact)

**Interfaces:**
- Consumes: nothing.
- Produces: the CSS class vocabulary every later task styles against. Class contracts (later tasks assume these exact class names):
  - Layout/app: `.app`, `.page-shell`, `.header`, `.header-spacer`, `.main`, `.wide`, `.grid-detail`
  - Buttons: `.btn`, `.btn.primary`, `.btn.danger`, `.btn.ghost`, `.btn.sm`, `.btn.icon`
  - Inputs/forms: `.input`, `.field`, `.field.check`, `.toggle` (pill switch), `.toggle.on`
  - Stats: `.stats`, `.stat`, `.stat-label`, `.stat-value`, `.stat-value.tone-ok`, `.tone-fail`, `.tone-muted`
  - Table: `.tbl`, `.tbl th`, `.tbl td`, `.tbl .row-click`, `.tbl .row-fail`
  - Status: `.pill`, `.pill.ok`, `.pill.failed`, `.pill.error`, `.pill.never`, `.dot`, `.dot.ok`, `.dot.failed`, `.dot.error`, `.dot.never`
  - Detail page: `.back`, `.card`, `.card-head`, `.card-meta`, `.cmd-block`, `.tabs`, `.tab`, `.tab.active`, `.run-item`, `.run-item.open`, `.run-meta`, `.attempts`, `.attempt`
  - Messaging: `.banner` (tinted error), `.banner .retry`, `.empty`, `.loading`, `.notfound`

- [ ] **Step 1: Write the failing CSS-presence test**

Create `ui/src/style.test.js`:

```js
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const css = readFileSync(new URL('./style.css', import.meta.url), 'utf8')

function checkClass(sel) {
  return css.includes(sel)
}

describe('design system', () => {
  it('defines both theme token blocks', () => {
    expect(css).toContain(':root')
    expect(css).toContain(`[data-theme='dark']`)
  })

  it('defines required semantic roles', () => {
    for (const tok of ['--bg', '--bg-raised', '--text', '--text-muted', '--border-subtle', '--accent', '--ok', '--fail', '--ring', '--radius-md', '--mono']) {
      expect(css, `missing token ${tok}`).toContain(tok)
    }
  })

  it('defines component classes used by the views', () => {
    const classes = [
      '.btn.primary', '.btn.danger', '.stats', '.stat-label', '.tbl', '.pill.ok',
      '.dot.failed', '.toggle.on', '.tabs', '.tab.active', '.run-item', '.banner', '.cmd-block',
    ]
    for (const c of classes) expect(css, `missing class ${c}`).toContain(c)
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm --prefix ui test`
Expected: FAIL — `style.css` currently lacks `.btn.primary`, `.stats`, etc.

- [ ] **Step 3: Replace `ui/src/style.css`**

Write this full file to `ui/src/style.css` (replaces the current 85 lines). Copy verbatim — later CSS is assumed, not repeated, in later tasks:

```css
:root {
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

  --ok: #16a34a;
  --ok-soft: rgba(22, 163, 74, 0.12);
  --warn: #d97706;
  --warn-soft: rgba(217, 119, 6, 0.12);
  --fail: #dc2626;
  --fail-soft: rgba(220, 38, 38, 0.12);

  --ring: rgba(43, 92, 240, 0.35);

  --radius-sm: 6px;
  --radius-md: 10px;
  --radius-lg: 14px;
  --shadow: 0 1px 2px rgba(16, 24, 40, 0.05), 0 4px 12px rgba(16, 24, 40, 0.06);
  --mono: ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, monospace;
}

[data-theme='dark'] {
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

  --ok: #34d399;
  --ok-soft: rgba(52, 211, 153, 0.14);
  --warn: #fbbf24;
  --warn-soft: rgba(251, 191, 36, 0.14);
  --fail: #f87171;
  --fail-soft: rgba(248, 113, 113, 0.14);

  --ring: rgba(91, 140, 255, 0.4);

  --shadow: 0 1px 2px rgba(0, 0, 0, 0.4), 0 4px 12px rgba(0, 0, 0, 0.35);
}

* { box-sizing: border-box; }

body {
  margin: 0;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif;
  font-variant-numeric: tabular-nums;
  background: var(--bg);
  color: var(--text);
}

.app { min-height: 100vh; display: flex; flex-direction: column; }
.page-shell { min-height: 100vh; display: flex; flex-direction: column; }

/* ---- Header ---- */
.header {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 24px;
  background: color-mix(in srgb, var(--bg) 85%, transparent);
  backdrop-filter: blur(8px);
  border-bottom: 1px solid var(--border-subtle);
}
.header .brand { font-size: 18px; font-weight: 700; letter-spacing: -0.02em; margin: 0 auto 0 0; }
.header .spacer, .header-spacer { flex: 1; }

.main { width: 100%; max-width: 1080px; margin: 0 auto; padding: 24px; }
.wide { width: 100%; max-width: 1280px; margin: 0 auto; padding: 24px; }

/* ---- Buttons ---- */
.btn {
  appearance: none;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 7px 14px;
  font-size: 14px;
  font-family: inherit;
  color: var(--text);
  background: var(--bg-raised);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition: background 120ms, border-color 120ms, color 120ms, box-shadow 120ms;
}
.btn:hover { border-color: var(--border-strong); }
.btn:focus-visible { outline: none; box-shadow: 0 0 0 3px var(--ring); }
.btn.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
.btn.primary:hover { background: var(--accent-hover); border-color: var(--accent-hover); }
.btn.danger { color: var(--fail); }
.btn.danger:hover { background: var(--fail-soft); border-color: var(--fail); }
.btn.ghost { background: transparent; }
.btn.sm { padding: 4px 9px; font-size: 13px; }
.btn.icon { padding: 4px 8px; }

/* ---- Inputs ---- */
.input {
  width: 100%;
  padding: 7px 10px;
  font-size: 14px;
  font-family: inherit;
  color: var(--text);
  background: var(--bg-raised);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}
.input:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--ring); }

.field { display: flex; flex-direction: column; gap: 5px; margin-bottom: 14px; }
.field > label { font-size: 13px; color: var(--text-secondary); }
.field .error-text { font-size: 13px; color: var(--fail); }
.error-text { color: var(--fail); }

.field.check { flex-direction: row; align-items: center; gap: 10px; }

/* Toggle switch */
.toggle {
  position: relative;
  width: 40px;
  height: 22px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--bg-subtle);
  cursor: pointer;
  flex: none;
  transition: background 120ms, border-color 120ms;
}
.toggle::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--text-muted);
  transition: transform 120ms, background 120ms;
}
.toggle.on { background: var(--accent); border-color: var(--accent); }
.toggle.on::after { transform: translateX(18px); background: #fff; }

/* ---- Stats ---- */
.stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 14px; margin-bottom: 24px; }
.stat {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 16px 18px;
  background: var(--bg-raised);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
}
.stat-label { font-size: 12px; text-transform: uppercase; letter-spacing: 0.04em; color: var(--text-muted); }
.stat-value { font-size: 28px; font-weight: 700; letter-spacing: -0.02em; }
.stat-value.tone-ok { color: var(--ok); }
.stat-value.tone-fail { color: var(--fail); }
.stat-value.tone-muted { color: var(--text-muted); }

/* ---- Toolbar ---- */
.toolbar { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; }
.toolbar .search { position: relative; flex: 1; max-width: 320px; }
.toolbar .search svg { position: absolute; left: 10px; top: 50%; transform: translateY(-50%); color: var(--text-muted); }
.toolbar .search .input { padding-left: 32px; }

/* ---- Table ---- */
.tbl { width: 100%; border-collapse: collapse; }
.tbl th, .tbl td {
  padding: 12px 14px;
  text-align: left;
  border-bottom: 1px solid var(--border-subtle);
  vertical-align: middle;
}
.tbl th { font-size: 12px; text-transform: uppercase; letter-spacing: 0.04em; color: var(--text-muted); }
.tbl tbody tr.row-click { cursor: pointer; transition: background 120ms; }
.tbl tbody tr.row-click:hover { background: var(--bg-hover); }
.tbl tbody tr.row-fail td:first-child { box-shadow: inset 3px 0 0 var(--fail); }
.tbl .mono { font-family: var(--mono); font-size: 13px; }
.tbl .actions { display: flex; gap: 6px; }

/* ---- Status pills & dots ---- */
.pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
}
.pill.ok { background: var(--ok-soft); color: var(--ok); }
.pill.failed { background: var(--fail-soft); color: var(--fail); }
.pill.error { background: var(--warn-soft); color: var(--warn); }
.pill.never { background: var(--bg-subtle); color: var(--text-muted); }

.dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; flex: none; }
.dot.ok { background: var(--ok); }
.dot.failed { background: var(--fail); }
.dot.error { background: var(--warn); }
.dot.never { background: var(--text-muted); }

/* ---- Shared meta ---- */
.mono { font-family: var(--mono); }
.muted { color: var(--text-muted); }
.row { display: flex; align-items: center; gap: 10px; }

/* ---- Detail page ---- */
.back { display: inline-flex; align-items: center; gap: 6px; margin-bottom: 16px; color: var(--text-secondary); text-decoration: none; font-size: 14px; }
.back:hover { color: var(--text); }

.grid-detail { display: grid; grid-template-columns: minmax(0, 3fr) minmax(0, 4fr); gap: 20px; align-items: start; }
@media (max-width: 860px) { .grid-detail { grid-template-columns: 1fr; } }

.card {
  background: var(--bg-raised);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow);
  padding: 20px;
}
.card-head { display: flex; align-items: flex-start; gap: 14px; margin-bottom: 14px; }
.card-head h1 { margin: 0; font-size: 22px; letter-spacing: -0.02em; }
.card-head .mono { font-size: 12px; color: var(--text-muted); margin-top: 2px; }
.card-meta { display: grid; grid-template-columns: 1fr 1fr; gap: 10px 18px; margin-bottom: 16px; }
.card-meta .k { font-size: 12px; color: var(--text-muted); }
.card-meta .v { font-family: var(--mono); font-size: 14px; margin-top: 2px; }
.card-actions { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 16px; }

.cmd-block {
  background: var(--bg-subtle);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  padding: 12px 14px;
  font-family: var(--mono);
  font-size: 13px;
  overflow-x: auto;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
}
.cmd-block pre { margin: 0; font-family: inherit; font-size: inherit; }

/* ---- Tabs ---- */
.tabs { display: flex; gap: 4px; border-bottom: 1px solid var(--border-subtle); margin-bottom: 16px; }
.tab {
  padding: 8px 14px;
  font-size: 14px;
  color: var(--text-secondary);
  background: none;
  border: none;
  border-bottom: 2px solid transparent;
  cursor: pointer;
}
.tab:hover { color: var(--text); }
.tab.active { color: var(--text); border-bottom-color: var(--accent); font-weight: 600; }

/* ---- Run history / output ---- */
.run-item {
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  padding: 10px 14px;
  margin-bottom: 8px;
  background: var(--bg-raised);
  cursor: pointer;
  transition: background 120ms;
}
.run-item:hover { background: var(--bg-hover); }
.run-item.open { border-color: var(--accent); background: var(--accent-soft); }
.run-meta { display: flex; align-items: center; gap: 10px; font-size: 14px; }
.run-meta .rel { color: var(--text-muted); font-size: 13px; }
.run-meta .exit { font-family: var(--mono); font-size: 13px; color: var(--text-secondary); margin-left: auto; }
.attempts { margin-top: 10px; padding-left: 18px; border-top: 1px dashed var(--border-subtle); padding-top: 8px; }
.attempt { font-family: var(--mono); font-size: 13px; padding: 4px 0; color: var(--text-secondary); }
.attempt .out { color: var(--text); }

/* ---- Messaging ---- */
.banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 14px;
  border: 1px solid var(--fail);
  background: var(--fail-soft);
  color: var(--fail);
  border-radius: var(--radius-sm);
  margin-bottom: 16px;
  font-size: 14px;
}
.banner .retry { flex: none; }
.empty {
  text-align: center;
  padding: 48px 16px;
  color: var(--text-muted);
  background: var(--bg-raised);
  border: 1px dashed var(--border);
  border-radius: var(--radius-md);
  font-size: 14px;
}
.loading { color: var(--text-muted); padding: 24px; }
.notfound { text-align: center; padding: 64px 16px; }
.notfound h2 { margin: 0 0 8px; }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm --prefix ui test`
Expected: PASS — the design-system test plus all existing lib tests.

- [ ] **Step 5: Rebuild and commit**

```bash
npm --prefix ui run build && git add ui/src/style.css ui/src/style.test.js web/
```

Note: rebuilding `web/` will change the hashed bundle; commit those changes too. (If Task 1 did not change the bundle, `web/` may be unchanged from HEAD — that's fine; `git add web/` stages whatever the build changed.)

```bash
git commit -m "style: add hand-rolled design-system tokens and base components CSS"
```

---

### Task 3: Shared presentational components

**Files:**
- Create: `ui/src/components/Button.jsx`, `ui/src/components/Badge.jsx`, `ui/src/components/StatusDot.jsx`, `ui/src/components/StatCard.jsx`
- Create: `ui/src/components/JobEditor.jsx`

**Interfaces:**
- Consumes: `filterJobs`, `relativeTime` (Task 1, existing), `api` module.
- Produces (exact props — later tasks rely on these):
  - `Button({ variant='ghost'|'primary'|'danger', size='md'|'sm', href?, children, ...rest })`
  - `Badge({ lastRun })` — renders the status pill; `lastRun` = null → never pill; label for failed: `failed <exit_code>`.
  - `StatusDot({ status })` — status `'ok'|'failed'|'error'|null` → `.dot` with matching class (null → `never`).
  - `StatCard({ label, value, tone })` — tone `'ok'|'fail'|'muted'|undefined`.
  - `JobEditor({ job, error, onSave, onCancel, submitLabel })` — form fields name, schedule, curl, retries, retry_delay, enabled checkbox; `onSave(form)` receives `{...job, name, schedule, curl, retries, retry_delay, enabled}` with `id` preserved when present.

- [ ] **Step 1: Verify the baseline before implementing**

There are no component-level unit tests in this project (no jsdom/testing-library dependency); components are verified through the vitest lib tests, the build, and the live E2E (Task 7). Confirm the baseline is green:

Run: `npm --prefix ui test && go test -run 'TestSpaServesReactIndex' ./...`
Expected: PASS (both) — establishes the baseline before Task 4 rewires the entrypoint.

- [ ] **Step 2: Implement components**

Create `ui/src/components/Button.jsx`:

```jsx
export default function Button({ variant = 'ghost', size = 'md', href, className = '', type = 'button', children, ...rest }) {
  const cls = `btn ${variant} ${size === 'sm' ? 'sm' : ''} ${className}`.trim()
  if (href) {
    return (
      <a className={cls} href={href} {...rest}>
        {children}
      </a>
    )
  }
  return (
    <button type={type} className={cls} {...rest}>
      {children}
    </button>
  )
}
```

Note: `Button` forwards `type` (needed by `JobEditor`'s submit button) and supports `href` for link-style usage.

Create `ui/src/components/StatusDot.jsx`:

```jsx
export default function StatusDot({ status }) {
  const cls = status === 'ok' || status === 'failed' || status === 'error' ? status : 'never'
  return <span aria-hidden="true" className={`dot ${cls}`} />
}
```

Create `ui/src/components/Badge.jsx`:

```jsx
import StatusDot from './StatusDot'

export default function Badge({ lastRun }) {
  if (!lastRun) {
    return <span className="pill never"><StatusDot status={null} />never</span>
  }
  const label = lastRun.status === 'failed'
    ? `failed ${lastRun.exit_code}`
    : lastRun.status
  return (
    <span className={`pill ${lastRun.status}`}>
      <StatusDot status={lastRun.status} />
      {label}
    </span>
  )
}
```

Create `ui/src/components/StatCard.jsx`:

```jsx
export default function StatCard({ label, value, tone }) {
  return (
    <div className="stat">
      <span className="stat-label">{label}</span>
      <span className={`stat-value ${tone ? `tone-${tone}` : ''}`}>{value}</span>
    </div>
  )
}
```

Create `ui/src/components/JobEditor.jsx`:

```jsx
import { useState } from 'react'
import Button from './Button'

const empty = {
  name: '',
  schedule: '*/5 * * * *',
  curl: '',
  retries: 0,
  retry_delay: 5,
  enabled: true,
}

export default function JobEditor({ job, error, onSave, onCancel, submitLabel = 'Save' }) {
  const [form, setForm] = useState({ ...empty, ...job })
  const set = (k) => (e) => setForm({ ...form, [k]: e.target.value })
  const setNum = (k) => (e) => setForm({ ...form, [k]: Number(e.target.value) })
  return (
    <form
      className="card"
      onSubmit={(e) => {
        e.preventDefault()
        onSave(form)
      }}
    >
      <h2>{job && job.id ? 'Edit job' : 'New job'}</h2>
      <div className="field">
        <label htmlFor="ed-name">Name</label>
        <input id="ed-name" className="input" value={form.name} placeholder="ping example" onChange={set('name')} />
      </div>
      <div className="field">
        <label htmlFor="ed-schedule">Schedule (5-part cron)</label>
        <input id="ed-schedule" className="input" value={form.schedule} placeholder="*/5 * * * *" onChange={set('schedule')} />
      </div>
      <div className="field">
        <label htmlFor="ed-curl">Curl command</label>
        <input id="ed-curl" className="input" value={form.curl} placeholder="curl -s https://example.com" onChange={set('curl')} />
      </div>
      <div className="field">
        <label htmlFor="ed-retries">Retries</label>
        <input id="ed-retries" className="input" type="number" min="0" value={form.retries} onChange={setNum('retries')} />
      </div>
      <div className="field">
        <label htmlFor="ed-retry-delay">Retry delay (seconds)</label>
        <input id="ed-retry-delay" className="input" type="number" min="0" value={form.retry_delay} onChange={setNum('retry_delay')} />
      </div>
      <div className="field check">
        <input id="ed-enabled" type="checkbox" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} />
        <label htmlFor="ed-enabled">Enabled</label>
      </div>
      {error && <p className="error-text">{error}</p>}
      <div className="row">
        <Button variant="primary" type="submit">{submitLabel}</Button>
        <Button type="button" onClick={onCancel}>Cancel</Button>
      </div>
    </form>
  )
}
```

- [ ] **Step 3: Run tests + build gate**

Run: `npm --prefix ui test && npm --prefix ui run build && go test ./... -count=1`
Expected: all green (no component files are referenced by the entrypoint yet, so the bundle is unchanged; Go suite untouched).

- [ ] **Step 4: Commit**

```bash
git add ui/src/components
git commit -m "feat: add shared Button, Badge, StatusDot, StatCard, and JobEditor components"
```

`web/` will show no diff because nothing imports the components yet — that's expected; commit only `ui/src/components`.

---

### Task 4: Dashboard view

**Files:**
- Create: `ui/src/views/Dashboard.jsx`
- Create: `ui/src/views/JobDetail.jsx` (placeholder — real content lands in Task 5; Task 4's route import needs a stub)
- Modify: `ui/src/App.jsx` (full rewrite into the router shell)
- Modify: `ui/package.json` (add react-router-dom)

**Interfaces:**
- Consumes: `api` (listJobs, createJob, updateJob, deleteJob, runJob), `lib` (`filterJobs`, `summarizeJobs`, `relativeTime`), components (Button, Badge, StatusDot, StatCard, JobEditor), `react-router-dom` (`Link`, `useNavigate`).
- Produces:
  - `Dashboard` — self-contained list view (stat strip, toolbar, table, inline editor). Owns its `jobs` state, `refresh()`, `editing`, `runPanel`, `error`, `query`. Renders its own sticky header (brand, theme toggle, sign-out).
  - `App` — shell: token gate (login form), theme state, auth handling (401 → clear token), `BrowserRouter` with routes `/` (Dashboard) and `/jobs/:id` (JobDetail). Renders header + `<main>` wrapper. Theme is passed down as `theme` + `toggleTheme` prop to Dashboard; token state owned here.
  - `JobDetail` (stub for now): renders `<div className="loading">Job detail coming soon</div>`.

- [ ] **Step 1: Install react-router-dom**

Run: `npm --prefix ui install react-router-dom@^6.28.0`
Expected: adds `react-router-dom` (and `@remix-run/router`) to `ui/package.json` + lockfile.

- [ ] **Step 2: Write the failing SPA test**

The SPA test (`TestSpaServesReactIndex`) currently passes against the old bundle. After this task the bundle will be re-generated with the new routes. The RED gate for this task is the existing test against a **built-with-new-views** bundle — write the App shell first (Step 3), build, then confirm:

Run: `npm --prefix ui run build && go test -run 'TestSpaServesReactIndex' ./...`
Expected: PASS (the React bundle still embeds and serves; the check is `#root` + `/assets/` which remain true). This is a smoke gate, not a red-green toggle — the real coverage for routing/views is the manual E2E in Task 7. Proceed to implementation.

- [ ] **Step 3: Write `ui/src/views/Dashboard.jsx`**

```jsx
import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import * as api from '../api'
import { filterJobs, relativeTime, summarizeJobs } from '../lib'
import Button from '../components/Button'
import Badge from '../components/Badge'
import StatusDot from '../components/StatusDot'
import StatCard from '../components/StatCard'
import JobEditor from '../components/JobEditor'

const emptyJob = {
  name: '',
  schedule: '*/5 * * * *',
  curl: '',
  retries: 0,
  retry_delay: 5,
  enabled: true,
}

function RunPanel({ name, steps, onClose }) {
  return (
    <div className="card">
      <div className="row" style={{ justifyContent: 'space-between', marginBottom: 10 }}>
        <h2 style={{ margin: 0 }}>Run output — {name}</h2>
        <Button onClick={onClose}>Close</Button>
      </div>
      {steps.map((s, i) => (
        <div key={i} className="attempt">
          <span>attempt {s.attempt}/{s.total} exit={s.exit}</span>
          <div className="out">{s.output || '(no output)'}</div>
        </div>
      ))}
    </div>
  )
}

export default function Dashboard({ theme, onToggleTheme, logout }) {
  const navigate = useNavigate()
  const [jobs, setJobs] = useState([])
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [editing, setEditing] = useState(null)
  const [runPanel, setRunPanel] = useState(null)

  const handleError = (e) => {
    if (e.message === 'unauthorized') {
      api.setToken('')
      logout()
      return
    }
    setError(e.message)
  }

  const refresh = useMemo(
    () => () =>
      api.listJobs()
        .then((j) => {
          setJobs(Array.isArray(j) ? j : [])
          setError('')
        })
        .catch(handleError),
    [],
  )

  useEffect(() => {
    refresh()
  }, [refresh])

  const shown = filterJobs(jobs, query)
  const stats = summarizeJobs(jobs)

  const save = (form) => {
    const body = { ...form }
    delete body.id
    delete body.last_run
    const p = form.id
      ? api.updateJob(form.id, body)
      : api.createJob(body)
    p.then(() => {
      setEditing(null)
      setError('')
      refresh()
    }).catch(handleError)
  }

  const toggle = (j) =>
    api.updateJob(j.id, { ...j, enabled: !j.enabled })
      .then(refresh)
      .catch(handleError)

  const remove = (j) => {
    if (!window.confirm(`Delete job ${j.name || j.id}?`)) return
    api.deleteJob(j.id)
      .then(refresh)
      .catch(handleError)
  }

  const runNow = (j) =>
    api.runJob(j.id)
      .then((r) => {
        setRunPanel({ name: j.name || j.id, steps: (r && r.steps) || [] })
        refresh()
      })
      .catch(handleError)

  return (
    <>
      <div className="stats">
        <StatCard label="Total" value={stats.total} tone="muted" />
        <StatCard label="Enabled" value={stats.enabled} tone="muted" />
        <StatCard label="Healthy" value={stats.healthy} tone="ok" />
        <StatCard label="Failing" value={stats.failing} tone={stats.failing > 0 ? 'fail' : 'muted'} />
      </div>

      <div className="toolbar">
        <div className="search">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <circle cx="11" cy="11" r="7" />
            <line x1="21" y1="21" x2="16.5" y2="16.5" />
          </svg>
          <input
            className="input"
            value={query}
            placeholder="Filter jobs..."
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        <Button variant="primary" onClick={() => setEditing({ ...emptyJob })}>New job</Button>
      </div>

      {error && (
        <div className="banner">
          <span>{error}</span>
          <Button variant="ghost" size="sm" className="retry" onClick={refresh}>Retry</Button>
        </div>
      )}

      {editing && (
        <JobEditor
          key={editing.id || 'new'}
          job={editing}
          error={error}
          onSave={save}
          onCancel={() => setEditing(null)}
        />
      )}

      {!editing && jobs.length === 0 && !error && (
        <div className="empty">
          <p style={{ margin: '0 0 16px' }}>No jobs yet. Create one to start scheduling.</p>
          <Button variant="primary" onClick={() => setEditing({ ...emptyJob })}>New job</Button>
        </div>
      )}

      {!editing && jobs.length > 0 && shown.length === 0 && (
        <div className="empty">No jobs match '{query}'</div>
      )}

      {!editing && shown.length > 0 && (
        <table className="tbl">
          <thead>
            <tr>
              <th>Job</th>
              <th>Schedule</th>
              <th>Last run</th>
              <th>Status</th>
              <th>Enabled</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((j) => (
              <tr
                key={j.id}
                className={`row-click ${j.last_run && (j.last_run.status === 'failed' || j.last_run.status === 'error') ? 'row-fail' : ''}`}
                onClick={() => navigate(`/jobs/${j.id}`)}
              >
                <td>
                  <div style={{ fontWeight: 600 }}>{j.name || j.id}</div>
                  <div className="mono muted" style={{ fontSize: 12 }}>{j.id}</div>
                </td>
                <td className="mono">{j.schedule}</td>
                <td>
                  <div className="row">
                    <StatusDot status={j.last_run ? j.last_run.status : null} />
                    <span style={{ fontSize: 13 }}>{j.last_run && j.last_run.time ? relativeTime(j.last_run.time) : 'never'}</span>
                  </div>
                </td>
                <td><Badge lastRun={j.last_run} /></td>
                <td>
                  <button
                    type="button"
                    className={`toggle ${j.enabled ? 'on' : ''}`}
                    aria-label={j.enabled ? 'Disable' : 'Enable'}
                    onClick={(e) => { e.stopPropagation(); toggle(j) }}
                  />
                </td>
                <td>
                  <div className="actions" onClick={(e) => e.stopPropagation()}>
                    <Button size="sm" onClick={() => runNow(j)}>run</Button>
                    <Button size="sm" onClick={() => setEditing({ ...j })}>edit</Button>
                    <Button size="sm" variant="danger" onClick={() => remove(j)}>delete</Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {runPanel && (
        <RunPanel name={runPanel.name} steps={runPanel.steps} onClose={() => setRunPanel(null)} />
      )}
    </>
  )
}
```

Note: `toggle` and `save` send the whole job object (including `last_run`) to `updateJob`. The server decodes the payload into `Job` and ignores unknown fields, so `last_run` is harmlessly dropped — this matches the current v3 behavior.

- [ ] **Step 4: Write the `App.jsx` shell**

Replace `ui/src/App.jsx` entirely with the final version below (the sticky header with global theme toggle and sign-out matches the spec):


```jsx
import { useEffect, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import * as api from './api'
import Dashboard from './views/Dashboard'
import JobDetail from './views/JobDetail'

function Login({ onToken }) {
  const [input, setInput] = useState('')
  return (
    <div className="main">
      <form
        className="card"
        style={{ maxWidth: 360, margin: '48px auto' }}
        onSubmit={(e) => {
          e.preventDefault()
          const t = input.trim()
          api.setToken(t)
          onToken(t)
        }}
      >
        <h1 style={{ margin: '0 0 4px' }}>Cronix</h1>
        <p className="muted" style={{ margin: '0 0 16px' }}>Enter your API token to manage jobs.</p>
        <div className="field">
          <label htmlFor="login-token">API token</label>
          <input
            id="login-token"
            className="input"
            type="password"
            value={input}
            placeholder="••••••••"
            autoFocus
            onChange={(e) => setInput(e.target.value)}
          />
        </div>
        <button className="btn primary" type="submit" style={{ width: '100%' }}>Sign in</button>
      </form>
    </div>
  )
}

export default function App() {
  const [token, setToken] = useState(() => api.getToken())
  const [theme, setTheme] = useState(() => localStorage.getItem('cronix_theme') || 'light')

  const authed = Boolean(token)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    localStorage.setItem('cronix_theme', theme)
  }, [theme])

  const logout = () => {
    api.setToken('')
    setToken('')
  }

  if (!authed) {
    return <Login onToken={setToken} />
  }

  const toggleTheme = () => setTheme(theme === 'light' ? 'dark' : 'light')

  return (
    <div className="app">
      <BrowserRouter>
        <header className="header">
          <span className="brand">Cronix</span>
          <span className="spacer" />
          <button className="btn ghost sm" type="button" onClick={toggleTheme} aria-label="Toggle theme">
            {theme === 'light' ? (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8z" /></svg>
            ) : (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></svg>
            )}
          </button>
          <button className="btn ghost sm" type="button" onClick={logout}>Sign out</button>
        </header>
        <main className="main">
          <Routes>
            <Route path="/" element={<Dashboard theme={theme} onToggleTheme={toggleTheme} logout={logout} />} />
            <Route path="/jobs/:id" element={<JobDetail logout={logout} />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </BrowserRouter>
    </div>
  )
}
```

Create `ui/src/views/JobDetail.jsx` (stub):

```jsx
export default function JobDetail() {
  return <div className="loading">Job detail coming soon</div>
}
```

- [ ] **Step 5: Build + test gate**

Run: `npm --prefix ui run build && npm --prefix ui test && go test ./... -count=1`
Expected: build clean; vitest green; Go suite green (80+ tests, `TestSpaServesReactIndex` included).

- [ ] **Step 6: Manual smoke check (optional but recommended)**

Run the dev proxy build once if desired; otherwise rely on Task 7's E2E. If you do run it:

```bash
# from a separate shell: npm --prefix ui run dev
# open http://localhost:5173, enter token, confirm job list + stat cards render
```

If not run here, Task 7 covers it live against the container.

- [ ] **Step 7: Commit**

```bash
git add ui/ web/
git commit -m "feat: add dashboard with stat strip, job table, and router shell"
```

---

### Task 5: Job detail view

**Files:**
- Modify: `ui/src/views/JobDetail.jsx` (replace stub)

**Interfaces:**
- Consumes: `api` (`getJob`, `listRuns`, `runJob`, `updateJob`, `deleteJob`), `lib` (`relativeTime`), components (Button, Badge, StatusDot, JobEditor), `react-router-dom` (`Link`, `useParams`, `useNavigate`).
- Produces: the full `/jobs/:id` page — job card, meta, enabled switch, actions, command block with copy, History | Run output tabs, inline editor, loading/not-found/error states.

- [ ] **Step 1: Verify the baseline**

There are no component-level unit tests in this project (no jsdom/testing-library dependency); the detail view is verified by the build, the SPA test, and the live E2E (Task 7). Confirm the baseline is still green before adding the detail page:

Run: `npm --prefix ui test && npm --prefix ui run build && go test -run 'TestSpaServesReactIndex' ./...`
Expected: PASS — valid baseline before the JobDetail stub is replaced.

- [ ] **Step 2: Implement `ui/src/views/JobDetail.jsx`**

Replace the stub with:

```jsx
import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import * as api from '../api'
import { relativeTime } from '../lib'
import Button from '../components/Button'
import Badge from '../components/Badge'
import StatusDot from '../components/StatusDot'
import JobEditor from '../components/JobEditor'

function RunHistory({ runs }) {
  const [openId, setOpenId] = useState(null)
  if (!runs || runs.length === 0) {
    return <div className="empty">No runs yet.</div>
  }
  return (
    <div>
      {runs.map((r, i) => (
        <div
          key={i}
          className={`run-item ${openId === i ? 'open' : ''}`}
          onClick={() => setOpenId(openId === i ? null : i)}
        >
          <div className="run-meta">
            <StatusDot status={r.status} />
            <span>{r.trigger}</span>
            <span className="rel">{r.time ? relativeTime(r.time) : ''} <span className="mono muted" title={r.time}>· {r.time ? new Date(r.time).toLocaleString() : ''}</span></span>
            <span className="exit">exit {r.exit_code}</span>
          </div>
          {openId === i && (
            <div className="attempts">
              {(r.results || []).map((s, j) => (
                <div key={j} className="attempt">
                  <span>attempt {s.attempt}/{s.total} exit={s.exit}</span>
                  <div className="out">{s.output || '(no output)'}</div>
                </div>
              ))}
            </div>
          )}
        </div>
      ))}
    </div>
  )
}

function RunOutput({ steps }) {
  if (!steps || steps.length === 0) {
    return <div className="empty">Run a job to see output.</div>
  }
  return (
    <div>
      {steps.map((s, i) => (
        <div key={i} className="attempt" style={{ padding: '8px 0' }}>
          <span>attempt {s.attempt}/{s.total} exit={s.exit}</span>
          <div className="out" style={{ marginTop: 4 }}>{s.output || '(no output)'}</div>
        </div>
      ))}
    </div>
  )
}

export default function JobDetail({ logout }) {
  const { id } = useParams()
  const navigate = useNavigate()
  const [job, setJob] = useState(null)
  const [runs, setRuns] = useState(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [notFound, setNotFound] = useState(false)
  const [tab, setTab] = useState('history')
  const [editing, setEditing] = useState(false)
  const [copied, setCopied] = useState(false)
  const [lastRunOutput, setLastRunOutput] = useState(null)

  const handleError = useCallback(
    (e) => {
      if (e.message === 'unauthorized') {
        api.setToken('')
        logout()
        return
      }
      setError(e.message)
    },
    [logout],
  )

  const refresh = useCallback(() => {
    setLoading(true)
    setError('')
    api.getJob(id).then((j) => {
      if (!j || !j.id) {
        setNotFound(true)
        setLoading(false)
        return
      }
      setJob(j)
      setNotFound(false)
      return api.listRuns(id).then((r) => setRuns((r && r.runs) || []))
    }).catch(handleError).finally(() => setLoading(false))
  }, [id, handleError])

  useEffect(() => {
    refresh()
  }, [refresh])

  const toggle = () => {
    api.updateJob(job.id, { ...job, enabled: !job.enabled })
      .then((j) => { setJob(j); return api.listRuns(id).then((r) => setRuns((r && r.runs) || [])) })
      .catch(handleError)
  }

  const runNow = () => {
    api.runJob(id)
      .then((r) => {
        setLastRunOutput((r && r.steps) || [])
        setTab('output')
        refresh()
      })
      .catch(handleError)
  }

  return (
    <div>
      <Link className="back" to="/">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M19 12H5M12 19l-7-7 7-7" /></svg>
        Jobs
      </Link>

      {loading && <div className="loading">Loading…</div>}
      {!loading && notFound && (
        <div className="notfound">
          <h2>Job not found</h2>
          <p className="muted">This job may have been deleted.</p>
          <p><Link to="/">← Back to jobs</Link></p>
        </div>
      )}
      {!loading && error && (
        <div className="banner">
          <span>{error}</span>
          <Button variant="ghost" size="sm" className="retry" onClick={refresh}>Retry</Button>
        </div>
      )}

      {!loading && !notFound && !error && job && (
        <div className="grid-detail">
          <div>
            {!editing ? (
              <div className="card">
                <div className="card-head">
                  <div>
                    <h1>{job.name || job.id}</h1>
                    <div className="mono">{job.id}</div>
                  </div>
                  <div style={{ marginLeft: 'auto' }}>
                    <Badge lastRun={job.last_run} />
                  </div>
                </div>

                <div className="card-meta">
                  <div>
                    <div className="k">Schedule</div>
                    <div className="v">{job.schedule}</div>
                  </div>
                  <div>
                    <div className="k">Retries</div>
                    <div className="v">{job.retries}</div>
                  </div>
                  <div>
                    <div className="k">Retry delay</div>
                    <div className="v">{job.retry_delay}s</div>
                  </div>
                  <div>
                    <div className="k">Enabled</div>
                    <div className="v">
                      <button
                        type="button"
                        className={`toggle ${job.enabled ? 'on' : ''}`}
                        aria-label={job.enabled ? 'Disable' : 'Enable'}
                        onClick={toggle}
                      />
                    </div>
                  </div>
                </div>

                <div className="card-actions">
                  <Button variant="primary" onClick={runNow}>Run now</Button>
                  <Button onClick={() => setEditing(true)}>Edit</Button>
                  <Button variant="danger" onClick={() => {
                    if (!window.confirm(`Delete job ${job.name || job.id}?`)) return
                    api.deleteJob(id).then(() => navigate('/')).catch(handleError)
                  }}>Delete</Button>
                </div>

                <div className="cmd-block">
                  <pre>{job.curl || '(no command)'}</pre>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      navigator.clipboard?.writeText(job.curl || '').then(() => {
                        setCopied(true)
                        setTimeout(() => setCopied(false), 1500)
                      }).catch(() => {})
                    }}
                  >
                    {copied ? 'Copied' : 'Copy'}
                  </Button>
                </div>
              </div>
            ) : (
              <JobEditor
                key={job.id}
                job={job}
                error={error}
                submitLabel="Save"
                onSave={(form) => {
                  api.updateJob(job.id, { ...form, id: job.id })
                    .then(() => { setEditing(false); refresh() })
                    .catch(handleError)
                }}
                onCancel={() => setEditing(false)}
              />
            )}
          </div>

          <div>
            <div className="tabs">
              <button className={`tab ${tab === 'history' ? 'active' : ''}`} onClick={() => setTab('history')}>History</button>
              <button className={`tab ${tab === 'output' ? 'active' : ''}`} onClick={() => setTab('output')}>Run output</button>
            </div>
            {tab === 'history' ? (
              <RunHistory runs={runs} />
            ) : (
              <RunOutput steps={lastRunOutput} />
            )}
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 3: Build + test gate**

Run: `npm --prefix ui run build && npm --prefix ui test && go test ./... -count=1`
Expected: build clean; all green. (`TestSpaServesReactIndex` unaffected.)

- [ ] **Step 4: Manual smoke (recommended)**

```bash
# shell A: npm --prefix ui run dev   (Vite dev server on :5173, /api proxied to :8080)
# run the real server: make run  (Go server on :8080)
# open http://localhost:5173, sign in, click a job row → detail page, switch tabs, run a job, edit
```
If not run here, Task 7 covers it live against the container.

- [ ] **Step 5: Commit**

```bash
git add ui/ web/
git commit -m "feat: add job detail page with history and output tabs"
```

---

### Task 6: Shared error helper + final wiring

**Files:**
- Create: `ui/src/errors.js`, `ui/src/errors.test.js`
- Modify: `ui/src/views/Dashboard.jsx` (use shared helper)
- Modify: `ui/src/views/JobDetail.jsx` (use shared helper)

**Interfaces:**
- Consumes: the inline `handleError` duplicates currently defined in `Dashboard` (Task 4) and `JobDetail` (Task 5).
- Produces: `handleApiError(e, { logout, setError })` — pure helper in `ui/src/errors.js` that clears auth + token on a 401 (`e.message === 'unauthorized'`) and otherwise records the message. Both views import it (DRY, per the spec's "shared error helper used by both views").

- [ ] **Step 1: Write the failing test**

Create `ui/src/errors.test.js`:

```js
import { describe, expect, it, vi } from 'vitest'
import { handleApiError } from './errors'

describe('handleApiError', () => {
  it('clears auth on unauthorized', () => {
    const logout = vi.fn()
    const setError = vi.fn()
    handleApiError(new Error('unauthorized'), { logout, setError })
    expect(logout).toHaveBeenCalledTimes(1)
    expect(setError).not.toHaveBeenCalled()
  })

  it('records other errors without logging out', () => {
    const logout = vi.fn()
    const setError = vi.fn()
    handleApiError(new Error('boom'), { logout, setError })
    expect(logout).not.toHaveBeenCalled()
    expect(setError).toHaveBeenCalledWith('boom')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm --prefix ui test -- errors`
Expected: FAIL — `./errors` resolves to nothing (`handleApiError is not a function`).

- [ ] **Step 3: Implement the helper**

Create `ui/src/errors.js`:

```js
export function handleApiError(e, { logout, setError }) {
  if (e.message === 'unauthorized') {
    logout()
    return
  }
  setError(e.message)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm --prefix ui test`
Expected: PASS — the two new cases plus all existing tests.

- [ ] **Step 5: Refactor the two views to use it**

In `ui/src/views/Dashboard.jsx`:
- Add `import { handleApiError } from '../errors'`
- Remove the inline `handleError` definition (the block starting `const handleError = (e) => {`)
- Replace every `.catch(handleError)` with `.catch((e) => handleApiError(e, { logout, setError }))`

In `ui/src/views/JobDetail.jsx`:
- Add `import { handleApiError } from '../errors'`
- Remove the `handleError` `useCallback` (the block with `api.setToken(''); logout(); return`)
- Replace every `.catch(handleError)` with `.catch((e) => handleApiError(e, { logout, setError }))`
- The `refresh` `useCallback` currently has `[id, handleError]` in its dependency array — with `handleError` gone, change it to `[id]` and remove the `handleApiError`-dependent closure (the helper is module-level).

Note: `Dashboard` has all four catch sites (`refresh`, `save`, `toggle`, `remove`, `runNow` — five total); `JobDetail` has `refresh`, `toggle`, `runNow`, and the delete button's catch. Update every one.

- [ ] **Step 6: Build + test gate**

Run: `npm --prefix ui test && npm --prefix ui run build && go test ./... -count=1 && gofmt -l .`
Expected: all green; `gofmt -l .` empty.

- [ ] **Step 7: Commit**

```bash
git add ui/
git commit -m "refactor: extract shared API error handling into a helper"
```

---

### Task 7: Full verification + Docker live E2E

**Files:** none (verification only).

- [ ] **Step 1: Full gate**

Run:
```bash
go build ./... && go vet ./... && go test ./... -count=1
gofmt -l .
npm --prefix ui test
npm --prefix ui run build
```
Expected: all green.

- [ ] **Step 2: Live E2E in the container**

```bash
make docker-build
make docker-run
sleep 2
make cli-add            # creates "ping" job (schedule */5, curl example.com)
make cli-list
# open the UI and verify the ship-shape views (stat strip, table, detail, tabs, theme)
# via HTTP:
curl -s http://127.0.0.1:7002/ | grep -c 'root'        # React mount exists → expect 1
ID=$(docker exec cronix-dev /cronix cli list --token devtoken | awk 'NR==2{print $1}')
curl -s -H "Authorization: Bearer devtoken" "http://127.0.0.1:7002/api/v1/jobs/$ID/runs"   # history endpoint live
curl -s -H "Authorization: Bearer devtoken" http://127.0.0.1:7002/api/v1/jobs | head -c 600  # last_run present
# wait ~70s for a */5 scheduled fire, then confirm runs.json grew (volume is .data/)
python3 -c "import json; print(len(json.load(open('.data/runs.json'))))"   # expect >= 1 after fire
# restart persistence
make docker-stop && make docker-run
python3 -c "import json; runs=json.load(open('.data/runs.json')); print(runs[0]['status'], runs[0]['trigger'])"
make docker-stop
```

Note: `docker exec ... cat` fails inside the scratch image (no `cat`); read the mounted volume at `.data/` instead, as shown above.

- [ ] **Step 3: Visual check (browser)**

With the container running (`make docker-run`), open `http://127.0.0.1:7002/`, sign in with `devtoken`, and verify by sight:
- Stat strip shows Total/Enabled/Healthy/Failing
- Table renders with pills, dots, toggle switches
- Row click → detail page; tabs switch History/Run output
- Theme toggle flips light/dark in both views
- Run now populates output tab; scheduled fires appear in History
- Empty and error states render sanely

- [ ] **Step 4: Update the SDD progress ledger**

Append v3 entries to `.superpowers/sdd/progress.md` mirroring prior sections (tasks 1–7, commits, gate results, E2E pass).

- [ ] **Step 5: Commit any stragglers**

If anything changed, commit it. Otherwise done.

---

## Plan Self-Review (run after writing)

**Spec coverage checklist (against 2026-08-16-cronix-v3-sharp-ui-design.md):**
- Design tokens + both themes → Task 2 ✅ (token blocks, roles, mono, radius, shadow, ring)
- Header bar sticky + brand + theme + sign-out → Task 4 (App shell) ✅
- Stat strip (4 cards, empty → 0) → Task 4 via `summarizeJobs` (Task 1) ✅
- Toolbar filter + New job → Task 4 ✅
- Table (Job/Schedule/Last run/Status/Enabled/Actions; dot+pill; toggle switch; row hover; row-fail) → Task 4 ✅
- Row click → /jobs/:id → Task 4 (`useNavigate`) ✅
- Filter empty result → Task 4 ✅
- Detail page: back link, two-column grid, job card (meta, switch, actions, cmd-block copy) → Task 5 ✅
- Tabs History | Run output with expand-inline history rows → Task 5 ✅
- Inline editor, delete confirm + navigate back → Task 5 ✅
- Loading / not-found / inline error banner + Retry → Tasks 4-5 ✅
- 401 → clear token → login (shared helper) → Task 6 (`errors.js` `handleApiError`), used by both views via `logout` prop ✅
- No backend changes, no Tailwind/lib/icons, committed web/, Go suite green → Global Constraints ✅
- vitest `summarizeJobs` → Task 1 ✅; style token test → Task 2 ✅; `handleApiError` → Task 6 ✅

**Placeholder scan:** All code blocks are concrete. `JobDetail` stub in Task 4 is intentional (fills in Task 5). The stale `// runNow needs...` comment from an earlier draft has been removed inline (verified against current Task 5). ✅

**Type consistency:** `StatCard` prop `tone` values `ok|fail|muted` match `.stat-value.tone-*` CSS (Task 2). `Badge({lastRun})`/`StatusDot({status})` used identically in Tasks 4-5. `Dashboard` props `{theme, onToggleTheme, logout}` match `App`'s `<Dashboard theme=... onToggleTheme=... logout=...>` in Task 4. `JobDetail` prop `logout` matches App (Task 4 shell / Task 5 usage). `Button` forwards `type` and `href`. `summarizeJobs` returns `{total, enabled, healthy, failing}` used in Task 4 stats. `handleApiError(e, { logout, setError })` (Task 6) matches the catch sites refactored in Task 6. ✅ (Fixes already applied inline: App.jsx single clean block, JobDetail state/runNow wiring, Button `type`, Dashboard `save` dedupes `id`/`last_run`, Task 6 is the concrete shared-error-helper task.)