import { describe, expect, it } from 'vitest'
import { filterJobs, relativeTime } from './lib'

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