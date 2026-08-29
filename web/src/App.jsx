import { useCallback, useEffect, useRef, useState } from 'react'
import PlanForm from './components/PlanForm.jsx'
import MetricsPanel from './components/MetricsPanel.jsx'
import WorkersPanel from './components/WorkersPanel.jsx'
import { getWorkers, startRun, stopRun, streamRun } from './api.js'

const emptyPlan = {
  name: 'My API test',
  stages: [{ duration: '30s', target_vus: 5, ramp_up: '' }],
  http: {
    base_url: '',
    timeout: '10s',
    headerRows: [],
    requests: [
      { method: 'GET', path: '/', body: '', think_time: '', headerRows: [], assertions: [] },
    ],
  },
}

export default function App() {
  const [plan, setPlan] = useState(emptyPlan)
  const [run, setRun] = useState(null) // {id, status}
  const [metrics, setMetrics] = useState(null)
  const [history, setHistory] = useState([])
  const [workers, setWorkers] = useState({ workers: [], capacity: 0 })
  const [error, setError] = useState(null)
  const [busy, setBusy] = useState(false)
  const closeStream = useRef(null)

  const refreshWorkers = useCallback(async () => {
    try {
      setWorkers(await getWorkers())
    } catch {
      setWorkers({ workers: [], capacity: 0 })
    }
  }, [])

  useEffect(() => {
    refreshWorkers()
    const t = setInterval(refreshWorkers, 3000)
    return () => clearInterval(t)
  }, [refreshWorkers])

  useEffect(() => () => closeStream.current?.(), [])

  async function handleStart() {
    setError(null)
    setBusy(true)
    setMetrics(null)
    setHistory([])

    try {
      const { test_id } = await startRun(toApiPlan(plan))
      setRun({ id: test_id, status: 'RUNNING' })

      closeStream.current = streamRun(test_id, {
        onMetrics: (m) => {
          setMetrics(m)
          setHistory((h) => [...h.slice(-179), m])
        },
        onStatus: (s) => setRun((r) => (r ? { ...r, status: s.status } : r)),
        onEnd: (e) => setRun((r) => (r ? { ...r, status: e.status || 'COMPLETED' } : r)),
      })
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  async function handleStop() {
    if (!run) return
    setBusy(true)
    try {
      await stopRun(run.id)
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const running = run && !['COMPLETED', 'STOPPED', 'FAILED'].includes(run.status)

  return (
    <div className="app">
      <header>
        <h1>GoSentinel</h1>
        <div className="fleet">
          {workers.workers.length} worker{workers.workers.length === 1 ? '' : 's'}
          {' · '}
          {workers.capacity.toLocaleString()} VU capacity
        </div>
      </header>

      {error && (
        <div className="banner error" onClick={() => setError(null)}>
          {error}
        </div>
      )}

      <div className="columns">
        <section className="col">
          <PlanForm plan={plan} onChange={setPlan} disabled={!!running} />
          <div className="actions">
            <button className="primary" onClick={handleStart} disabled={busy || running}>
              {running ? 'Running…' : 'Run test'}
            </button>
            <button onClick={handleStop} disabled={!running || busy}>
              Stop
            </button>
            {run && (
              <span className={`status ${run.status.toLowerCase()}`}>
                {run.id} · {run.status}
              </span>
            )}
          </div>
        </section>

        <section className="col">
          <MetricsPanel metrics={metrics} history={history} />
          <WorkersPanel workers={workers.workers} />
        </section>
      </div>
    </div>
  )
}

// toApiPlan converts the form's header rows into the objects the API expects.
function toApiPlan(plan) {
  const rowsToObject = (rows) =>
    rows.reduce((acc, { key, value }) => {
      if (key.trim()) acc[key.trim()] = value
      return acc
    }, {})

  return {
    name: plan.name,
    stages: plan.stages.map((s) => ({
      duration: s.duration,
      target_vus: Number(s.target_vus) || 0,
      ...(s.ramp_up ? { ramp_up: s.ramp_up } : {}),
    })),
    http: {
      base_url: plan.http.base_url.trim(),
      timeout: plan.http.timeout,
      headers: rowsToObject(plan.http.headerRows),
      requests: plan.http.requests.map((r) => ({
        method: r.method,
        path: r.path,
        headers: rowsToObject(r.headerRows),
        ...(r.body ? { body: r.body } : {}),
        ...(r.think_time ? { think_time: r.think_time } : {}),
        assertions: r.assertions.map(toAssertion).filter(Boolean),
      })),
    },
  }
}

function toAssertion(a) {
  const n = Number(a.value)
  switch (a.type) {
    case 'status_code':
      return Number.isFinite(n) ? { status_code: n } : null
    case 'response_time_p99_ms':
      return Number.isFinite(n) ? { response_time_p99_ms: n } : null
    case 'response_time_p95_ms':
      return Number.isFinite(n) ? { response_time_p95_ms: n } : null
    case 'body_contains':
      return a.value ? { body_contains: a.value } : null
    default:
      return null
  }
}
