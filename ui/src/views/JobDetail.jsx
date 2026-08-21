import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import * as api from '../api'
import { handleApiError } from '../errors'
import { relativeTime, relativeUntil } from '../lib'
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
    }).catch((e) => handleApiError(e, { logout, setError })).finally(() => setLoading(false))
  }, [id])

  useEffect(() => {
    refresh()
  }, [refresh])

  const toggle = () => {
    api.updateJob(job.id, { ...job, enabled: !job.enabled })
      .then((j) => { setJob({ ...j, last_run: job.last_run }); return api.listRuns(id).then((r) => setRuns((r && r.runs) || [])) })
      .catch((e) => handleApiError(e, { logout, setError }))
  }

  const runNow = () => {
    api.runJob(id)
      .then((r) => {
        setLastRunOutput((r && r.steps) || [])
        setTab('output')
        refresh()
      })
      .catch((e) => handleApiError(e, { logout, setError }))
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
      {!loading && error && !editing && (
        <div className="banner">
          <span>{error}</span>
          <Button variant="ghost" size="sm" className="retry" onClick={refresh}>Retry</Button>
        </div>
      )}

      {!loading && !notFound && job && (editing || !error) && (
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
                    <div className="k">Next run</div>
                    <div
                      className="v"
                      title={job.next_run ? new Date(job.next_run).toLocaleString() : ''}
                    >
                      {job.enabled && job.next_run ? relativeUntil(job.next_run) : '—'}
                    </div>
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
                    api.deleteJob(id).then(() => navigate('/')).catch((e) => handleApiError(e, { logout, setError }))
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
                    .catch((e) => handleApiError(e, { logout, setError }))
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
