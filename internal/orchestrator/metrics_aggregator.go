package orchestrator

import (
	"sync"
	"time"

	"go.uber.org/zap"

	pbmetrics "github.com/vermakmanish001/go_sentinel/proto/metrics"
	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// MetricsAggregator aggregates metrics from all workers
type MetricsAggregator struct {
	logger      *zap.Logger
	workerMetrics map[string]*WorkerMetrics
	mu          sync.RWMutex
	aggregated  models.MetricSnapshot
	lastUpdate  time.Time
}

// WorkerMetrics stores metrics from a single worker
type WorkerMetrics struct {
	Snapshot   models.MetricSnapshot
	LastUpdate time.Time
	mu         sync.RWMutex
}

// NewMetricsAggregator creates a new metrics aggregator
func NewMetricsAggregator(logger *zap.Logger) *MetricsAggregator {
	return &MetricsAggregator{
		logger:        logger,
		workerMetrics: make(map[string]*WorkerMetrics),
		aggregated:    models.MetricSnapshot{},
		lastUpdate:    time.Now(),
	}
}

// UpdateWorkerMetrics updates metrics from a worker
func (ma *MetricsAggregator) UpdateWorkerMetrics(workerID string, batch *pbmetrics.MetricBatch) {
	ma.mu.Lock()
	defer ma.mu.Unlock()

	// Convert proto to models
	snapshot := models.MetricSnapshot{
		RPS: models.RPSSnapshot{
			Current:      batch.RpsSnapshot.Current,
			Average:      batch.RpsSnapshot.Average,
			Peak:         batch.RpsSnapshot.Peak,
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

	ma.workerMetrics[workerID] = &WorkerMetrics{
		Snapshot:   snapshot,
		LastUpdate: time.Now(),
	}

	// Recalculate aggregated metrics
	ma.recalculate()
}

// recalculate recalculates aggregated metrics from all workers
func (ma *MetricsAggregator) recalculate() {
	if len(ma.workerMetrics) == 0 {
		return
	}

	var totalRPS, totalAvgRPS, peakRPS float64
	var totalRequests, totalErrors int64
	var minLatency, maxLatency, totalLatency time.Duration
	var latencyCount int64

	// Aggregate RPS and requests
	for _, wm := range ma.workerMetrics {
		totalRPS += wm.Snapshot.RPS.Current
		totalAvgRPS += wm.Snapshot.RPS.Average
		if wm.Snapshot.RPS.Peak > peakRPS {
			peakRPS = wm.Snapshot.RPS.Peak
		}
		totalRequests += wm.Snapshot.TotalRequests
		totalErrors += wm.Snapshot.TotalErrors
	}

	// Aggregate latency (weighted average)
	for _, wm := range ma.workerMetrics {
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

	// Calculate percentiles (simplified - in production, merge histograms)
	var p50, p95, p99, p999 time.Duration
	if len(ma.workerMetrics) > 0 {
		// Use average of worker percentiles (not ideal, but simple)
		var totalP50, totalP95, totalP99, totalP999 time.Duration
		count := 0
		for _, wm := range ma.workerMetrics {
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
	}

	// Calculate error rate
	errorRate := float64(0)
	errorPercentage := float64(0)
	if totalRequests > 0 {
		errorRate = float64(totalErrors) / time.Since(ma.lastUpdate).Seconds()
		errorPercentage = (float64(totalErrors) / float64(totalRequests)) * 100
	}

	ma.aggregated = models.MetricSnapshot{
		RPS: models.RPSSnapshot{
			Current:      totalRPS,
			Average:      totalAvgRPS,
			Peak:         peakRPS,
			WindowSeconds: 10, // TODO: Get from config
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
			Rate:       errorRate,
			Percentage: errorPercentage,
		},
		TotalRequests: totalRequests,
		TotalErrors:   totalErrors,
		Timestamp:     time.Now(),
	}

	ma.lastUpdate = time.Now()
}

// GetAggregatedMetrics returns the aggregated metrics snapshot
func (ma *MetricsAggregator) GetAggregatedMetrics() models.MetricSnapshot {
	ma.mu.RLock()
	defer ma.mu.RUnlock()
	return ma.aggregated
}

// GetWorkerMetrics returns metrics for a specific worker
func (ma *MetricsAggregator) GetWorkerMetrics(workerID string) (models.MetricSnapshot, bool) {
	ma.mu.RLock()
	defer ma.mu.RUnlock()

	wm, ok := ma.workerMetrics[workerID]
	if !ok {
		return models.MetricSnapshot{}, false
	}

	return wm.Snapshot, true
}

// RemoveWorker removes metrics for a worker
func (ma *MetricsAggregator) RemoveWorker(workerID string) {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	delete(ma.workerMetrics, workerID)
	ma.recalculate()
}
