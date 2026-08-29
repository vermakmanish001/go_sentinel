import HeaderRows from './HeaderRows.jsx'

const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']
const ASSERTION_TYPES = [
  ['status_code', 'Status code equals'],
  ['response_time_p95_ms', 'Response time under (ms)'],
  ['response_time_p99_ms', 'Response time under (ms, p99)'],
  ['body_contains', 'Body contains'],
]

export default function PlanForm({ plan, onChange, disabled }) {
  const set = (patch) => onChange({ ...plan, ...patch })
  const setHttp = (patch) => set({ http: { ...plan.http, ...patch } })

  const setStage = (i, patch) =>
    set({ stages: plan.stages.map((s, j) => (j === i ? { ...s, ...patch } : s)) })

  const setRequest = (i, patch) =>
    setHttp({ requests: plan.http.requests.map((r, j) => (j === i ? { ...r, ...patch } : r)) })

  const peakVUs = plan.stages.reduce((m, s) => Math.max(m, Number(s.target_vus) || 0), 0)

  return (
    <fieldset className="card" disabled={disabled}>
      <h2>Test plan</h2>

      <label className="field">
        <span>Name</span>
        <input value={plan.name} onChange={(e) => set({ name: e.target.value })} />
      </label>

      <div className="row">
        <label className="field grow">
          <span>Base URL</span>
          <input
            placeholder="https://api.example.com"
            value={plan.http.base_url}
            onChange={(e) => setHttp({ base_url: e.target.value })}
          />
        </label>
        <label className="field narrow">
          <span>Timeout</span>
          <input value={plan.http.timeout} onChange={(e) => setHttp({ timeout: e.target.value })} />
        </label>
      </div>

      <HeaderRows
        label="Headers sent with every request"
        rows={plan.http.headerRows}
        onChange={(headerRows) => setHttp({ headerRows })}
      />

      <h3>
        Stages <span className="hint">peak {peakVUs} VUs</span>
      </h3>
      <table className="grid">
        <thead>
          <tr><th>Duration</th><th>Target VUs</th><th>Ramp-up</th><th /></tr>
        </thead>
        <tbody>
          {plan.stages.map((s, i) => (
            <tr key={i}>
              <td><input value={s.duration} onChange={(e) => setStage(i, { duration: e.target.value })} placeholder="30s" /></td>
              <td><input type="number" min="1" value={s.target_vus} onChange={(e) => setStage(i, { target_vus: e.target.value })} /></td>
              <td><input value={s.ramp_up} onChange={(e) => setStage(i, { ramp_up: e.target.value })} placeholder="optional" /></td>
              <td>
                <button type="button" className="icon" disabled={plan.stages.length === 1}
                  onClick={() => set({ stages: plan.stages.filter((_, j) => j !== i) })}>✕</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <button type="button" className="link"
        onClick={() => set({ stages: [...plan.stages, { duration: '30s', target_vus: 10, ramp_up: '' }] })}>
        + Add stage
      </button>

      <h3>Requests</h3>
      {plan.http.requests.map((r, i) => (
        <div className="subcard" key={i}>
          <div className="row">
            <label className="field narrow">
              <span>Method</span>
              <select value={r.method} onChange={(e) => setRequest(i, { method: e.target.value })}>
                {METHODS.map((m) => <option key={m}>{m}</option>)}
              </select>
            </label>
            <label className="field grow">
              <span>Path</span>
              <input value={r.path} onChange={(e) => setRequest(i, { path: e.target.value })} placeholder="/v1/users" />
            </label>
            <label className="field narrow">
              <span>Think time</span>
              <input value={r.think_time} onChange={(e) => setRequest(i, { think_time: e.target.value })} placeholder="0s" />
            </label>
            {plan.http.requests.length > 1 && (
              <button type="button" className="icon"
                onClick={() => setHttp({ requests: plan.http.requests.filter((_, j) => j !== i) })}>✕</button>
            )}
          </div>

          <HeaderRows label="Request headers" rows={r.headerRows}
            onChange={(headerRows) => setRequest(i, { headerRows })} />

          {r.method !== 'GET' && r.method !== 'HEAD' && (
            <label className="field">
              <span>Body</span>
              <textarea rows="3" value={r.body} onChange={(e) => setRequest(i, { body: e.target.value })} />
            </label>
          )}

          <div className="assertions">
            <span className="sublabel">Assertions</span>
            {r.assertions.map((a, ai) => (
              <div className="row tight" key={ai}>
                <select value={a.type}
                  onChange={(e) => setRequest(i, {
                    assertions: r.assertions.map((x, j) => (j === ai ? { ...x, type: e.target.value } : x)),
                  })}>
                  {ASSERTION_TYPES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
                </select>
                <input value={a.value}
                  onChange={(e) => setRequest(i, {
                    assertions: r.assertions.map((x, j) => (j === ai ? { ...x, value: e.target.value } : x)),
                  })} />
                <button type="button" className="icon"
                  onClick={() => setRequest(i, { assertions: r.assertions.filter((_, j) => j !== ai) })}>✕</button>
              </div>
            ))}
            <button type="button" className="link"
              onClick={() => setRequest(i, { assertions: [...r.assertions, { type: 'status_code', value: '200' }] })}>
              + Add assertion
            </button>
          </div>
        </div>
      ))}
      <button type="button" className="link"
        onClick={() => setHttp({
          requests: [...plan.http.requests,
            { method: 'GET', path: '/', body: '', think_time: '', headerRows: [], assertions: [] }],
        })}>
        + Add request
      </button>
    </fieldset>
  )
}
