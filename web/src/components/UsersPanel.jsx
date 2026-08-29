import { useEffect, useState } from 'react'
import { createUser, deleteUser, listUsers } from '../api.js'

/**
 * Account management.
 *
 * There is no open signup: an account here can point the whole fleet at any
 * allowed target, so access is granted by someone who already has it.
 */
export default function UsersPanel({ currentUser, onError }) {
  const [users, setUsers] = useState([])
  const [open, setOpen] = useState(false)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)

  const refresh = () => listUsers().then((d) => setUsers(d.users || [])).catch(() => {})
  useEffect(() => { refresh() }, [])

  async function add(e) {
    e.preventDefault()
    setBusy(true)
    try {
      await createUser(username.trim(), password)
      setUsername(''); setPassword(''); setOpen(false)
      refresh()
    } catch (err) {
      onError(err.message)
    } finally {
      setBusy(false)
    }
  }

  async function remove(u) {
    if (!window.confirm(`Delete the account "${u.username}"? They will be signed out immediately.`)) return
    try { await deleteUser(u.id); refresh() } catch (err) { onError(err.message) }
  }

  return (
    <div className="card">
      <div className="chart-head">
        <h2>Accounts</h2>
        <button type="button" className="link" onClick={() => setOpen((v) => !v)}>
          {open ? 'Cancel' : '+ Add account'}
        </button>
      </div>

      {open && (
        <form className="subcard" onSubmit={add}>
          <div className="row">
            <label className="field grow">
              <span>Username</span>
              <input value={username} autoComplete="off"
                onChange={(e) => setUsername(e.target.value)} />
            </label>
            <label className="field grow">
              <span>Password</span>
              <input type="password" value={password} autoComplete="new-password"
                onChange={(e) => setPassword(e.target.value)} />
            </label>
          </div>
          <p className="hint">At least 8 characters. There is no self-signup — anyone you add can drive the fleet.</p>
          <button className="primary" type="submit"
            disabled={busy || !username.trim() || password.length < 8}>
            {busy ? 'Creating…' : 'Create account'}
          </button>
        </form>
      )}

      <table className="grid">
        <tbody>
          {users.map((u) => (
            <tr key={u.id}>
              <td>
                {u.username}
                {u.username === currentUser && <span className="hint"> · you</span>}
              </td>
              <td className="rowactions">
                <button type="button" className="icon" title="Delete account"
                  disabled={u.username === currentUser || users.length <= 1}
                  onClick={() => remove(u)}>✕</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
