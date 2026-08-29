package worker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// Engine is the core load testing engine
type Engine struct {
	logger             *zap.Logger
	pool               *Pool
	httpClient         *HTTPClient
	metrics            *MetricsCollector
	reporter           *Reporter
	virtualUsers       []*VirtualUser
	mu                 sync.RWMutex
	currentTestID      string
	ctx                context.Context
	cancel             context.CancelFunc
	activeVUs          int32
	testActive         bool
	testCancel         context.CancelFunc
}

// NewEngine creates a new load testing engine
func NewEngine(workerID string, maxVUs int, orchestratorClient pborchestrator.OrchestratorServiceClient, logger *zap.Logger) *Engine {
	ctx, cancel := context.WithCancel(context.Background())

	pool := NewPool(maxVUs, maxVUs*2, logger)
	pool.Start()

	httpClient := NewHTTPClient(30*time.Second, logger)
	metrics := NewMetricsCollector(10 * time.Second)
	reporter := NewReporter(workerID, metrics, orchestratorClient, logger)

	return &Engine{
		logger:       logger,
		pool:         pool,
		httpClient:   httpClient,
		metrics:      metrics,
		reporter:     reporter,
		virtualUsers: make([]*VirtualUser, 0),
		ctx:          ctx,
		cancel:       cancel,
	}
}

// RunTest runs a test plan
func (e *Engine) RunTest(ctx context.Context, testID string, plan *models.TestPlan, assignedVUs int32) error {
	// Per-test context: cancelling it aborts every remaining stage, not just the
	// virtual users of the stage currently in flight.
	testCtx, testCancel := context.WithCancel(ctx)
	defer testCancel()

	e.mu.Lock()
	e.currentTestID = testID
	e.testActive = true
	e.testCancel = testCancel
	e.mu.Unlock()

	e.logger.Info("starting test",
		zap.String("test_id", testID),
		zap.Int32("assigned_vus", assignedVUs),
	)

	// Reset metrics
	e.metrics.Reset()

	// Start reporter
	e.reporter.Start(testID)
	defer e.reporter.Stop()

	// Clear the active flag before the reporter's final batch goes out, so the
	// keep-alive in cmd/worker cannot re-assert this test as still running.
	defer func() {
		e.mu.Lock()
		e.testActive = false
		e.mu.Unlock()
	}()

	// Create virtual users
	vus := make([]*VirtualUser, assignedVUs)
	for i := int32(0); i < assignedVUs; i++ {
		vus[i] = NewVirtualUser(int(i), &plan.HTTP, e.httpClient, e.logger, e.metrics)
	}
	e.mu.Lock()
	e.virtualUsers = vus
	e.mu.Unlock()

	// Run stages sequentially
	for stageIdx, stage := range plan.Stages {
		e.logger.Info("starting stage",
			zap.Int("stage", stageIdx),
			zap.Int32("target_vus", stage.TargetVUs),
			zap.Duration("duration", stage.Duration),
		)

		if err := e.runStage(testCtx, stage, assignedVUs); err != nil {
			return fmt.Errorf("stage %d failed: %w", stageIdx, err)
		}
	}

	e.logger.Info("test completed", zap.String("test_id", testID))
	return nil
}

// runStage runs a single stage
func (e *Engine) runStage(ctx context.Context, stage models.Stage, totalVUs int32) error {
	// The orchestrator has already scaled stage.TargetVUs to this worker's share
	// of the stage, so it should never exceed the VUs allocated for the test.
	// Cap it anyway rather than index past the pre-built virtual user slice.
	activeVUs := stage.TargetVUs
	if activeVUs > totalVUs {
		activeVUs = totalVUs
	}

	e.mu.RLock()
	vus := e.virtualUsers
	e.mu.RUnlock()
	if int(activeVUs) > len(vus) {
		activeVUs = int32(len(vus))
	}

	// Start virtual users
	var wg sync.WaitGroup
	stageCtx, stageCancel := context.WithTimeout(ctx, stage.Duration)
	defer stageCancel()

	for i := int32(0); i < activeVUs; i++ {
		vu := vus[i]
		wg.Add(1)
		atomic.AddInt32(&e.activeVUs, 1)
		promActiveVUs.Inc()

		go func(vu *VirtualUser) {
			defer wg.Done()
			defer atomic.AddInt32(&e.activeVUs, -1)
			defer promActiveVUs.Dec()
			if err := vu.Run(stageCtx, stage.Duration, stage.RampUp); err != nil {
				e.logger.Warn("virtual user failed",
					zap.Int("vu_id", vu.id),
					zap.Error(err),
				)
			}
		}(vu)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Always hold the stage for its declared duration rather than returning as
	// soon as the VUs finish. A stage this worker was allocated no VUs for would
	// otherwise complete instantly and let the worker run ahead into the next
	// stage while the rest of the fleet is still on this one.
	<-stageCtx.Done()

	for _, vu := range vus[:activeVUs] {
		vu.Stop()
	}
	<-done

	// If the parent context was cancelled (e.g. StopTest), propagate the error.
	// If the stage simply ran for its full duration, that is success.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

// CurrentTest returns the test this engine last ran and whether it is still
// running. The keep-alive reporter uses it to stamp batches correctly between
// tests.
func (e *Engine) CurrentTest() (string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.currentTestID, e.testActive
}

// GetActiveVUs returns the number of currently active virtual users
func (e *Engine) GetActiveVUs() int32 {
	return atomic.LoadInt32(&e.activeVUs)
}

// StopTest aborts the test currently in flight. The engine itself stays usable
// for subsequent tests — only Shutdown tears it down.
func (e *Engine) StopTest() error {
	e.mu.RLock()
	testID := e.currentTestID
	cancel := e.testCancel
	vus := e.virtualUsers
	e.mu.RUnlock()

	e.logger.Info("stopping test", zap.String("test_id", testID))

	// Cancelling the per-test context unwinds the stage loop; stopping the
	// virtual users directly makes in-flight requests abort immediately rather
	// than waiting out the current stage tick.
	if cancel != nil {
		cancel()
	}
	for _, vu := range vus {
		vu.Stop()
	}

	return nil
}

// GetMetrics returns current metrics
func (e *Engine) GetMetrics() models.MetricSnapshot {
	return e.metrics.GetSnapshot()
}

// Shutdown shuts down the engine
func (e *Engine) Shutdown() error {
	e.logger.Info("shutting down engine")

	e.cancel()

	e.mu.RLock()
	vus := e.virtualUsers
	e.mu.RUnlock()

	// Stop all virtual users
	for _, vu := range vus {
		vu.Stop()
	}

	// Shutdown pool
	if err := e.pool.Shutdown(10 * time.Second); err != nil {
		e.logger.Warn("pool shutdown error", zap.Error(err))
	}

	// Close HTTP client
	e.httpClient.Close()

	return nil
}
