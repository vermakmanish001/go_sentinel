package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	pbmetrics "github.com/vermakmanish001/go_sentinel/proto/metrics"
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

// handleStreamRun streams a run's metrics as Server-Sent Events.
//
// It subscribes to the recorder rather than opening its own gRPC stream, so
// every dashboard watching a run sees exactly the samples that were persisted,
// and N viewers cost one orchestrator stream rather than N.
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

	sess, live := s.recorder.Session(testID)
	if !live {
		// The run already finished. Report its stored outcome and close, rather
		// than holding a connection open that will never produce an event; the
		// dashboard fetches the recorded series separately.
		run, err := s.store.GetRun(r.Context(), testID)
		if err != nil {
			sendEvent(w, flusher, "end", map[string]string{"status": "UNKNOWN"})
			return
		}
		sendEvent(w, flusher, "end", map[string]string{"status": run.Status})
		return
	}

	events, unsubscribe := sess.subscribe()
	defer unsubscribe()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-events:
			if !open {
				return
			}
			sendEvent(w, flusher, ev.Type, ev.Data)
			if ev.Type == "end" {
				return
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
