export function handleApiError(e, { logout, setError }) {
  if (e.message === 'unauthorized') {
    logout()
    return
  }
  setError(e.message)
}
