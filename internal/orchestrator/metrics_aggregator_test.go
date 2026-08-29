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
		ma.UpdateWorkerMetrics("test-1", workerID, &pbmetrics.MetricBatch{
			WorkerId:         workerID,
			TestId:           "test-1",
			RpsSnapshot:      &pbmetrics.RPSSnapshot{Current: 50},
			LatencyHistogram: &pbmetrics.LatencyHistogram{Count: 1000},
			ErrorRate:        &pbmetrics.ErrorRate{Rate: 2, Percentage: 10},
			TotalRequests:    1000,
			TotalErrors:      100,
		})
	}

	got := ma.GetAggregatedMetrics("test-1")

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

// Metrics are scoped per test: a new run must not inherit the previous run's
// totals, and two runs must never blend into one rollup.
func TestAggregatedMetricsAreScopedPerTest(t *testing.T) {
	ma := NewMetricsAggregator(zap.NewNop())

	batch := func(testID string, requests int64) *pbmetrics.MetricBatch {
		return &pbmetrics.MetricBatch{
			WorkerId:         "w-1",
			TestId:           testID,
			RpsSnapshot:      &pbmetrics.RPSSnapshot{Current: 10},
			LatencyHistogram: &pbmetrics.LatencyHistogram{Count: requests},
			ErrorRate:        &pbmetrics.ErrorRate{},
			TotalRequests:    requests,
		}
	}

	ma.UpdateWorkerMetrics("test-old", "w-1", batch("test-old", 5000))
	ma.UpdateWorkerMetrics("test-new", "w-1", batch("test-new", 12))

	if got := ma.GetAggregatedMetrics("test-new").TotalRequests; got != 12 {
		t.Errorf("new test shows %d requests, want 12 (leaked from the previous run)", got)
	}
	if got := ma.GetAggregatedMetrics("test-old").TotalRequests; got != 5000 {
		t.Errorf("old test shows %d requests, want 5000", got)
	}
	if got := ma.GetAggregatedMetrics("never-ran").TotalRequests; got != 0 {
		t.Errorf("unknown test shows %d requests, want 0", got)
	}

	ma.RemoveTest("test-old")
	if got := ma.GetAggregatedMetrics("test-old").TotalRequests; got != 0 {
		t.Errorf("removed test still reports %d requests", got)
	}
}
