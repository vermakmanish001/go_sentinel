export default function WorkersPanel({ workers }) {
  return (
    <div className="card">
      <h2>Workers</h2>
      {workers.length === 0 ? (
        <p className="hint">No workers registered. Start the stack with <code>make up</code>.</p>
      ) : (
        <table className="grid">
          <thead>
            <tr><th>ID</th><th>Status</th><th>Max VUs</th></tr>
          </thead>
          <tbody>
            {workers.map((w) => (
              <tr key={w.id}>
                <td className="mono">{w.id}</td>
                <td><span className={`pill ${w.status}`}>{w.status}</span></td>
                <td>{w.max_vus.toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
