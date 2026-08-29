import ChartCard from './ChartCard.jsx'

/**
 * Overlays several runs' throughput on one plot.
 *
 * Runs are indexed to elapsed seconds from their own start, not wall-clock
 * time, so runs from different days line up and are actually comparable.
 */
export default function ComparePanel({ runs, colorFor, onClear }) {
  if (runs.length < 2) return null

  const series = runs.map((r) => ({
    id: r.run.id,
    label: r.run.name || r.run.id,
    color: colorFor(r.run.id),
    points: r.samples.map((s) => ({
      x: Math.round((s.ts_ms - r.samples[0].ts_ms) / 1000),
      y: Math.round(s.rps),
    })),
  }))

  return (
    <div className="card">
      <div className="chart-head">
        <h2>Comparing {runs.length} runs</h2>
        <button type="button" className="link" onClick={onClear}>Clear</button>
      </div>

      <ChartCard title="Requests/sec by elapsed time" series={series} height={200} />

      <table className="grid compare">
        <thead>
          <tr><th>Run</th><th>Peak RPS</th><th>Avg RPS</th><th>p95</th><th>p99</th><th>Errors</th><th>Requests</th></tr>
        </thead>
        <tbody>
          {runs.map(({ run }) => (
            <tr key={run.id}>
              <td>
                <span className="swatch" style={{ background: colorFor(run.id) }} />
                {run.name || run.id}
              </td>
              <td className="num">{run.peak_rps.toFixed(0)}</td>
              <td className="num">{run.avg_rps.toFixed(1)}</td>
              <td className="num">{run.p95_ms}ms</td>
              <td className="num">{run.p99_ms}ms</td>
              <td className={`num ${run.error_pct > 1 ? 'bad-text' : ''}`}>{run.error_pct.toFixed(2)}%</td>
              <td className="num">{run.total_requests.toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
