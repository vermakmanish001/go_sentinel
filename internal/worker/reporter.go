package worker

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
	pbmetrics "github.com/vermakmanish001/go_sentinel/proto/metrics"
)

// Reporter streams metrics to the orchestrator
type Reporter struct {
	workerID           string
	metrics            *MetricsCollector
	logger             *zap.Logger
	orchestratorClient pborchestrator.OrchestratorServiceClient
	ctx                context.Context
	cancel             context.CancelFunc
	mu                 sync.RWMutex
	lastReport         time.Time
	reportInterval     time.Duration
}

// NewReporter creates a new metrics reporter
func NewReporter(workerID string, metrics *MetricsCollector, client pborchestrator.OrchestratorServiceClient, logger *zap.Logger) *Reporter {
	ctx, cancel := context.WithCancel(context.Background())

	return &Reporter{
		workerID:           workerID,
		metrics:            metrics,
		logger:             logger,
		orchestratorClient: client,
		ctx:                ctx,
		cancel:             cancel,
		reportInterval:     1 * time.Second,
		lastReport:         time.Now(),
	}
}

// Start starts the reporter goroutine
func (r *Reporter) Start() {
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
	// Get current metrics snapshot
	snapshot := r.metrics.GetSnapshot()

	// Create metric batch
	batch := &pbmetrics.MetricBatch{
		WorkerId:         r.workerID,
		BatchTimestampMs: time.Now().UnixMilli(),
		RpsSnapshot: &pbmetrics.RPSSnapshot{
			Current:       snapshot.RPS.Current,
			Average:       snapshot.RPS.Average,
			Peak:          snapshot.RPS.Peak,
			WindowSeconds: snapshot.RPS.WindowSeconds,
		},
		LatencyHistogram: &pbmetrics.LatencyHistogram{
			MinMs:  int64(snapshot.Latency.Min / time.Millisecond),
			MaxMs:  int64(snapshot.Latency.Max / time.Millisecond),
			MeanMs: int64(snapshot.Latency.Mean / time.Millisecond),
			P50Ms:  int64(snapshot.Latency.P50 / time.Millisecond),
			P95Ms:  int64(snapshot.Latency.P95 / time.Millisecond),
			P99Ms:  int64(snapshot.Latency.P99 / time.Millisecond),
			P999Ms: int64(snapshot.Latency.P999 / time.Millisecond),
			Count:  snapshot.Latency.Count,
		},
		ErrorRate: &pbmetrics.ErrorRate{
			Rate:       snapshot.ErrorRate.Rate,
			Percentage: snapshot.ErrorRate.Percentage,
		},
		TotalRequests: snapshot.TotalRequests,
		TotalErrors:   snapshot.TotalErrors,
	}

	if r.orchestratorClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = r.orchestratorClient.ReportMetrics(ctx, batch)
	}

	r.lastReport = time.Now()
	return nil
}

// Stop stops the reporter
func (r *Reporter) Stop() {
	r.cancel()
}
