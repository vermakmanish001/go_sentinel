package worker

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	pbmetrics "github.com/vermakmanish001/go_sentinel/proto/metrics"
)

// Reporter streams metrics to the orchestrator
type Reporter struct {
	workerID      string
	metrics       *MetricsCollector
	logger        *zap.Logger
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.RWMutex
	stream        pbmetrics.MetricsService_StreamMetricsClient // TODO: Fix this - need proper interface
	lastReport    time.Time
	reportInterval time.Duration
}

// NewReporter creates a new metrics reporter
func NewReporter(workerID string, metrics *MetricsCollector, logger *zap.Logger) *Reporter {
	ctx, cancel := context.WithCancel(context.Background())

	return &Reporter{
		workerID:       workerID,
		metrics:        metrics,
		logger:         logger,
		ctx:            ctx,
		cancel:         cancel,
		reportInterval: 1 * time.Second,
		lastReport:     time.Now(),
	}
}

// Start starts the reporter goroutine
func (r *Reporter) Start(stream interface{}) { // TODO: Use proper interface
	r.mu.Lock()
	// r.stream = stream // TODO: Set stream when interface is defined
	r.mu.Unlock()

	go r.reportLoop()
}

// reportLoop continuously reports metrics
func (r *Reporter) reportLoop() {
	ticker := time.NewTicker(r.reportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			if err := r.report(); err != nil {
				r.logger.Warn("failed to report metrics", zap.Error(err))
			}
		}
	}
}

// report sends a metrics batch to the orchestrator
func (r *Reporter) report() error {
	r.mu.RLock()
	// stream := r.stream // TODO: Use stream
	r.mu.RUnlock()

	// Get current metrics snapshot
	snapshot := r.metrics.GetSnapshot()

	// Create metric batch
	batch := &pbmetrics.MetricBatch{
		WorkerId:        r.workerID,
		BatchTimestampMs: time.Now().UnixMilli(),
		RpsSnapshot: &pbmetrics.RPSSnapshot{
			Current:      snapshot.RPS.Current,
			Average:      snapshot.RPS.Average,
			Peak:         snapshot.RPS.Peak,
			WindowSeconds: snapshot.RPS.WindowSeconds,
		},
		LatencyHistogram: &pbmetrics.LatencyHistogram{
			MinMs:   int64(snapshot.Latency.Min / time.Millisecond),
			MaxMs:   int64(snapshot.Latency.Max / time.Millisecond),
			MeanMs:  int64(snapshot.Latency.Mean / time.Millisecond),
			P50Ms:   int64(snapshot.Latency.P50 / time.Millisecond),
			P95Ms:   int64(snapshot.Latency.P95 / time.Millisecond),
			P99Ms:   int64(snapshot.Latency.P99 / time.Millisecond),
			P999Ms:  int64(snapshot.Latency.P999 / time.Millisecond),
			Count:   snapshot.Latency.Count,
		},
		ErrorRate: &pbmetrics.ErrorRate{
			Rate:       snapshot.ErrorRate.Rate,
			Percentage: snapshot.ErrorRate.Percentage,
		},
	}

	// TODO: Send batch via stream
	_ = batch

	r.lastReport = time.Now()
	return nil
}

// Stop stops the reporter
func (r *Reporter) Stop() {
	r.cancel()
}
