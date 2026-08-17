const TOKEN_KEY = 'cronix_token'

export function getToken() {
  return sessionStorage.getItem(TOKEN_KEY)
}

export function setToken(token) {
  if (token) {
    sessionStorage.setItem(TOKEN_KEY, token)
  } else {
    sessionStorage.removeItem(TOKEN_KEY)
  }
}

async function request(method, path, body) {
  const headers = { Authorization: `Bearer ${getToken() ?? ''}` }
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }
  const resp = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (resp.status === 401) {
    throw new Error('unauthorized')
  }
  if (resp.status === 204) {
    return null
  }
  const data = await resp.json().catch(() => null)
  if (!resp.ok) {
    throw new Error((data && data.error) || `request failed (${resp.status})`)
  }
  return data
}

export async function login(username, password) {
  const resp = await fetch('/api/v1/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  const data = await resp.json().catch(() => null)
  if (!resp.ok) {
    throw new Error((data && data.error) || `request failed (${resp.status})`)
  }
  return data
}

export const listJobs = () => request('GET', '/api/v1/jobs')
export const getJob = (id) => request('GET', `/api/v1/jobs/${id}`)
export const createJob = (job) => request('POST', '/api/v1/jobs', job)
export const updateJob = (id, job) => request('PUT', `/api/v1/jobs/${id}`, job)
export const deleteJob = (id) => request('DELETE', `/api/v1/jobs/${id}`)
export const runJob = (id) => request('POST', `/api/v1/jobs/${id}/run`)
export const listRuns = (id) => request('GET', `/api/v1/jobs/${id}/runs`)