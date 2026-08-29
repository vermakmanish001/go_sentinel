import { useCallback, useEffect, useRef, useState } from 'react'
import PlanForm from './components/PlanForm.jsx'
import MetricsPanel from './components/MetricsPanel.jsx'
import WorkersPanel from './components/WorkersPanel.jsx'
import HistoryPanel from './components/HistoryPanel.jsx'
import PlansPanel from './components/PlansPanel.jsx'
import ComparePanel from './components/ComparePanel.jsx'
import LoginScreen from './components/LoginScreen.jsx'
import UsersPanel from './components/UsersPanel.jsx'
import { MAX_COMPARE, SERIES } from './palette.js'
import {
  AuthError, deletePlan, deleteRun, getMe, getSeries, getWorkers, listPlans,
  listRuns, logout, savePlan, startRun, stopRun, streamRun,
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
  const [queuePos, setQueuePos] = useState(null)
  const [compare, setCompare] = useState([])   // [{run, samples}]
  const [session, setSession] = useState({ state: 'loading' })
  const closeStream = useRef(null)

  // Colour follows the run, not its position in the current selection, so
  // removing one run never repaints the others.
  const colorSlots = useRef(new Map())
  const colorFor = (id) => {
    if (!colorSlots.current.has(id)) {
      const taken = new Set(colorSlots.current.values())
      const free = SERIES.find((c) => !taken.has(c)) ?? SERIES[0]
      colorSlots.current.set(id, free)
    }
    return colorSlots.current.get(id)
  }

  // A 401 anywhere means the session lapsed; drop straight to the login screen
  // rather than showing a wall of failed panels.
  const guard = useCallback((e) => {
    if (e instanceof AuthError) { setSession({ state: 'out' }); return true }
    return false
  }, [])

  const refreshRuns = useCallback(async () => {
    try { setRuns((await listRuns()).runs || []) } catch (e) { guard(e) }
  }, [guard])
  const refreshPlans = useCallback(async () => {
    try { setPlans((await listPlans()).plans || []) } catch (e) { guard(e) }
  }, [guard])
  const refreshWorkers = useCallback(async () => {
    try { setWorkers(await getWorkers()) } catch (e) {
      if (!guard(e)) setWorkers({ workers: [], capacity: 0 })
    }
  }, [guard])

  useEffect(() => {
    getMe()
      .then((me) => setSession(
        !me.auth_required || me.username
          ? { state: 'in', username: me.username, authEnabled: !!me.auth_required }
          : { state: 'out' }))
      .catch(() => setSession({ state: 'out' }))
  }, [])

  useEffect(() => {
    if (session.state !== 'in') return
    refreshWorkers(); refreshRuns(); refreshPlans()
    const t = setInterval(refreshWorkers, 3000)
    return () => clearInterval(t)
  }, [session.state, refreshWorkers, refreshRuns, refreshPlans])

  useEffect(() => () => closeStream.current?.(), [])

  const running = run && !['COMPLETED', 'STOPPED', 'FAILED', 'CANCELLED'].includes(run.status)

  async function handleStart() {
    setError(null); setBusy(true)
    setMetrics(null); setHistory([]); setViewing(null)

    try {
      const { test_id, status, position } = await startRun(toApiPlan(plan))
      setRun({ id: test_id, status: status || 'QUEUED' })
      setQueuePos(position ?? 0)
      refreshRuns()

      closeStream.current = streamRun(test_id, {
        onMetrics: (m) => {
          setMetrics(m)
          setHistory((h) => [...h.slice(-299), m])
        },
        onStatus: (s) => {
          setRun((r) => (r ? { ...r, status: s.status } : r))
          setQueuePos(s.status === 'QUEUED' ? s.position ?? 0 : null)
        },
        onEnd: (e) => {
          setRun((r) => (r ? { ...r, status: e.status || 'COMPLETED' } : r))
          setQueuePos(null)
          refreshRuns()
        },
      })
    } catch (e) {
      if (!guard(e)) setError(e.message)
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
      setHistory((samples || []).map((s) => ({
        rps: { current: s.rps },
        latency: { p50_ms: s.p50_ms, p95_ms: s.p95_ms, p99_ms: s.p99_ms },
        errors: { percentage: s.err_pct, rate: s.err_rate },
      })))
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

  // Toggle a finished run into the comparison set, fetching its stored series.
  async function handleToggleCompare(r) {
    if (compare.some((c) => c.run.id === r.id)) {
      setCompare((c) => c.filter((x) => x.run.id !== r.id))
      return
    }
    if (compare.length >= MAX_COMPARE) {
      setError(`Comparison is limited to ${MAX_COMPARE} runs`)
      return
    }
    try {
      const { samples } = await getSeries(r.id)
      if (!samples?.length) { setError('That run has no recorded samples'); return }
      colorFor(r.id)
      setCompare((c) => [...c, { run: r, samples }])
    } catch (e) { setError(e.message) }
  }

  function clearCompare() {
    colorSlots.current.clear()
    setCompare([])
  }

  async function handleDeleteRun(r) {
    try {
      await deleteRun(r.id)
      if (viewing?.id === r.id) { setViewing(null); setMetrics(null); setHistory([]) }
      setCompare((c) => c.filter((x) => x.run.id !== r.id))
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

  if (session.state === 'loading') return <div className="login-wrap"><p className="hint">Loading…</p></div>
  if (session.state === 'out') {
    return <LoginScreen onSignedIn={(username) => setSession({ state: 'in', username, authEnabled: true })} />
  }

  return (
    <div className="app">
      <header>
        <h1>GoSentinel</h1>
        <div className="fleet">
          {workers.workers.length} worker{workers.workers.length === 1 ? '' : 's'}
          {' · '}{workers.capacity.toLocaleString()} VU capacity
        </div>
        {session.username && (
          <div className="account">
            {session.username}
            <button type="button" className="link"
              onClick={() => logout().finally(() => setSession({ state: 'out' }))}>
              Sign out
            </button>
          </div>
        )}
      </header>

      {error && <div className="banner error" onClick={() => setError(null)}>{error}</div>}

      <div className="columns">
        <section className="col">
          <PlanForm plan={plan} onChange={setPlan} disabled={false} />
          <div className="actions">
            <button className="primary" onClick={handleStart} disabled={busy}>
              {run?.status === 'QUEUED' ? 'Queued…' : running ? 'Running…' : 'Run test'}
            </button>
            <button onClick={handleStop} disabled={!running || busy}>
              {run?.status === 'QUEUED' ? 'Cancel' : 'Stop'}
            </button>
            {run && (
              <span className={`status ${run.status.toLowerCase()}`}>
                {run.id} · {run.status}
                {run.status === 'QUEUED' && queuePos != null &&
                  (queuePos === 0 ? ' · next up' : ` · ${queuePos} ahead`)}
              </span>
            )}
            {viewing && <span className="status">viewing {viewing.name || viewing.id}</span>}
          </div>
          <PlansPanel plans={plans} disabled={!!running}
            onSave={handleSavePlan}
            onLoad={(p) => loadSpec(p.spec, p.name)}
            onDelete={handleDeletePlan} />
          {session.authEnabled && (
            <UsersPanel currentUser={session.username} onError={setError} />
          )}
        </section>

        <section className="col">
          <MetricsPanel metrics={metrics} history={history}
            title={viewing ? `Run · ${viewing.name || viewing.id}` : 'Live metrics'} />
          <ComparePanel runs={compare} colorFor={colorFor} onClear={clearCompare} />
          <HistoryPanel runs={runs} activeId={viewing?.id}
            selected={compare.map((c) => c.run.id)} colorFor={colorFor}
            onSelect={handleSelectRun} onToggleCompare={handleToggleCompare}
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
