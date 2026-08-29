package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/vermakmanish001/go_sentinel/internal/runtime"
	"github.com/vermakmanish001/go_sentinel/internal/store"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
)

// Run states beyond those the orchestrator reports.
const (
	StatusQueued    = "QUEUED"
	StatusRunning   = "RUNNING"
	StatusCancelled = "CANCELLED"
	StatusFailed    = "FAILED"
)

// Queue executes runs one at a time.
//
// Load tests are serialised rather than parallelised on purpose: a worker can
// only execute one test, and concurrent runs on shared workers contend for the
// same goroutine and connection pools, distorting the very latency numbers the
// tool exists to measure. Queueing keeps submissions non-blocking without
// sacrificing that.
type Queue struct {
	store    store.Store
	orch     pborchestrator.OrchestratorServiceClient
	recorder *Recorder
	parser   *runtime.Parser
	logger   *zap.Logger

	wake chan struct{}

	mu       sync.Mutex
	current  string
	attempts map[string]int
}

// maxDispatchAttempts bounds retries for a run that cannot be dispatched. A
// queued run is commonly submitted while the fleet is still registering, so a
// transient "no worker nodes available" must not kill it; a genuinely
// undispatchable run still fails, just after a grace period.
const maxDispatchAttempts = 5

func NewQueue(
	st store.Store,
	orch pborchestrator.OrchestratorServiceClient,
	rec *Recorder,
	parser *runtime.Parser,
	logger *zap.Logger,
) *Queue {
	return &Queue{
		store:    st,
		orch:     orch,
		recorder: rec,
		parser:   parser,
		logger:   logger,
		wake:     make(chan struct{}, 1),
		attempts: make(map[string]int),
	}
}

// Current returns the run being executed, or "" when idle.
func (q *Queue) Current() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.current
}

// Notify nudges the dispatcher. Non-blocking: a pending nudge is enough.
func (q *Queue) Notify() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Start begins draining the queue. A run left RUNNING by a previous process is
// marked failed first: its metrics stream is gone and it cannot be resumed.
func (q *Queue) Start(ctx context.Context) {
	if n, err := q.store.FailInterruptedRuns(ctx); err != nil {
		q.logger.Warn("could not reconcile interrupted runs", zap.Error(err))
	} else if n > 0 {
		q.logger.Warn("marked interrupted runs as failed", zap.Int("count", n))
	}

	go q.loop(ctx)
	q.Notify()
}

func (q *Queue) loop(ctx context.Context) {
	// The ticker is a safety net: Notify covers every normal transition, but a
	// missed signal should delay the queue, not stall it.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
		case <-ticker.C:
		}

		for q.dispatchNext(ctx) {
			// Keep going while runs start successfully; a run that fails to
			// dispatch is skipped and the next is tried immediately.
		}
	}
}

// dispatchNext starts the oldest queued run. It reports whether it should be
// called again straight away.
func (q *Queue) dispatchNext(ctx context.Context) bool {
	q.mu.Lock()
	if q.current != "" {
		q.mu.Unlock()
		return false
	}
	q.mu.Unlock()

	run, err := q.store.NextQueued(ctx)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			q.logger.Warn("failed to read queue", zap.Error(err))
		}
		return false
	}

	// Claim the slot before any slow work so a concurrent tick cannot double-start.
	q.mu.Lock()
	if q.current != "" {
		q.mu.Unlock()
		return false
	}
	q.current = run.ID
	q.mu.Unlock()

	if err := q.dispatch(ctx, run); err != nil {
		q.mu.Lock()
		q.attempts[run.ID]++
		n := q.attempts[run.ID]
		q.mu.Unlock()

		q.release(run.ID)

		if n < maxDispatchAttempts {
			// Leave it QUEUED; the next tick retries it.
			q.logger.Warn("dispatch failed, will retry",
				zap.String("test_id", run.ID),
				zap.Int("attempt", n),
				zap.Int("of", maxDispatchAttempts),
				zap.Error(err))
			return false
		}

		q.logger.Error("giving up on queued run",
			zap.String("test_id", run.ID),
			zap.Int("attempts", n),
			zap.Error(err))
		_ = q.store.SetRunStatus(ctx, run.ID, StatusFailed)

		q.mu.Lock()
		delete(q.attempts, run.ID)
		q.mu.Unlock()
		return true // move on to the next run
	}

	q.mu.Lock()
	delete(q.attempts, run.ID)
	q.mu.Unlock()
	return false
}

func (q *Queue) dispatch(ctx context.Context, run store.Run) error {
	var spec runtime.YAMLTestPlan
	if err := json.Unmarshal([]byte(run.PlanSpec), &spec); err != nil {
		return err
	}
	scenario, err := q.parser.FromSpec(spec)
	if err != nil {
		return err
	}

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	resp, err := q.orch.DistributeTestPlan(dialCtx, &pborchestrator.DistributeRequest{
		Plan: runtime.ScenarioToProto(scenario, run.ID),
	})
	if err != nil {
		return err
	}

	if err := q.store.MarkDispatched(ctx, run.ID, int(resp.WorkersAssigned), time.Now().UnixMilli()); err != nil {
		q.logger.Warn("run dispatched but not recorded as running",
			zap.String("test_id", run.ID), zap.Error(err))
	}

	q.logger.Info("run dispatched",
		zap.String("test_id", run.ID),
		zap.String("name", run.Name),
		zap.Int32("workers", resp.WorkersAssigned),
	)

	q.recorder.Start(run.ID, func(string) {
		q.release(run.ID)
		q.Notify()
	})
	return nil
}

func (q *Queue) release(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.current == id {
		q.current = ""
	}
}

// Cancel removes a run that has not started yet. Running tests are stopped
// through the orchestrator instead, so this deliberately refuses them.
func (q *Queue) Cancel(ctx context.Context, id string) error {
	run, err := q.store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if run.Status != StatusQueued {
		return errors.New("run is not queued")
	}
	return q.store.SetRunStatus(ctx, id, StatusCancelled)
}
