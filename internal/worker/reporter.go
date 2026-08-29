package worker

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
	pbmetrics "github.com/vermakmanish001/go_sentinel/proto/metrics"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
)

// Reporter streams metrics to the orchestrator for the duration of a test.
type Reporter struct {
	workerID           string
	metrics            *MetricsCollector
	logger             *zap.Logger
	orchestratorClient pborchestrator.OrchestratorServiceClient
	reportInterval     time.Duration

	// cancel stops the in-flight report loop. It is replaced on every Start and
	// cleared by Stop so the reporter can be restarted for each test. A single
	// long-lived context would stay cancelled after the first test finished,
	// silently reducing every later test to the 10s keep-alive cadence.
	mu     sync.Mutex
	cancel context.CancelFunc
	testID string
}

// NewReporter creates a new metrics reporter
func NewReporter(workerID string, metrics *MetricsCollector, client pborchestrator.OrchestratorServiceClient, logger *zap.Logger) *Reporter {
	return &Reporter{
		workerID:           workerID,
		metrics:            metrics,
		logger:             logger,
		orchestratorClient: client,
		reportInterval:     1 * time.Second,
	}
}

// Start begins reporting metrics for testID, stopping any previous loop first.
func (r *Reporter) Start(testID string) {
	ctx, cancel := context.WithCancel(context.Background())

	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	r.cancel = cancel
	r.testID = testID
	r.mu.Unlock()

	go r.reportLoop(ctx)
}

// reportLoop continuously reports metrics until its context is cancelled.
func (r *Reporter) reportLoop(ctx context.Context) {
	ticker := time.NewTicker(r.reportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.report(true)
		}
	}
}

// Stop halts reporting and sends one final batch marking the test finished.
// That final batch is how the orchestrator learns this worker is done, so it
// is sent even though the loop has already been cancelled.
func (r *Reporter) Stop() {
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.mu.Unlock()

	r.report(false)
}

// report sends a metrics batch to the orchestrator.
func (r *Reporter) report(testActive bool) {
	r.mu.Lock()
	testID := r.testID
	r.mu.Unlock()

	if r.orchestratorClient == nil {
		return
	}

	batch := NewMetricBatch(r.workerID, testID, testActive, r.metrics.GetSnapshot())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := r.orchestratorClient.ReportMetrics(ctx, batch); err != nil {
		r.logger.Warn("failed to report metrics", zap.Error(err))
	}
}

// NewMetricBatch converts a metrics snapshot into a batch for the orchestrator.
//
// Every batch sent to the orchestrator must go through here. The orchestrator's
// aggregator replaces a worker's entry wholesale, so a batch that leaves RPS,
// latency or error rate unset drops that worker's contribution to the fleet
// totals to zero until its next full report.
//
// testActive must reflect whether the worker is still executing testID: the
// orchestrator marks a test complete once every assigned worker reports false.
func NewMetricBatch(workerID, testID string, testActive bool, snapshot models.MetricSnapshot) *pbmetrics.MetricBatch {
	return &pbmetrics.MetricBatch{
		WorkerId:         workerID,
		TestId:           testID,
		TestActive:       testActive,
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
}
