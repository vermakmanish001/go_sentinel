import { useCallback, useEffect, useRef, useState } from 'react'
import PlanForm from './components/PlanForm.jsx'
import MetricsPanel from './components/MetricsPanel.jsx'
import WorkersPanel from './components/WorkersPanel.jsx'
import HistoryPanel from './components/HistoryPanel.jsx'
import PlansPanel from './components/PlansPanel.jsx'
import {
  deletePlan, deleteRun, getSeries, getWorkers, listPlans, listRuns,
  savePlan, startRun, stopRun, streamRun,
} from './api.js'

const emptyPlan = () => ({
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
})

export default function App() {
  const [plan, setPlan] = useState(emptyPlan)
  const [run, setRun] = useState(null)
  const [metrics, setMetrics] = useState(null)
  const [history, setHistory] = useState([])
  const [viewing, setViewing] = useState(null) // a past run being inspected
  const [runs, setRuns] = useState([])
  const [plans, setPlans] = useState([])
  const [workers, setWorkers] = useState({ workers: [], capacity: 0 })
  const [error, setError] = useState(null)
  const [busy, setBusy] = useState(false)
  const closeStream = useRef(null)

  const refreshRuns = useCallback(async () => {
    try { setRuns((await listRuns()).runs || []) } catch { /* history is best-effort */ }
  }, [])
  const refreshPlans = useCallback(async () => {
    try { setPlans((await listPlans()).plans || []) } catch { /* ignore */ }
  }, [])
  const refreshWorkers = useCallback(async () => {
    try { setWorkers(await getWorkers()) } catch { setWorkers({ workers: [], capacity: 0 }) }
  }, [])

  useEffect(() => {
    refreshWorkers(); refreshRuns(); refreshPlans()
    const t = setInterval(refreshWorkers, 3000)
    return () => clearInterval(t)
  }, [refreshWorkers, refreshRuns, refreshPlans])

  useEffect(() => () => closeStream.current?.(), [])

  const running = run && !['COMPLETED', 'STOPPED', 'FAILED'].includes(run.status)

  async function handleStart() {
    setError(null); setBusy(true)
    setMetrics(null); setHistory([]); setViewing(null)

    try {
      const { test_id } = await startRun(toApiPlan(plan))
      setRun({ id: test_id, status: 'RUNNING' })

      closeStream.current = streamRun(test_id, {
        onMetrics: (m) => {
          setMetrics(m)
          setHistory((h) => [...h.slice(-299), m])
        },
        onStatus: (s) => setRun((r) => (r ? { ...r, status: s.status } : r)),
        onEnd: (e) => {
          setRun((r) => (r ? { ...r, status: e.status || 'COMPLETED' } : r))
          refreshRuns()
        },
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
    try { await stopRun(run.id) } catch (e) { setError(e.message) } finally { setBusy(false) }
  }

  // Inspect a finished run: rebuild its throughput trend from stored samples.
  async function handleSelectRun(r) {
    if (running) return
    setError(null)
    try {
      const { samples } = await getSeries(r.id)
      setViewing(r)
      setRun(null)
      setHistory((samples || []).map((s) => ({ rps: { current: s.rps } })))
      const last = samples?.[samples.length - 1]
      setMetrics({
        timestamp_ms: last?.ts_ms ?? r.started_at,
        rps: { current: last?.rps ?? 0, average: r.avg_rps, peak: r.peak_rps },
        latency: {
          min_ms: 0, max_ms: 0, mean_ms: 0,
          p50_ms: last?.p50_ms ?? 0, p95_ms: r.p95_ms, p99_ms: r.p99_ms, count: 0,
        },
        errors: { rate: last?.err_rate ?? 0, percentage: r.error_pct },
        total_requests: r.total_requests,
        total_errors: r.total_errors,
      })
    } catch (e) {
      setError(e.message)
    }
  }

  async function handleDeleteRun(r) {
    try {
      await deleteRun(r.id)
      if (viewing?.id === r.id) { setViewing(null); setMetrics(null); setHistory([]) }
      refreshRuns()
    } catch (e) { setError(e.message) }
  }

  async function handleSavePlan() {
    const name = window.prompt('Save plan as:', plan.name)
    if (!name) return
    try {
      await savePlan(name, toApiPlan({ ...plan, name }))
      refreshPlans()
    } catch (e) { setError(e.message) }
  }

  async function handleDeletePlan(p) {
    try { await deletePlan(p.id); refreshPlans() } catch (e) { setError(e.message) }
  }

  const loadSpec = (spec, fallbackName) => {
    try {
      setPlan(fromApiPlan(typeof spec === 'string' ? JSON.parse(spec) : spec, fallbackName))
      setError(null)
    } catch {
      setError('Could not load that plan')
    }
  }

  return (
    <div className="app">
      <header>
        <h1>GoSentinel</h1>
        <div className="fleet">
          {workers.workers.length} worker{workers.workers.length === 1 ? '' : 's'}
          {' · '}{workers.capacity.toLocaleString()} VU capacity
        </div>
      </header>

      {error && <div className="banner error" onClick={() => setError(null)}>{error}</div>}

      <div className="columns">
        <section className="col">
          <PlanForm plan={plan} onChange={setPlan} disabled={!!running} />
          <div className="actions">
            <button className="primary" onClick={handleStart} disabled={busy || running}>
              {running ? 'Running…' : 'Run test'}
            </button>
            <button onClick={handleStop} disabled={!running || busy}>Stop</button>
            {run && <span className={`status ${run.status.toLowerCase()}`}>{run.id} · {run.status}</span>}
            {viewing && <span className="status">viewing {viewing.name || viewing.id}</span>}
          </div>
          <PlansPanel plans={plans} disabled={!!running}
            onSave={handleSavePlan}
            onLoad={(p) => loadSpec(p.spec, p.name)}
            onDelete={handleDeletePlan} />
        </section>

        <section className="col">
          <MetricsPanel metrics={metrics} history={history} />
          <HistoryPanel runs={runs} activeId={viewing?.id} onSelect={handleSelectRun}
            onDelete={handleDeleteRun}
            onReplay={(r) => r.plan_spec ? loadSpec(r.plan_spec, r.name)
                                         : setError('This run has no stored plan')} />
          <WorkersPanel workers={workers.workers} />
        </section>
      </div>
    </div>
  )
}

const rowsToObject = (rows) =>
  rows.reduce((acc, { key, value }) => {
    if (key.trim()) acc[key.trim()] = value
    return acc
  }, {})

const objectToRows = (obj) =>
  Object.entries(obj || {}).map(([key, value]) => ({ key, value }))

// toApiPlan converts form state into the JSON the API accepts.
function toApiPlan(plan) {
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

// fromApiPlan is the inverse, so a saved plan or a past run loads back into the
// form exactly as it was submitted.
function fromApiPlan(spec, fallbackName) {
  return {
    name: spec.name || fallbackName || 'Loaded plan',
    stages: (spec.stages || []).map((s) => ({
      duration: s.duration || '30s',
      target_vus: s.target_vus ?? 1,
      ramp_up: s.ramp_up || '',
    })),
    http: {
      base_url: spec.http?.base_url || '',
      timeout: spec.http?.timeout || '10s',
      headerRows: objectToRows(spec.http?.headers),
      requests: (spec.http?.requests || []).map((r) => ({
        method: r.method || 'GET',
        path: r.path || '/',
        body: r.body || '',
        think_time: r.think_time || '',
        headerRows: objectToRows(r.headers),
        assertions: (r.assertions || []).map(fromAssertion).filter(Boolean),
      })),
    },
  }
}

const ASSERTION_KEYS = ['status_code', 'response_time_p95_ms', 'response_time_p99_ms', 'body_contains']

function toAssertion(a) {
  const n = Number(a.value)
  if (a.type === 'body_contains') return a.value ? { body_contains: a.value } : null
  return Number.isFinite(n) && a.value !== '' ? { [a.type]: n } : null
}

function fromAssertion(a) {
  for (const key of ASSERTION_KEYS) {
    if (a[key] !== undefined) return { type: key, value: String(a[key]) }
  }
  return null
}
