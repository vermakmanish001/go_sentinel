import { useState } from 'react'
import { login } from '../api.js'

export default function LoginScreen({ onSignedIn }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState(null)
  const [busy, setBusy] = useState(false)

  async function submit(e) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const { username: name } = await login(username, password)
      onSignedIn(name)
    } catch (err) {
      setError(err.message || 'Sign in failed')
      setPassword('')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login-wrap">
      <form className="card login" onSubmit={submit}>
        <h1>GoSentinel</h1>
        <p className="hint">Sign in to run load tests.</p>

        {error && <div className="banner error">{error}</div>}

        <label className="field">
          <span>Username</span>
          <input value={username} autoFocus autoComplete="username"
            onChange={(e) => setUsername(e.target.value)} />
        </label>
        <label className="field">
          <span>Password</span>
          <input type="password" value={password} autoComplete="current-password"
            onChange={(e) => setPassword(e.target.value)} />
        </label>

        <button className="primary" type="submit" disabled={busy || !username || !password}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}
