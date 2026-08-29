async function request(path, options) {
  const res = await fetch(path, options)
  const text = await res.text()
  let body = null
  try {
    body = text ? JSON.parse(text) : null
  } catch {
    throw new Error(text || `HTTP ${res.status}`)
  }
  if (!res.ok) throw new Error(body?.error || `HTTP ${res.status}`)
  return body
}

export const getHealth = () => request('/api/health')
export const getWorkers = () => request('/api/workers')
export const getRun = (id) => request(`/api/runs/${id}`)

export const startRun = (plan) =>
  request('/api/runs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(plan),
  })

export const stopRun = (id) =>
  request(`/api/runs/${id}/stop`, { method: 'POST' })

// streamRun subscribes to a run's Server-Sent Events. Returns a close function.
export function streamRun(id, { onMetrics, onStatus, onEnd, onError }) {
  const source = new EventSource(`/api/runs/${id}/stream`)

  source.addEventListener('metrics', (e) => onMetrics?.(JSON.parse(e.data)))
  source.addEventListener('status', (e) => onStatus?.(JSON.parse(e.data)))
  source.addEventListener('end', (e) => {
    onEnd?.(JSON.parse(e.data))
    source.close()
  })
  source.addEventListener('error', () => {
    // EventSource fires this on network drops too; the caller decides.
    onError?.()
  })

  return () => source.close()
}
