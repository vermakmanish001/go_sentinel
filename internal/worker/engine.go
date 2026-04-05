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
	e.mu.Lock()
	e.currentTestID = testID
	e.mu.Unlock()

	e.logger.Info("starting test",
		zap.String("test_id", testID),
		zap.Int32("assigned_vus", assignedVUs),
	)

	// Reset metrics
	e.metrics.Reset()

	// Start reporter
	e.reporter.Start()
	defer e.reporter.Stop()

	// Create virtual users
	e.virtualUsers = make([]*VirtualUser, assignedVUs)
	for i := int32(0); i < assignedVUs; i++ {
		vu := NewVirtualUser(int(i), &plan.HTTP, e.httpClient, e.logger, e.metrics)
		e.virtualUsers[i] = vu
	}

	// Run stages sequentially
	for stageIdx, stage := range plan.Stages {
		e.logger.Info("starting stage",
			zap.Int("stage", stageIdx),
			zap.Int32("target_vus", stage.TargetVUs),
			zap.Duration("duration", stage.Duration),
		)

		if err := e.runStage(ctx, stage, assignedVUs); err != nil {
			return fmt.Errorf("stage %d failed: %w", stageIdx, err)
		}
	}

	e.logger.Info("test completed", zap.String("test_id", testID))
	return nil
}

// runStage runs a single stage
func (e *Engine) runStage(ctx context.Context, stage models.Stage, totalVUs int32) error {
	// Calculate VUs for this stage (scaled from total)
	activeVUs := stage.TargetVUs
	if activeVUs > totalVUs {
		activeVUs = totalVUs
	}

	// Start virtual users
	var wg sync.WaitGroup
	stageCtx, stageCancel := context.WithTimeout(ctx, stage.Duration)
	defer stageCancel()

	for i := int32(0); i < activeVUs; i++ {
		if i >= int32(len(e.virtualUsers)) {
			break
		}

		vu := e.virtualUsers[i]
		wg.Add(1)
		atomic.AddInt32(&e.activeVUs, 1)

		go func(vu *VirtualUser) {
			defer wg.Done()
			defer atomic.AddInt32(&e.activeVUs, -1)
			if err := vu.Run(stageCtx, stage.Duration, stage.RampUp); err != nil {
				e.logger.Warn("virtual user failed",
					zap.Int("vu_id", vu.id),
					zap.Error(err),
				)
			}
		}(vu)
	}

	// Wait for stage to complete or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-stageCtx.Done():
		for _, vu := range e.virtualUsers[:activeVUs] {
			vu.Stop()
		}
		<-done
		// If the parent context was cancelled (e.g. StopTest), propagate the error.
		// If the stage simply ran for its full duration, that is success.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	case <-done:
		return nil
	}
}

// GetActiveVUs returns the number of currently active virtual users
func (e *Engine) GetActiveVUs() int32 {
	return atomic.LoadInt32(&e.activeVUs)
}

// StopTest stops the current test
func (e *Engine) StopTest() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.logger.Info("stopping test", zap.String("test_id", e.currentTestID))

	e.cancel()

	// Stop all virtual users
	for _, vu := range e.virtualUsers {
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

	// Stop all virtual users
	for _, vu := range e.virtualUsers {
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
