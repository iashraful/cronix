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
