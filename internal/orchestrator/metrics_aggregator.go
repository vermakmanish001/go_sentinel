package orchestrator

import (
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
	pbmetrics "github.com/vermakmanish001/go_sentinel/proto/metrics"
)

// MetricsAggregator aggregates metrics from all workers, keyed by test.
//
// Metrics are scoped per test rather than kept as one global rollup: without
// that, a new run inherits the previous run's totals until every worker has
// reported, and concurrent or back-to-back runs blend into each other.
type MetricsAggregator struct {
	logger *zap.Logger

	mu sync.RWMutex
	// workerMetrics maps testID -> workerID -> that worker's latest snapshot.
	workerMetrics map[string]map[string]*WorkerMetrics
	// aggregated caches the fleet rollup per testID.
	aggregated map[string]models.MetricSnapshot
}

// WorkerMetrics stores metrics from a single worker
type WorkerMetrics struct {
	Snapshot   models.MetricSnapshot
	LastUpdate time.Time
}

// NewMetricsAggregator creates a new metrics aggregator
func NewMetricsAggregator(logger *zap.Logger) *MetricsAggregator {
	return &MetricsAggregator{
		logger:        logger,
		workerMetrics: make(map[string]map[string]*WorkerMetrics),
		aggregated:    make(map[string]models.MetricSnapshot),
	}
}

// UpdateWorkerMetrics records a worker's latest batch for a test.
func (ma *MetricsAggregator) UpdateWorkerMetrics(testID, workerID string, batch *pbmetrics.MetricBatch) {
	if testID == "" {
		// A worker that has never run a test still sends keep-alives; there is
		// no run to attribute them to.
		return
	}

	ma.mu.Lock()
	defer ma.mu.Unlock()

	snapshot := models.MetricSnapshot{
		RPS: models.RPSSnapshot{
			Current:       batch.RpsSnapshot.Current,
			Average:       batch.RpsSnapshot.Average,
			Peak:          batch.RpsSnapshot.Peak,
			WindowSeconds: batch.RpsSnapshot.WindowSeconds,
		},
		Latency: models.LatencyHistogram{
			Min:   time.Duration(batch.LatencyHistogram.MinMs) * time.Millisecond,
			Max:   time.Duration(batch.LatencyHistogram.MaxMs) * time.Millisecond,
			Mean:  time.Duration(batch.LatencyHistogram.MeanMs) * time.Millisecond,
			P50:   time.Duration(batch.LatencyHistogram.P50Ms) * time.Millisecond,
			P95:   time.Duration(batch.LatencyHistogram.P95Ms) * time.Millisecond,
			P99:   time.Duration(batch.LatencyHistogram.P99Ms) * time.Millisecond,
			P999:  time.Duration(batch.LatencyHistogram.P999Ms) * time.Millisecond,
			Count: batch.LatencyHistogram.Count,
		},
		ErrorRate: models.ErrorRate{
			Rate:       batch.ErrorRate.Rate,
			Percentage: batch.ErrorRate.Percentage,
		},
		TotalRequests: batch.TotalRequests,
		TotalErrors:   batch.TotalErrors,
		Timestamp:     time.UnixMilli(batch.BatchTimestampMs),
		WorkerID:      workerID,
	}

	if ma.workerMetrics[testID] == nil {
		ma.workerMetrics[testID] = make(map[string]*WorkerMetrics)
	}
	ma.workerMetrics[testID][workerID] = &WorkerMetrics{
		Snapshot:   snapshot,
		LastUpdate: time.Now(),
	}

	ma.recalculate(testID)
}

// recalculate recalculates the rollup for one test. Caller must hold ma.mu.
func (ma *MetricsAggregator) recalculate(testID string) {
	workers := ma.workerMetrics[testID]
	if len(workers) == 0 {
		return
	}

	var totalRPS, totalAvgRPS, peakRPS, totalErrorRate float64
	var totalRequests, totalErrors int64
	var minLatency, maxLatency, totalLatency time.Duration
	var latencyCount int64

	for _, wm := range workers {
		totalRPS += wm.Snapshot.RPS.Current
		totalAvgRPS += wm.Snapshot.RPS.Average
		if wm.Snapshot.RPS.Peak > peakRPS {
			peakRPS = wm.Snapshot.RPS.Peak
		}
		totalRequests += wm.Snapshot.TotalRequests
		totalErrors += wm.Snapshot.TotalErrors
		totalErrorRate += wm.Snapshot.ErrorRate.Rate
	}

	// Aggregate latency (count-weighted mean, true min/max)
	for _, wm := range workers {
		if wm.Snapshot.Latency.Count > 0 {
			if minLatency == 0 || wm.Snapshot.Latency.Min < minLatency {
				minLatency = wm.Snapshot.Latency.Min
			}
			if wm.Snapshot.Latency.Max > maxLatency {
				maxLatency = wm.Snapshot.Latency.Max
			}
			totalLatency += wm.Snapshot.Latency.Mean * time.Duration(wm.Snapshot.Latency.Count)
			latencyCount += wm.Snapshot.Latency.Count
		}
	}

	meanLatency := time.Duration(0)
	if latencyCount > 0 {
		meanLatency = totalLatency / time.Duration(latencyCount)
	}

	// Percentiles are averaged across workers, which is statistically wrong —
	// merging the HDR histograms would be correct, but the raw histograms are
	// not shipped over the wire.
	var p50, p95, p99, p999 time.Duration
	var totalP50, totalP95, totalP99, totalP999 time.Duration
	count := 0
	for _, wm := range workers {
		if wm.Snapshot.Latency.Count > 0 {
			totalP50 += wm.Snapshot.Latency.P50
			totalP95 += wm.Snapshot.Latency.P95
			totalP99 += wm.Snapshot.Latency.P99
			totalP999 += wm.Snapshot.Latency.P999
			count++
		}
	}
	if count > 0 {
		p50 = totalP50 / time.Duration(count)
		p95 = totalP95 / time.Duration(count)
		p99 = totalP99 / time.Duration(count)
		p999 = totalP999 / time.Duration(count)
	}

	// Each worker already reports errors-per-second over its own test duration,
	// so the fleet rate is the sum of the worker rates — the same way RPS is
	// combined above. Dividing the cumulative error count by the time since the
	// last aggregation would report every error the test has ever seen as if it
	// happened in the last second.
	errorPercentage := float64(0)
	if totalRequests > 0 {
		errorPercentage = (float64(totalErrors) / float64(totalRequests)) * 100
	}

	ma.aggregated[testID] = models.MetricSnapshot{
		RPS: models.RPSSnapshot{
			Current:       totalRPS,
			Average:       totalAvgRPS,
			Peak:          peakRPS,
			WindowSeconds: 10,
		},
		Latency: models.LatencyHistogram{
			Min:   minLatency,
			Max:   maxLatency,
			Mean:  meanLatency,
			P50:   p50,
			P95:   p95,
			P99:   p99,
			P999:  p999,
			Count: latencyCount,
		},
		ErrorRate: models.ErrorRate{
			Rate:       totalErrorRate,
			Percentage: errorPercentage,
		},
		TotalRequests: totalRequests,
		TotalErrors:   totalErrors,
		Timestamp:     time.Now(),
	}
}

// GetAggregatedMetrics returns the rollup for one test.
func (ma *MetricsAggregator) GetAggregatedMetrics(testID string) models.MetricSnapshot {
	ma.mu.RLock()
	defer ma.mu.RUnlock()
	return ma.aggregated[testID]
}

// GetWorkerMetrics returns metrics for a specific worker on a specific test.
func (ma *MetricsAggregator) GetWorkerMetrics(testID, workerID string) (models.MetricSnapshot, bool) {
	ma.mu.RLock()
	defer ma.mu.RUnlock()

	wm, ok := ma.workerMetrics[testID][workerID]
	if !ok {
		return models.MetricSnapshot{}, false
	}
	return wm.Snapshot, true
}

// RemoveTest drops all metrics held for a test.
func (ma *MetricsAggregator) RemoveTest(testID string) {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	delete(ma.workerMetrics, testID)
	delete(ma.aggregated, testID)
}
