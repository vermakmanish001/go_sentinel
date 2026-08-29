export default function PlansPanel({ plans, onLoad, onSave, onDelete, disabled }) {
  return (
    <div className="card">
      <h2>Saved plans</h2>
      <button type="button" className="link" onClick={onSave} disabled={disabled}>
        + Save current plan
      </button>
      {plans.length > 0 && (
        <table className="grid">
          <tbody>
            {plans.map((p) => (
              <tr key={p.id}>
                <td>{p.name}</td>
                <td className="rowactions">
                  <button type="button" className="link" onClick={() => onLoad(p)} disabled={disabled}>Load</button>
                  <button type="button" className="icon" onClick={() => onDelete(p)}>✕</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
