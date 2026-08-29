const fmtTime = (ms) => new Date(ms).toLocaleString(undefined,
  { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })

const fmtDuration = (run) => {
  if (!run.finished_at) return '—'
  const s = Math.round((run.finished_at - run.started_at) / 1000)
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`
}

export default function HistoryPanel({ runs, activeId, onSelect, onDelete, onReplay }) {
  return (
    <div className="card">
      <h2>Run history</h2>
      {runs.length === 0 ? (
        <p className="hint">No runs recorded yet.</p>
      ) : (
        <table className="grid history">
          <thead>
            <tr><th>Run</th><th>Status</th><th>Peak RPS</th><th>Errors</th><th>Duration</th><th /></tr>
          </thead>
          <tbody>
            {runs.map((r) => (
              <tr key={r.id} className={r.id === activeId ? 'selected' : ''}
                  onClick={() => onSelect(r)}>
                <td>
                  <div className="run-name">{r.name || r.id}</div>
                  <div className="run-time">{fmtTime(r.started_at)}</div>
                </td>
                <td><span className={`pill ${r.status.toLowerCase()}`}>{r.status}</span></td>
                <td className="num">{r.peak_rps ? r.peak_rps.toFixed(0) : '—'}</td>
                <td className={`num ${r.error_pct > 1 ? 'bad-text' : ''}`}>
                  {r.total_requests ? `${r.error_pct.toFixed(1)}%` : '—'}
                </td>
                <td className="num">{fmtDuration(r)}</td>
                <td className="rowactions">
                  <button type="button" className="icon" title="Load this run's plan"
                    onClick={(e) => { e.stopPropagation(); onReplay(r) }}>↺</button>
                  <button type="button" className="icon" title="Delete run"
                    onClick={(e) => { e.stopPropagation(); onDelete(r) }}>✕</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
