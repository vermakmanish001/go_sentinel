package api

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/vermakmanish001/go_sentinel/internal/store"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
)

// Event is one Server-Sent Event delivered to a dashboard.
type Event struct {
	Type string // "metrics" | "status" | "end"
	Data any
}

// session is a run being recorded. One gRPC metrics stream feeds both the
// database and every connected dashboard, so what viewers see and what history
// records cannot diverge.
type session struct {
	testID string

	mu      sync.Mutex
	subs    map[chan Event]struct{}
	closed  bool
	last    *Metrics
	peakRPS float64
	status  string
}

func (s *session) subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		close(ch)
		return ch, func() {}
	}
	s.subs[ch] = struct{}{}

	// Bring a late subscriber up to date immediately rather than making it wait
	// a second for the next tick.
	if s.last != nil {
		ch <- Event{Type: "metrics", Data: s.last}
	}

	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
	}
}

func (s *session) publish(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	for ch := range s.subs {
		select {
		case ch <- ev:
		default: // a stalled dashboard must not block recording
		}
	}
}

func (s *session) close(final Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for ch := range s.subs {
		select {
		case ch <- final:
		default:
		}
		close(ch)
	}
	s.subs = nil
}

// Recorder drives active runs: it streams metrics from the orchestrator,
// persists them, and broadcasts them to dashboards.
type Recorder struct {
	orch   pborchestrator.OrchestratorServiceClient
	store  store.Store
	logger *zap.Logger

	mu       sync.Mutex
	sessions map[string]*session
}

func NewRecorder(orch pborchestrator.OrchestratorServiceClient, st store.Store, logger *zap.Logger) *Recorder {
	return &Recorder{
		orch:     orch,
		store:    st,
		logger:   logger,
		sessions: make(map[string]*session),
	}
}

// Session returns the live session for a run, if it is still being recorded.
func (r *Recorder) Session(testID string) (*session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[testID]
	return s, ok
}

// Start begins recording a run. onDone fires once when the run reaches a
// terminal state, whether or not any dashboard is connected.
func (r *Recorder) Start(testID string, onDone func(status string)) {
	s := &session{testID: testID, subs: make(map[chan Event]struct{}), status: "RUNNING"}

	r.mu.Lock()
	r.sessions[testID] = s
	r.mu.Unlock()

	go r.run(s, onDone)
}

func (r *Recorder) run(s *session, onDone func(status string)) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer func() {
		r.mu.Lock()
		delete(r.sessions, s.testID)
		r.mu.Unlock()
	}()

	stream, err := r.orch.StreamMetrics(ctx, &pborchestrator.StreamMetricsRequest{TestId: s.testID})
	if err != nil {
		r.logger.Error("metrics stream failed", zap.String("test_id", s.testID), zap.Error(err))
		r.finish(s, "FAILED", onDone)
		return
	}

	metricsCh := make(chan *Metrics)
	go func() {
		defer close(metricsCh)
		for {
			resp, err := stream.Recv()
			if err != nil {
				return
			}
			m := metricsFromProto(resp.Snapshot)
			if m == nil {
				continue
			}
			select {
			case metricsCh <- m:
			case <-ctx.Done():
				return
			}
		}
	}()

	statusTicker := time.NewTicker(time.Second)
	defer statusTicker.Stop()

	// Backstop so a silent orchestrator cannot pin a run open forever.
	deadline := time.After(24 * time.Hour)

	for {
		select {
		case <-deadline:
			r.logger.Warn("run recorder timed out", zap.String("test_id", s.testID))
			r.finish(s, "FAILED", onDone)
			return

		case m, open := <-metricsCh:
			if !open {
				r.finish(s, s.currentStatus(), onDone)
				return
			}
			s.mu.Lock()
			s.last = m
			if m.RPS.Current > s.peakRPS {
				s.peakRPS = m.RPS.Current
			}
			s.mu.Unlock()

			s.publish(Event{Type: "metrics", Data: m})
			r.persistSample(s.testID, m)

		case <-statusTicker.C:
			resp, err := r.orch.GetTestStatus(ctx, &pborchestrator.TestStatusRequest{TestId: s.testID})
			if err != nil {
				continue
			}
			st := resp.Status.String()

			s.mu.Lock()
			s.status = st
			s.mu.Unlock()

			s.publish(Event{Type: "status", Data: RunStatus{
				TestID:        s.testID,
				Status:        st,
				ActiveWorkers: resp.ActiveWorkers,
				TotalVUs:      resp.TotalVus,
			}})

			if isTerminal(resp.Status) {
				// One final read so stored totals match the run's true end.
				if final := metricsFromProto(resp.CurrentMetrics); final != nil {
					s.mu.Lock()
					s.last = final
					s.mu.Unlock()
					s.publish(Event{Type: "metrics", Data: final})
					r.persistSample(s.testID, final)
				}
				r.finish(s, st, onDone)
				return
			}
		}
	}
}

func (s *session) currentStatus() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (r *Recorder) persistSample(testID string, m *Metrics) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := r.store.AddSample(ctx, testID, store.Sample{
		TsMs:     time.Now().UnixMilli(),
		RPS:      m.RPS.Current,
		ErrRate:  m.Errors.Rate,
		ErrPct:   m.Errors.Percentage,
		P50Ms:    m.Latency.P50Ms,
		P95Ms:    m.Latency.P95Ms,
		P99Ms:    m.Latency.P99Ms,
		Requests: m.TotalRequests,
		Errors:   m.TotalErrors,
	})
	if err != nil {
		r.logger.Warn("failed to persist sample", zap.String("test_id", testID), zap.Error(err))
	}
}

// finish writes the run's summary row and closes the session.
func (r *Recorder) finish(s *session, status string, onDone func(status string)) {
	s.mu.Lock()
	last, peak := s.last, s.peakRPS
	s.mu.Unlock()

	summary := store.Run{}
	if last != nil {
		summary = store.Run{
			TotalRequests: last.TotalRequests,
			TotalErrors:   last.TotalErrors,
			ErrorPct:      last.Errors.Percentage,
			PeakRPS:       peak,
			AvgRPS:        last.RPS.Average,
			P95Ms:         last.Latency.P95Ms,
			P99Ms:         last.Latency.P99Ms,
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.store.FinishRun(ctx, s.testID, status, time.Now().UnixMilli(), summary); err != nil {
		r.logger.Warn("failed to finalise run", zap.String("test_id", s.testID), zap.Error(err))
	}

	r.logger.Info("run recorded",
		zap.String("test_id", s.testID),
		zap.String("status", status),
		zap.Int64("requests", summary.TotalRequests),
	)

	s.close(Event{Type: "end", Data: map[string]string{"status": status}})

	if onDone != nil {
		onDone(status)
	}
}
