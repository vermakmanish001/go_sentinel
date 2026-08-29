package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
	pbmetrics "github.com/vermakmanish001/go_sentinel/proto/metrics"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
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

	batch := NewMetricBatch("w-1", "test-42", true, snapshot)

	if batch.RpsSnapshot == nil || batch.LatencyHistogram == nil || batch.ErrorRate == nil {
		t.Fatalf("batch has unset sub-messages: rps=%v latency=%v errors=%v",
			batch.RpsSnapshot, batch.LatencyHistogram, batch.ErrorRate)
	}
	if batch.WorkerId != "w-1" {
		t.Errorf("worker id = %q, want %q", batch.WorkerId, "w-1")
	}
	if batch.TestId != "test-42" || !batch.TestActive {
		t.Errorf("test attribution = %q/active=%v, want \"test-42\"/active=true",
			batch.TestId, batch.TestActive)
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

// The orchestrator marks a test complete once every assigned worker reports
// test_active=false, so the final batch must carry that flag.
func TestNewMetricBatchMarksTestFinished(t *testing.T) {
	batch := NewMetricBatch("w-1", "test-42", false, models.MetricSnapshot{})

	if batch.TestActive {
		t.Error("test_active is true on a final batch; the orchestrator will never mark the test complete")
	}
	if batch.TestId != "test-42" {
		t.Errorf("test id = %q, want %q", batch.TestId, "test-42")
	}
}

// fakeOrchestrator records the batches a reporter sends.
type fakeOrchestrator struct {
	pborchestrator.OrchestratorServiceClient
	mu      sync.Mutex
	batches []*pbmetrics.MetricBatch
}

func (f *fakeOrchestrator) ReportMetrics(_ context.Context, in *pbmetrics.MetricBatch, _ ...grpc.CallOption) (*pborchestrator.ReportMetricsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, in)
	return &pborchestrator.ReportMetricsResponse{Accepted: true}, nil
}

func (f *fakeOrchestrator) countFor(testID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.batches {
		if b.TestId == testID && b.TestActive {
			n++
		}
	}
	return n
}

// A reporter is reused for every test the worker runs. If Stop permanently
// cancelled its context, the second test would report nothing at the 1s cadence
// and every run after a worker's first would be recorded at keep-alive
// resolution only.
func TestReporterRestartsForEachTest(t *testing.T) {
	fake := &fakeOrchestrator{}
	r := NewReporter("w-1", NewMetricsCollector(time.Second), fake, zap.NewNop())
	r.reportInterval = 30 * time.Millisecond

	for _, testID := range []string{"test-1", "test-2", "test-3"} {
		r.Start(testID)
		time.Sleep(150 * time.Millisecond)
		r.Stop()

		if got := fake.countFor(testID); got == 0 {
			t.Errorf("%s: reporter sent no active batches; it did not restart", testID)
		}
	}

	// Each Stop must also emit exactly one final batch marking the test done.
	fake.mu.Lock()
	defer fake.mu.Unlock()
	finals := map[string]int{}
	for _, b := range fake.batches {
		if !b.TestActive {
			finals[b.TestId]++
		}
	}
	for _, testID := range []string{"test-1", "test-2", "test-3"} {
		if finals[testID] != 1 {
			t.Errorf("%s: got %d completion batches, want exactly 1", testID, finals[testID])
		}
	}
}
