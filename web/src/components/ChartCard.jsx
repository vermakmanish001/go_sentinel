import { useState } from 'react'
import LineChart from './LineChart.jsx'

/**
 * A titled chart with a legend and a table view.
 *
 * The legend appears only for two or more series — with one, the title already
 * names it. The table view is the non-visual path to the same numbers.
 */
export default function ChartCard({ title, series, unit = '', height = 180 }) {
  const [asTable, setAsTable] = useState(false)
  const multi = series.length > 1

  return (
    <div className="chart-card">
      <div className="chart-head">
        <h3>{title}</h3>
        <button type="button" className="link" onClick={() => setAsTable((v) => !v)}>
          {asTable ? 'Chart' : 'Table'}
        </button>
      </div>

      {multi && (
        <div className="legend">
          {series.map((s) => (
            <span className="legend-item" key={s.id}>
              <span className="swatch" style={{ background: s.color }} />
              {s.label}
            </span>
          ))}
        </div>
      )}

      {asTable ? <SeriesTable series={series} unit={unit} /> : <LineChart series={series} unit={unit} height={height} />}
    </div>
  )
}

function SeriesTable({ series, unit }) {
  const xs = [...new Set(series.flatMap((s) => s.points.map((p) => p.x)))].sort((a, b) => a - b)
  return (
    <div className="table-scroll">
      <table className="grid">
        <thead>
          <tr><th>t</th>{series.map((s) => <th key={s.id}>{s.label}</th>)}</tr>
        </thead>
        <tbody>
          {xs.map((x) => (
            <tr key={x}>
              <td className="num">{x}s</td>
              {series.map((s) => {
                const p = s.points.find((q) => q.x === x)
                return <td className="num" key={s.id}>{p ? `${p.y}${unit}` : '—'}</td>
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
