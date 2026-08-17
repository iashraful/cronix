import { useEffect, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import * as api from './api'
import Dashboard from './views/Dashboard'
import JobDetail from './views/JobDetail'

function Login({ onToken }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  return (
    <div className="main">
      <form
        className="card"
        style={{ maxWidth: 360, margin: '48px auto' }}
        onSubmit={async (e) => {
          e.preventDefault()
          setError('')
          try {
            const res = await api.login(username.trim(), password)
            api.setToken(res.token)
            onToken(res.token)
          } catch (err) {
            setError(err.message)
          }
        }}
      >
        <h1 style={{ margin: '0 0 4px' }}>Cronix</h1>
        <p className="muted" style={{ margin: '0 0 16px' }}>Sign in to manage your jobs.</p>
        <div className="field">
          <label htmlFor="login-username">Username</label>
          <input
            id="login-username"
            className="input"
            type="text"
            value={username}
            autoComplete="username"
            autoFocus
            onChange={(e) => setUsername(e.target.value)}
          />
        </div>
        <div className="field">
          <label htmlFor="login-password">Password</label>
          <input
            id="login-password"
            className="input"
            type="password"
            value={password}
            autoComplete="current-password"
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        {error && (
          <p className="muted" style={{ margin: '0 0 12px' }}>{error}</p>
        )}
        <button className="btn primary" type="submit" style={{ width: '100%' }}>Sign in</button>
      </form>
    </div>
  )
}

export default function App() {
  const [token, setToken] = useState(() => api.getToken())
  const [theme, setTheme] = useState(() => localStorage.getItem('cronix_theme') || 'light')

  const authed = Boolean(token)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    localStorage.setItem('cronix_theme', theme)
  }, [theme])

  const logout = () => {
    api.setToken('')
    setToken('')
  }

  if (!authed) {
    return <Login onToken={setToken} />
  }

  const toggleTheme = () => setTheme(theme === 'light' ? 'dark' : 'light')

  return (
    <div className="app">
      <BrowserRouter>
        <header className="header">
          <span className="brand">Cronix</span>
          <span className="spacer" />
          <button className="btn ghost sm" type="button" onClick={toggleTheme} aria-label="Toggle theme">
            {theme === 'light' ? (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8z" /></svg>
            ) : (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></svg>
            )}
          </button>
          <button className="btn ghost sm" type="button" onClick={logout}>Sign out</button>
        </header>
        <main className="main">
          <Routes>
            <Route path="/" element={<Dashboard theme={theme} onToggleTheme={toggleTheme} logout={logout} />} />
            <Route path="/jobs/:id" element={<JobDetail logout={logout} />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </BrowserRouter>
    </div>
  )
}
