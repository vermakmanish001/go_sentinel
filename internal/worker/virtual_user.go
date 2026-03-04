package worker

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// VirtualUser represents a single virtual user
type VirtualUser struct {
	id        int
	scenario  *models.HTTPConfig
	client    *HTTPClient
	logger    *zap.Logger
	metrics   *MetricsCollector
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewVirtualUser creates a new virtual user
func NewVirtualUser(id int, scenario *models.HTTPConfig, client *HTTPClient, logger *zap.Logger, metrics *MetricsCollector) *VirtualUser {
	ctx, cancel := context.WithCancel(context.Background())

	return &VirtualUser{
		id:       id,
		scenario: scenario,
		client:   client,
		logger:   logger.With(zap.Int("vu_id", id)),
		metrics:  metrics,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Run runs the virtual user lifecycle
func (vu *VirtualUser) Run(ctx context.Context, duration time.Duration, rampUp time.Duration) error {
	// Ramp up logic
	if rampUp > 0 {
		vu.logger.Debug("ramping up", zap.Duration("ramp_up", rampUp))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rampUp):
			// Ramp up complete
		}
	}

	// Main execution loop
	deadline := time.Now().Add(duration)
	ticker := time.NewTicker(100 * time.Millisecond) // Check every 100ms
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-vu.ctx.Done():
			return vu.ctx.Err()
		default:
			if time.Now().After(deadline) {
				vu.logger.Debug("virtual user completed")
				return nil
			}

			// Execute requests
			for _, req := range vu.scenario.Requests {
				if err := vu.executeRequest(ctx, req); err != nil {
					vu.metrics.RecordError(err)
					vu.logger.Debug("request failed", zap.Error(err))
				}

				// Think time
				if req.ThinkTime > 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(req.ThinkTime):
					}
				}
			}

			// Small delay to prevent tight loop
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
}

// executeRequest executes a single HTTP request
func (vu *VirtualUser) executeRequest(ctx context.Context, req models.Request) error {
	url := vu.scenario.BaseURL + req.Path

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	for k, v := range vu.scenario.Headers {
		httpReq.Header.Set(k, v)
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	// Set body if present
	if len(req.Body) > 0 {
		httpReq.Body = http.MaxBytesReader(nil, http.MaxBytesReader(nil, nil, int64(len(req.Body))), int64(len(req.Body)))
		// TODO: Set body properly
	}

	start := time.Now()
	resp, err := vu.client.Do(httpReq)
	duration := time.Since(start)

	if err != nil {
		vu.metrics.RecordRequest(false, duration, 0)
		return err
	}

	statusCode := resp.StatusCode
	success := statusCode >= 200 && statusCode < 300

	// Check assertions
	for _, assertion := range req.Assertions {
		if !vu.checkAssertion(assertion, resp, duration) {
			success = false
		}
	}

	vu.metrics.RecordRequest(success, duration, statusCode)

	return nil
}

// checkAssertion checks if an assertion passes
func (vu *VirtualUser) checkAssertion(assertion models.Assertion, resp *http.Response, duration time.Duration) bool {
	switch assertion.Type {
	case models.AssertionTypeStatusCode:
		expected, ok := assertion.Value.(int)
		if !ok {
			return false
		}
		return resp.StatusCode == expected

	case models.AssertionTypeResponseTime:
		percentile, ok := assertion.Value.(string)
		if !ok {
			return false
		}
		threshold, ok := assertion.Threshold.(time.Duration)
		if !ok {
			return false
		}
		// TODO: Check against actual percentile from metrics
		return duration <= threshold

	case models.AssertionTypeBodyContains:
		// TODO: Read body and check for substring
		return true

	default:
		return true
	}
}

// Stop stops the virtual user
func (vu *VirtualUser) Stop() {
	vu.cancel()
}
