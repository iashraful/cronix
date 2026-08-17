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
