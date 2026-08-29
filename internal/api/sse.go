package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	pbmetrics "github.com/vermakmanish001/go_sentinel/proto/metrics"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
)

// Metrics is the wire shape the dashboard consumes.
type Metrics struct {
	TimestampMs   int64   `json:"timestamp_ms"`
	RPS           RPS     `json:"rps"`
	Latency       Latency `json:"latency"`
	Errors        Errors  `json:"errors"`
	TotalRequests int64   `json:"total_requests"`
	TotalErrors   int64   `json:"total_errors"`
}

type RPS struct {
	Current float64 `json:"current"`
	Average float64 `json:"average"`
	Peak    float64 `json:"peak"`
}

type Latency struct {
	MinMs  int64 `json:"min_ms"`
	MaxMs  int64 `json:"max_ms"`
	MeanMs int64 `json:"mean_ms"`
	P50Ms  int64 `json:"p50_ms"`
	P95Ms  int64 `json:"p95_ms"`
	P99Ms  int64 `json:"p99_ms"`
	Count  int64 `json:"count"`
}

type Errors struct {
	// Rate is errors per second; Percentage is share of all requests.
	Rate       float64 `json:"rate"`
	Percentage float64 `json:"percentage"`
}

func metricsFromProto(s *pbmetrics.MetricSnapshot) *Metrics {
	if s == nil {
		return nil
	}
	m := &Metrics{
		TimestampMs:   s.TimestampMs,
		TotalRequests: s.TotalRequests,
		TotalErrors:   s.TotalErrors,
	}
	if s.Rps != nil {
		m.RPS = RPS{Current: s.Rps.Current, Average: s.Rps.Average, Peak: s.Rps.Peak}
	}
	if s.Latency != nil {
		m.Latency = Latency{
			MinMs: s.Latency.MinMs, MaxMs: s.Latency.MaxMs, MeanMs: s.Latency.MeanMs,
			P50Ms: s.Latency.P50Ms, P95Ms: s.Latency.P95Ms, P99Ms: s.Latency.P99Ms,
			Count: s.Latency.Count,
		}
	}
	if s.ErrorRate != nil {
		m.Errors = Errors{Rate: s.ErrorRate.Rate, Percentage: s.ErrorRate.Percentage}
	}
	return m
}

// handleStreamRun streams a run's metrics as Server-Sent Events, and emits a
// terminal `end` event so the dashboard knows to stop without polling.
func (s *Server) handleStreamRun(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Disable proxy buffering, which would otherwise hold events back.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	stream, err := s.orch.StreamMetrics(ctx, &pborchestrator.StreamMetricsRequest{TestId: testID})
	if err != nil {
		sendEvent(w, flusher, "error", map[string]string{"error": err.Error()})
		return
	}

	// Metrics arrive on the gRPC stream; run status is polled alongside so the
	// dashboard learns about completion even though metrics keep flowing.
	statusCh := make(chan *pborchestrator.TestStatusResponse, 1)
	go s.pollStatus(ctx, testID, statusCh)

	metricsCh := make(chan *pbmetrics.MetricSnapshot)
	go func() {
		defer close(metricsCh)
		for {
			resp, err := stream.Recv()
			if err != nil {
				return
			}
			select {
			case metricsCh <- resp.Snapshot:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case snapshot, open := <-metricsCh:
			if !open {
				sendEvent(w, flusher, "end", map[string]string{"reason": "metrics stream closed"})
				return
			}
			if m := metricsFromProto(snapshot); m != nil {
				sendEvent(w, flusher, "metrics", m)
			}

		case st := <-statusCh:
			sendEvent(w, flusher, "status", RunStatus{
				TestID:        testID,
				Status:        st.Status.String(),
				ActiveWorkers: st.ActiveWorkers,
				TotalVUs:      st.TotalVus,
			})
			if isTerminal(st.Status) {
				// One last metrics read so the dashboard shows final totals
				// rather than whatever arrived a second before the end.
				if final, err := s.orch.GetTestStatus(ctx, &pborchestrator.TestStatusRequest{TestId: testID}); err == nil {
					if m := metricsFromProto(final.CurrentMetrics); m != nil {
						sendEvent(w, flusher, "metrics", m)
					}
				}
				sendEvent(w, flusher, "end", map[string]string{"status": st.Status.String()})
				return
			}
		}
	}
}

func (s *Server) pollStatus(ctx context.Context, testID string, out chan<- *pborchestrator.TestStatusResponse) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			resp, err := s.orch.GetTestStatus(ctx, &pborchestrator.TestStatusRequest{TestId: testID})
			if err != nil {
				continue
			}
			select {
			case out <- resp:
			case <-ctx.Done():
				return
			default: // consumer is busy; the next tick will carry fresher state
			}
		}
	}
}

func sendEvent(w http.ResponseWriter, flusher http.Flusher, event string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body)
	flusher.Flush()
}
