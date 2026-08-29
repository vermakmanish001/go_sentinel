package orchestrator

import (
	"testing"

	"go.uber.org/zap"

	pbmetrics "github.com/vermakmanish001/go_sentinel/proto/metrics"
)

// The fleet error rate is the sum of the workers' rates. It must not be derived
// from the cumulative error count divided by the gap between reports, which
// reports every error the test has ever seen as if it just happened.
func TestAggregatedErrorRateSumsWorkerRates(t *testing.T) {
	ma := NewMetricsAggregator(zap.NewNop())

	for _, workerID := range []string{"w-1", "w-2"} {
		ma.UpdateWorkerMetrics(workerID, &pbmetrics.MetricBatch{
			WorkerId:         workerID,
			RpsSnapshot:      &pbmetrics.RPSSnapshot{Current: 50},
			LatencyHistogram: &pbmetrics.LatencyHistogram{Count: 1000},
			ErrorRate:        &pbmetrics.ErrorRate{Rate: 2, Percentage: 10},
			TotalRequests:    1000,
			TotalErrors:      100,
		})
	}

	got := ma.GetAggregatedMetrics()

	if got.ErrorRate.Rate != 4 {
		t.Errorf("error rate = %v, want 4 (2/s from each of 2 workers)", got.ErrorRate.Rate)
	}
	if got.ErrorRate.Percentage != 10 {
		t.Errorf("error percentage = %v, want 10 (200 errors / 2000 requests)", got.ErrorRate.Percentage)
	}
	if got.TotalErrors != 200 || got.TotalRequests != 2000 {
		t.Errorf("totals = %d errors / %d requests, want 200 / 2000", got.TotalErrors, got.TotalRequests)
	}
}
