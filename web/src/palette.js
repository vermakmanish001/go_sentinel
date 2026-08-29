// Chart palette, dark-mode steps, validated against the panel surface #171a21
// with the categorical / ordinal checks (lightness band, chroma floor, CVD
// separation, normal-vision floor, contrast). Do not substitute values without
// re-running that validation.

// Categorical slots, assigned in fixed order and never cycled.
export const SERIES = ['#3987e5', '#d95926', '#199e70', '#c98500', '#d55181']

// Comparison is capped at the number of validated slots rather than generating
// a sixth hue.
export const MAX_COMPARE = SERIES.length

// Latency percentiles are ordered steps of one measure, so they use a single
// blue ramp rather than unrelated hues. Inverted for the dark surface: the
// highest percentile is the brightest, so p99 reads as the prominent line.
export const LATENCY = { p50: '#1c5cab', p95: '#3987e5', p99: '#86b6ef' }

// Error rate is a status measure, not a series; this step is reserved for it
// and always ships beside a text label.
export const STATUS_CRITICAL = '#d03b3b'
