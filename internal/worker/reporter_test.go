package worker

import (
	"testing"
	"time"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// The orchestrator's aggregator replaces a worker's entry wholesale on every
// report, so every batch must carry the full snapshot. A batch that left RPS,
// latency or error rate unset would blank out this worker's contribution to the
// fleet totals until its next full report.
func TestNewMetricBatchIsComplete(t *testing.T) {
	snapshot := models.MetricSnapshot{
		RPS:     models.RPSSnapshot{Current: 50, Average: 40, Peak: 75, WindowSeconds: 10},
		Latency: models.LatencyHistogram{Mean: 20 * time.Millisecond, P99: 90 * time.Millisecond, Count: 1234},
		ErrorRate: models.ErrorRate{
			Rate:       2.5,
			Percentage: 5,
		},
		TotalRequests: 1234,
		TotalErrors:   62,
	}

	batch := NewMetricBatch("w-1", snapshot)

	if batch.RpsSnapshot == nil || batch.LatencyHistogram == nil || batch.ErrorRate == nil {
		t.Fatalf("batch has unset sub-messages: rps=%v latency=%v errors=%v",
			batch.RpsSnapshot, batch.LatencyHistogram, batch.ErrorRate)
	}
	if batch.WorkerId != "w-1" {
		t.Errorf("worker id = %q, want %q", batch.WorkerId, "w-1")
	}
	if batch.RpsSnapshot.Current != 50 || batch.RpsSnapshot.Peak != 75 {
		t.Errorf("rps = %+v, want current 50 / peak 75", batch.RpsSnapshot)
	}
	if batch.LatencyHistogram.P99Ms != 90 || batch.LatencyHistogram.Count != 1234 {
		t.Errorf("latency = %+v, want p99 90ms / count 1234", batch.LatencyHistogram)
	}
	if batch.ErrorRate.Rate != 2.5 || batch.ErrorRate.Percentage != 5 {
		t.Errorf("error rate = %+v, want rate 2.5 / percentage 5", batch.ErrorRate)
	}
	if batch.TotalRequests != 1234 || batch.TotalErrors != 62 {
		t.Errorf("totals = %d / %d, want 1234 / 62", batch.TotalRequests, batch.TotalErrors)
	}
	if batch.BatchTimestampMs == 0 {
		t.Error("batch timestamp not set")
	}
}
