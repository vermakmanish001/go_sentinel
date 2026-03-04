package tracer

import (
	"sync"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
	"go.uber.org/zap"
)

// SpanAggregator aggregates span durations and computes percentiles
type SpanAggregator struct {
	logger    *zap.Logger
	histogram *hdrhistogram.Histogram
	mu        sync.RWMutex
	count     int64
	startTime time.Time
}

// NewSpanAggregator creates a new span aggregator
func NewSpanAggregator(logger *zap.Logger) *SpanAggregator {
	// Create HDR histogram with 1ms to 1 hour range, 3 significant digits
	hist := hdrhistogram.New(1, 3600000, 3)
	return &SpanAggregator{
		logger:    logger,
		histogram: hist,
		startTime: time.Now(),
	}
}

// Record records a span duration in milliseconds
func (sa *SpanAggregator) Record(duration time.Duration) {
	sa.mu.Lock()
	defer sa.mu.Unlock()

	durationMs := int64(duration / time.Millisecond)
	if err := sa.histogram.RecordValue(durationMs); err != nil {
		sa.logger.Warn("failed to record span duration", zap.Error(err))
		return
	}
	sa.count++
}

// GetPercentiles returns latency percentiles
func (sa *SpanAggregator) GetPercentiles() (min, max, mean, p50, p95, p99, p999 time.Duration, count int64) {
	sa.mu.RLock()
	defer sa.mu.RUnlock()

	if sa.count == 0 {
		return 0, 0, 0, 0, 0, 0, 0, 0
	}

	min = time.Duration(sa.histogram.Min()) * time.Millisecond
	max = time.Duration(sa.histogram.Max()) * time.Millisecond
	mean = time.Duration(sa.histogram.Mean()) * time.Millisecond
	p50 = time.Duration(sa.histogram.ValueAtQuantile(50)) * time.Millisecond
	p95 = time.Duration(sa.histogram.ValueAtQuantile(95)) * time.Millisecond
	p99 = time.Duration(sa.histogram.ValueAtQuantile(99)) * time.Millisecond
	p999 = time.Duration(sa.histogram.ValueAtQuantile(99.9)) * time.Millisecond
	count = sa.count

	return
}

// Reset resets the aggregator
func (sa *SpanAggregator) Reset() {
	sa.mu.Lock()
	defer sa.mu.Unlock()

	sa.histogram.Reset()
	sa.count = 0
	sa.startTime = time.Now()
}

// GetCount returns the total number of recorded spans
func (sa *SpanAggregator) GetCount() int64 {
	sa.mu.RLock()
	defer sa.mu.RUnlock()
	return sa.count
}
