package worker

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Task represents a unit of work
type Task func(ctx context.Context) error

// Pool is a custom goroutine pool with backpressure
type Pool struct {
	size        int
	queue       chan Task
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *zap.Logger
	activeCount int32
	mu          sync.RWMutex
}

// NewPool creates a new goroutine pool
func NewPool(size int, queueSize int, logger *zap.Logger) *Pool {
	ctx, cancel := context.WithCancel(context.Background())

	return &Pool{
		size:   size,
		queue:  make(chan Task, queueSize),
		ctx:    ctx,
		cancel: cancel,
		logger: logger,
	}
}

// Start starts the worker goroutines
func (p *Pool) Start() {
	for i := 0; i < p.size; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
}

// worker is a single worker goroutine
func (p *Pool) worker(id int) {
	defer p.wg.Done()

	for {
		select {
		case <-p.ctx.Done():
			p.logger.Debug("worker stopping", zap.Int("worker_id", id))
			return
		case task, ok := <-p.queue:
			if !ok {
				return
			}

			p.mu.Lock()
			p.activeCount++
			p.mu.Unlock()

			if err := task(p.ctx); err != nil {
				p.logger.Warn("task failed",
					zap.Int("worker_id", id),
					zap.Error(err),
				)
			}

			p.mu.Lock()
			p.activeCount--
			p.mu.Unlock()
		}
	}
}

// Submit submits a task to the pool
func (p *Pool) Submit(task Task) error {
	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case p.queue <- task:
		return nil
	case <-time.After(5 * time.Second):
		return ErrPoolFull
	}
}

// SubmitWithTimeout submits a task with a timeout
func (p *Pool) SubmitWithTimeout(task Task, timeout time.Duration) error {
	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case p.queue <- task:
		return nil
	case <-time.After(timeout):
		return ErrPoolFull
	}
}

// Shutdown gracefully shuts down the pool
func (p *Pool) Shutdown(timeout time.Duration) error {
	p.cancel()
	close(p.queue)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info("pool shut down gracefully")
		return nil
	case <-time.After(timeout):
		p.logger.Warn("pool shutdown timeout")
		return ErrShutdownTimeout
	}
}

// GetActiveCount returns the number of active tasks
func (p *Pool) GetActiveCount() int32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.activeCount
}

// GetQueueSize returns the current queue size
func (p *Pool) GetQueueSize() int {
	return len(p.queue)
}

var (
	ErrPoolFull        = &PoolError{Message: "pool queue is full"}
	ErrShutdownTimeout = &PoolError{Message: "pool shutdown timeout"}
)

// PoolError represents a pool error
type PoolError struct {
	Message string
}

func (e *PoolError) Error() string {
	return e.Message
}
