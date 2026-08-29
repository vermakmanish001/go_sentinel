export default function MetricsPanel({ metrics, history }) {
  if (!metrics) {
    return (
      <div className="card">
        <h2>Live metrics</h2>
        <p className="hint">Run a test to see throughput, latency and errors here.</p>
      </div>
    )
  }

  const { rps, latency, errors, total_requests, total_errors } = metrics
  const errClass = errors.percentage > 5 ? 'bad' : errors.percentage > 1 ? 'warn' : 'good'

  return (
    <div className="card">
      <h2>Live metrics</h2>

      <div className="stats">
        <Stat label="Requests/sec" value={rps.current.toFixed(1)} />
        <Stat label="Average" value={rps.average.toFixed(1)} />
        <Stat label="Peak" value={rps.peak.toFixed(1)} />
        <Stat label="Errors" value={`${errors.percentage.toFixed(2)}%`} className={errClass} />
      </div>

      <Sparkline points={history.map((h) => h.rps.current)} />

      <table className="grid readout">
        <tbody>
          <tr><th>p50 / p95 / p99</th>
              <td>{latency.p50_ms} / {latency.p95_ms} / {latency.p99_ms} ms</td></tr>
          <tr><th>min / mean / max</th>
              <td>{latency.min_ms} / {latency.mean_ms} / {latency.max_ms} ms</td></tr>
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

// Sparkline of recent throughput. Inline SVG keeps the bundle dependency-free.
function Sparkline({ points }) {
  if (points.length < 2) return <div className="sparkline empty" />

  const w = 100
  const h = 28
  const max = Math.max(...points, 1)
  const step = w / (points.length - 1)
  const d = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${(i * step).toFixed(2)},${(h - (p / max) * h).toFixed(2)}`).join(' ')

  return (
    <svg className="sparkline" viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" role="img"
         aria-label={`Throughput trend, peak ${max.toFixed(0)} requests per second`}>
      <path d={`${d} L${w},${h} L0,${h} Z`} className="spark-fill" />
      <path d={d} className="spark-line" vectorEffect="non-scaling-stroke" />
    </svg>
  )
}
