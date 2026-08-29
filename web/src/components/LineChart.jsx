import { useLayoutEffect, useRef, useState } from 'react'

// Measure the container so the chart renders at true pixel size; scaling a
// fixed viewBox would scale the type along with it.
function useWidth() {
  const ref = useRef(null)
  const [width, setWidth] = useState(0)
  useLayoutEffect(() => {
    if (!ref.current) return
    const ro = new ResizeObserver(([entry]) => setWidth(entry.contentRect.width))
    ro.observe(ref.current)
    return () => ro.disconnect()
  }, [])
  return [ref, width]
}

const PAD = { top: 10, right: 12, bottom: 26, left: 44 }

// niceTicks returns rounded axis values, so gridlines land on readable numbers.
//
// The top tick is rounded *up* past the data max: stopping at the last tick
// below it would let the line overflow the top of the plot. Ticks are indexed
// rather than accumulated so fractional steps do not drift.
function niceTicks(max, count = 4) {
  if (max <= 0) return [0, 1]
  const raw = max / count
  const mag = 10 ** Math.floor(Math.log10(raw))
  const step = [1, 2, 2.5, 5, 10].find((m) => m * mag >= raw) * mag
  const steps = Math.ceil(max / step)
  return Array.from({ length: steps + 1 }, (_, i) => Number((i * step).toFixed(6)))
}

const fmt = (v) =>
  v >= 1000 ? `${(v / 1000).toFixed(v >= 10000 ? 0 : 1)}k`
  : v >= 10 ? v.toFixed(0)
  : Number.isInteger(v) ? String(v) : v.toFixed(1)

/**
 * A time-series line chart.
 *
 * One measure per chart: throughput, latency and error rate live on separate
 * charts rather than sharing a plot with two y-scales, which would let the
 * relative position of two lines imply a relationship that isn't there.
 */
export default function LineChart({ series, height = 180, unit = '', xUnit = 's' }) {
  const [ref, width] = useWidth()
  const [hover, setHover] = useState(null)

  const visible = series.filter((s) => s.points.length > 0)
  const maxX = Math.max(1, ...visible.flatMap((s) => s.points.map((p) => p.x)))
  const maxYRaw = Math.max(...visible.flatMap((s) => s.points.map((p) => p.y)), 0)
  const yTicks = niceTicks(maxYRaw || 1)
  const maxY = yTicks[yTicks.length - 1]

  const plotW = Math.max(0, width - PAD.left - PAD.right)
  const plotH = height - PAD.top - PAD.bottom
  const sx = (x) => PAD.left + (x / maxX) * plotW
  const sy = (y) => PAD.top + plotH - (y / maxY) * plotH

  const xTickCount = Math.max(2, Math.min(6, Math.floor(plotW / 90)))
  const xTicks = Array.from({ length: xTickCount + 1 }, (_, i) => (maxX / xTickCount) * i)

  function onMove(e) {
    if (!plotW) return
    const rect = e.currentTarget.getBoundingClientRect()
    const px = e.clientX - rect.left
    if (px < PAD.left || px > PAD.left + plotW) return setHover(null)
    const xVal = ((px - PAD.left) / plotW) * maxX
    const rows = visible.map((s) => {
      let best = s.points[0]
      for (const p of s.points) {
        if (Math.abs(p.x - xVal) < Math.abs(best.x - xVal)) best = p
      }
      return { series: s, point: best }
    }).filter((r) => r.point)
    if (rows.length) setHover({ px, x: rows[0].point.x, rows })
  }

  if (!visible.length) {
    return <div ref={ref} className="chart-empty" style={{ height }}>No data</div>
  }

  return (
    <div ref={ref} className="chart-wrap" style={{ height }}>
      {width > 0 && (
        <svg width={width} height={height} role="img"
             onMouseMove={onMove} onMouseLeave={() => setHover(null)}>
          {yTicks.map((t) => (
            <g key={t}>
              <line className="grid" x1={PAD.left} x2={width - PAD.right} y1={sy(t)} y2={sy(t)} />
              <text className="axis" x={PAD.left - 8} y={sy(t)} textAnchor="end" dominantBaseline="middle">
                {fmt(t)}
              </text>
            </g>
          ))}

          {xTicks.map((t, i) => (
            <text key={i} className="axis" x={sx(t)} y={height - 8} textAnchor="middle">
              {fmt(t)}{xUnit}
            </text>
          ))}

          {visible.map((s) => (
            <path key={s.id} className="series-line" stroke={s.color}
                  d={s.points.map((p, i) => `${i ? 'L' : 'M'}${sx(p.x).toFixed(1)},${sy(p.y).toFixed(1)}`).join(' ')} />
          ))}

          {hover && (
            <>
              <line className="crosshair" x1={hover.px} x2={hover.px} y1={PAD.top} y2={PAD.top + plotH} />
              {hover.rows.map(({ series: s, point }) => (
                <circle key={s.id} cx={sx(point.x)} cy={sy(point.y)} r="4"
                        fill={s.color} className="marker" />
              ))}
            </>
          )}
        </svg>
      )}

      {hover && (
        <div className="tooltip" style={{
          left: Math.min(Math.max(hover.px + 12, 0), Math.max(0, width - 190)),
        }}>
          <div className="tt-x">{fmt(hover.x)}{xUnit}</div>
          {hover.rows.map(({ series: s, point }) => (
            <div className="tt-row" key={s.id}>
              <span className="swatch" style={{ background: s.color }} />
              <span className="tt-label">{s.label}</span>
              <span className="tt-val">{fmt(point.y)}{unit}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
