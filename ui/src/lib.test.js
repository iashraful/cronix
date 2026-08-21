import { describe, expect, it } from 'vitest'
import { filterJobs, relativeTime, relativeUntil, summarizeJobs } from './lib'

describe('relativeTime', () => {
  it('returns empty for missing input', () => {
    expect(relativeTime('')).toBe('')
    expect(relativeTime(null)).toBe('')
  })

  it('formats just now / minutes / hours / days', () => {
    const base = Date.now()
    expect(relativeTime(new Date(base - 5 * 1000).toISOString())).toBe('just now')
    expect(relativeTime(new Date(base - 90 * 1000).toISOString())).toBe('1m ago')
    expect(relativeTime(new Date(base - 2 * 3600 * 1000).toISOString())).toBe('2h ago')
    expect(relativeTime(new Date(base - 3 * 86400 * 1000).toISOString())).toBe('3d ago')
  })
})

describe('relativeUntil', () => {
  it('returns empty for missing input', () => {
    expect(relativeUntil('')).toBe('')
    expect(relativeUntil(null)).toBe('')
  })

  it('formats upcoming seconds / minutes / hours / days', () => {
    const base = Date.now()
    expect(relativeUntil(new Date(base + 30 * 1000).toISOString())).toBe('in 30s')
    expect(relativeUntil(new Date(base + 95 * 1000).toISOString())).toBe('in 1m')
    expect(relativeUntil(new Date(base + 2 * 3600 * 1000).toISOString())).toBe('in 2h')
    expect(relativeUntil(new Date(base + 72 * 3600 * 1000).toISOString())).toBe('in 3d')
  })

  it('reports due-now for past or current timestamps', () => {
    const base = Date.now()
    expect(relativeUntil(new Date(base - 60 * 1000).toISOString())).toBe('due now')
  })
})

describe('filterJobs', () => {
  const jobs = [
    { id: 'abc', name: 'ping', schedule: '*/5 * * * *' },
    { id: 'def', name: '', schedule: '0 9 * * 1-5' },
  ]

  it('returns everything when query is blank', () => {
    expect(filterJobs(jobs, '')).toEqual(jobs)
    expect(filterJobs(jobs, '   ')).toEqual(jobs)
  })

  it('matches by name, id, and schedule (case-insensitive)', () => {
    expect(filterJobs(jobs, 'ping')).toHaveLength(1)
    expect(filterJobs(jobs, 'ABC')).toHaveLength(1)
    expect(filterJobs(jobs, '9')).toHaveLength(1)
    expect(filterJobs(jobs, 'zzz')).toHaveLength(0)
  })
})

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