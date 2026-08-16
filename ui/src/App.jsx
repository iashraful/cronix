import { useEffect, useState } from 'react'
import { getToken, setToken, listJobs } from './api'

export default function App() {
  const [token, setTokenState] = useState(getToken())
  const [tokenInput, setTokenInput] = useState('')
  const [jobs, setJobs] = useState([])
  const [error, setError] = useState('')
  const [theme, setTheme] = useState(() => localStorage.getItem('cronix_theme') || 'light')

  const authed = Boolean(token)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    localStorage.setItem('cronix_theme', theme)
  }, [theme])

  useEffect(() => {
    if (!authed) return
    listJobs()
      .then((j) => {
        setJobs(Array.isArray(j) ? j : [])
        setError('')
      })
      .catch((e) => setError(e.message))
  }, [authed])

  if (!authed) {
    return (
      <form
        className="card"
        onSubmit={(e) => {
          e.preventDefault()
          setToken(tokenInput.trim())
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

  return (
    <div className="app">
      <header>
        <h1>Cronix</h1>
        <button onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')}>
          {theme === 'light' ? 'Dark' : 'Light'}
        </button>
        <button
          onClick={() => {
            setToken('')
            setTokenState('')
          }}
        >
          Sign out
        </button>
      </header>
      {error && <p className="error">{error}</p>}
      <p>Jobs: {jobs.length}</p>
    </div>
  )
}