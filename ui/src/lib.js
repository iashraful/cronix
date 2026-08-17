export function relativeTime(iso) {
  if (!iso) return ''
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ''
  const secs = Math.max(0, Math.floor((Date.now() - then) / 1000))
  if (secs < 10) return 'just now'
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  const days = Math.floor(hrs / 24)
  return days === 1 ? 'yesterday' : `${days}d ago`
}

export function filterJobs(jobs, query) {
  const needle = (query || '').trim().toLowerCase()
  if (!needle) return jobs
  return jobs.filter((j) =>
    [j.id, j.name, j.schedule].some((v) => (v || '').toLowerCase().includes(needle)),
  )
}

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