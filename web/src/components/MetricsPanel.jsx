import ChartCard from './ChartCard.jsx'
import { LATENCY, SERIES, STATUS_CRITICAL } from '../palette.js'

export default function MetricsPanel({ metrics, history, title = 'Live metrics' }) {
  if (!metrics) {
    return (
      <div className="card">
        <h2>{title}</h2>
        <p className="hint">Run a test to see throughput, latency and errors here.</p>
      </div>
    )
  }

  const { rps, latency, errors, total_requests, total_errors } = metrics
  const errClass = errors.percentage > 5 ? 'bad' : errors.percentage > 1 ? 'warn' : 'good'

  // Each point's x is its position in the series: samples arrive once a second,
  // so the index is elapsed seconds.
  const at = (fn) => history.map((h, i) => ({ x: i, y: Math.round(fn(h) * 10) / 10 }))

  return (
    <div className="card">
      <h2>{title}</h2>

      <div className="stats">
        <Stat label="Requests/sec" value={rps.current.toFixed(1)} />
        <Stat label="Average" value={rps.average.toFixed(1)} />
        <Stat label="Peak" value={rps.peak.toFixed(1)} />
        <Stat label="Errors" value={`${errors.percentage.toFixed(2)}%`} className={errClass} />
      </div>

      {history.length > 1 && (
        <>
          <ChartCard title="Requests/sec"
            series={[{ id: 'rps', label: 'Requests/sec', color: SERIES[0], points: at((h) => h.rps.current) }]} />

          {history.some((h) => h.latency) && (
            <ChartCard title="Latency percentiles" unit="ms" series={[
              { id: 'p50', label: 'p50', color: LATENCY.p50, points: at((h) => h.latency?.p50_ms ?? 0) },
              { id: 'p95', label: 'p95', color: LATENCY.p95, points: at((h) => h.latency?.p95_ms ?? 0) },
              { id: 'p99', label: 'p99', color: LATENCY.p99, points: at((h) => h.latency?.p99_ms ?? 0) },
            ]} />
          )}

          {history.some((h) => h.errors?.percentage > 0) && (
            <ChartCard title="Error rate" unit="%" height={140}
              series={[{ id: 'err', label: 'Error rate', color: STATUS_CRITICAL,
                         points: at((h) => h.errors?.percentage ?? 0) }]} />
          )}
        </>
      )}

      <table className="grid readout">
        <tbody>
          <tr><th>p50 / p95 / p99</th><td>{latency.p50_ms} / {latency.p95_ms} / {latency.p99_ms} ms</td></tr>
          <tr><th>min / mean / max</th><td>{latency.min_ms} / {latency.mean_ms} / {latency.max_ms} ms</td></tr>
          <tr><th>Errors / sec</th><td>{errors.rate.toFixed(2)}</td></tr>
          <tr><th>Total requests</th><td>{total_requests.toLocaleString()}</td></tr>
          <tr><th>Total errors</th><td>{total_errors.toLocaleString()}</td></tr>
        </tbody>
      </table>
    </div>
  )
}

function Stat({ label, value, className = '' }) {
  return (
    <div className={`stat ${className}`}>
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
    </div>
  )
}
