export default function HeaderRows({ label, rows, onChange }) {
  const set = (i, patch) => onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))

  return (
    <div className="headers">
      <span className="sublabel">{label}</span>
      {rows.map((row, i) => (
        <div className="row tight" key={i}>
          <input placeholder="Header" value={row.key} onChange={(e) => set(i, { key: e.target.value })} />
          <input placeholder="Value" value={row.value} onChange={(e) => set(i, { value: e.target.value })} />
          <button type="button" className="icon" onClick={() => onChange(rows.filter((_, j) => j !== i))}>✕</button>
        </div>
      ))}
      <button type="button" className="link" onClick={() => onChange([...rows, { key: '', value: '' }])}>
        + Add header
      </button>
    </div>
  )
}
