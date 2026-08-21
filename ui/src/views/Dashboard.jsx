import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import * as api from '../api'
import { handleApiError } from '../errors'
import { filterJobs, relativeTime, relativeUntil, summarizeJobs } from '../lib'
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

  const refresh = useMemo(
    () => () =>
      api.listJobs()
        .then((j) => {
          setJobs(Array.isArray(j) ? j : [])
          setError('')
        })
        .catch((e) => handleApiError(e, { logout, setError })),
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
    }).catch((e) => handleApiError(e, { logout, setError }))
  }

  const toggle = (j) =>
    api.updateJob(j.id, { ...j, enabled: !j.enabled })
      .then(refresh)
      .catch((e) => handleApiError(e, { logout, setError }))

  const remove = (j) => {
    if (!window.confirm(`Delete job ${j.name || j.id}?`)) return
    api.deleteJob(j.id)
      .then(refresh)
      .catch((e) => handleApiError(e, { logout, setError }))
  }

  const runNow = (j) =>
    api.runJob(j.id)
      .then((r) => {
        setRunPanel({ name: j.name || j.id, steps: (r && r.steps) || [] })
        refresh()
      })
      .catch((e) => handleApiError(e, { logout, setError }))

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
              <th>Next run</th>
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
                <td
                  className="mono muted"
                  style={{ fontSize: 13 }}
                  title={j.next_run ? new Date(j.next_run).toLocaleString() : ''}
                >
                  {j.enabled && j.next_run ? relativeUntil(j.next_run) : '—'}
                </td>
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
