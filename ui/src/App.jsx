import { useMemo, useEffect, useState } from 'react'
import * as api from './api'
import { filterJobs, relativeTime } from './lib'

const emptyJob = {
  name: '',
  schedule: '*/5 * * * *',
  curl: '',
  retries: 0,
  retry_delay: 5,
  enabled: true,
}

function Badge({ lastRun }) {
  if (!lastRun) return <span className="badge never">never</span>
  const label = lastRun.status === 'failed'
    ? `failed ${lastRun.exit_code}`
    : lastRun.status
  return (
    <span
      className={`badge ${lastRun.status}`}
      title={lastRun.time ? `last run ${relativeTime(lastRun.time)}` : ''}
    >
      {label}
    </span>
  )
}

function Editor({ job, onSave, onCancel, error }) {
  const [form, setForm] = useState(job)
  const set = (key) => (e) => setForm({ ...form, [key]: e.target.value })
  const setNum = (key) => (e) => setForm({ ...form, [key]: Number(e.target.value) })
  return (
    <form
      className="card"
      onSubmit={(e) => {
        e.preventDefault()
        onSave(form)
      }}
    >
      <h2>{job.id ? 'Edit job' : 'New job'}</h2>
      <label>
        Name
        <input value={form.name} placeholder="ping example" onChange={set('name')} />
      </label>
      <label>
        Schedule
        <input value={form.schedule} placeholder="*/5 * * * *" onChange={set('schedule')} />
      </label>
      <label>
        Curl command
        <input value={form.curl} placeholder="curl -s https://example.com" onChange={set('curl')} />
      </label>
      <label>
        Retries
        <input type="number" min="0" value={form.retries} onChange={setNum('retries')} />
      </label>
      <label>
        Retry delay (s)
        <input type="number" min="0" value={form.retry_delay} onChange={setNum('retry_delay')} />
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={form.enabled}
          onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
        />
        Enabled
      </label>
      {error && <p className="error">{error}</p>}
      <div className="row">
        <button type="submit">Save</button>
        <button type="button" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  )
}

function RunPanel({ name, steps, onClose }) {
  return (
    <div className="panel">
      <div className="row between">
        <h2>Run output — {name}</h2>
        <button onClick={onClose}>Close</button>
      </div>
      {steps.map((s, i) => (
        <pre key={i}>
attempt {s.attempt}/{s.total} exit={s.exit}
{s.output || '(no output)'}
        </pre>
      ))}
    </div>
  )
}

function HistoryPanel({ runs, onClose }) {
  return (
    <div className="panel">
      <div className="row between">
        <h2>Run history</h2>
        <button onClick={onClose}>Close</button>
      </div>
      {!runs || runs.length === 0 ? (
        <p className="muted">No runs yet.</p>
      ) : (
        runs.map((r, i) => (
          <pre key={i}>
{r.trigger} {r.time ? relativeTime(r.time) : ''} — {r.status} exit={r.exit_code}
{r.results.map((s) => `  attempt ${s.attempt}/${s.total} exit=${s.exit}${s.output ? `: ${s.output}` : ''}`).join('\n')}
          </pre>
        ))
      )}
    </div>
  )
}

export default function App() {
  const [token, setTokenState] = useState(api.getToken())
  const [tokenInput, setTokenInput] = useState('')
  const [jobs, setJobs] = useState([])
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [editing, setEditing] = useState(null) // null | {} new | job
  const [runPanel, setRunPanel] = useState(null) // {name, steps}
  const [history, setHistory] = useState(null) // {id, runs} or null
  const [theme, setTheme] = useState(() => localStorage.getItem('cronix_theme') || 'light')

  const authed = Boolean(token)

  const handleError = (e) => {
    if (e.message === 'unauthorized') {
      api.setToken('')
      setTokenState('')
    }
    setError(e.message)
  }

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    localStorage.setItem('cronix_theme', theme)
  }, [theme])

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
    if (authed) refresh()
  }, [authed, refresh])

  if (!authed) {
    return (
      <form
        className="card"
        onSubmit={(e) => {
          e.preventDefault()
          api.setToken(tokenInput.trim())
          setTokenState(tokenInput.trim())
        }}
      >
        <h1>Cronix</h1>
        <input
          type="password"
          value={tokenInput}
          placeholder="API token"
          onChange={(e) => setTokenInput(e.target.value)}
        />
        <button type="submit">Sign in</button>
      </form>
    )
  }

  const shown = filterJobs(jobs, query)

  const save = (form) => {
    const body = { ...form }
    delete body.id
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
      .then(() => {
        setHistory(null)
        refresh()
      })
      .catch(handleError)
  }

  const runNow = (j) =>
    api.runJob(j.id)
      .then((r) => {
        setRunPanel({ name: j.name || j.id, steps: (r && r.steps) || [] })
        refresh()
      })
      .catch(handleError)

  const openHistory = (j) =>
    api.listRuns(j.id)
      .then((r) => setHistory({ id: j.id, runs: (r && r.runs) || [] }))
      .catch(handleError)

  return (
    <div className="app">
      <header>
        <h1>Cronix</h1>
        <input
          className="search"
          value={query}
          placeholder="Filter jobs..."
          onChange={(e) => setQuery(e.target.value)}
        />
        <button onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')}>
          {theme === 'light' ? 'Dark' : 'Light'}
        </button>
        <button onClick={() => setEditing({ ...emptyJob })}>New job</button>
        <button
          onClick={() => {
            api.setToken('')
            setTokenState('')
          }}
        >
          Sign out
        </button>
      </header>

      {error && <p className="error">{error}</p>}

      {editing && (
        <Editor
          key={editing.id || 'new'}
          job={editing}
          error={error}
          onSave={save}
          onCancel={() => setEditing(null)}
        />
      )}

      {!editing && (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Schedule</th>
              <th>Last run</th>
              <th>Enabled</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((j) => (
              <tr key={j.id}>
                <td>
                  <strong>{j.name || j.id}</strong>
                  <div className="muted">{j.id}</div>
                </td>
                <td>{j.schedule}</td>
                <td><Badge lastRun={j.last_run} /></td>
                <td>
                  <button
                    className={j.enabled ? 'toggle on' : 'toggle'}
                    onClick={() => toggle(j)}
                  >
                    {j.enabled ? 'on' : 'off'}
                  </button>
                </td>
                <td>
                  <button onClick={() => openHistory(j)}>history</button>
                  <button onClick={() => runNow(j)}>run</button>
                  <button onClick={() => setEditing({ ...j })}>edit</button>
                  <button onClick={() => remove(j)}>delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {history && history.id && (
        <HistoryPanel runs={history.runs} onClose={() => setHistory(null)} />
      )}

      {runPanel && (
        <RunPanel name={runPanel.name} steps={runPanel.steps} onClose={() => setRunPanel(null)} />
      )}
    </div>
  )
}