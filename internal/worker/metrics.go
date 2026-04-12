package worker

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/vermakmanish001/go_sentinel/internal/tracer"
	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

var (
	promRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gosentinel_requests_total",
		Help: "Total number of HTTP requests made by workers",
	}, []string{"status"})

	promRequestDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "gosentinel_request_duration_seconds",
		Help:    "HTTP request latency in seconds",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	})

	promActiveVUs = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gosentinel_active_vus",
		Help: "Number of currently active virtual users",
	})
)

// MetricsCollector collects metrics from virtual users
type MetricsCollector struct {
	aggregator *tracer.SpanAggregator
	mu         sync.RWMutex

	// RPS tracking
	requestCounts []requestCount
	windowSize    time.Duration
	peakRPS       float64

	// Error tracking
	totalRequests int64
	totalErrors   int64
	statusCodes   map[int]int64
	errorMessages []string

	// Timestamps
	startTime time.Time
	lastReset time.Time
}

type requestCount struct {
	timestamp time.Time
	count     int64
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(windowSize time.Duration) *MetricsCollector {
	return &MetricsCollector{
		aggregator:    tracer.NewSpanAggregator(nil), // TODO: Pass logger
		requestCounts: make([]requestCount, 0),
		windowSize:    windowSize,
		statusCodes:   make(map[int]int64),
		errorMessages: make([]string, 0),
		startTime:     time.Now(),
		lastReset:     time.Now(),
	}
}

// RecordRequest records a request metric
func (mc *MetricsCollector) RecordRequest(success bool, duration time.Duration, statusCode int, message string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.totalRequests++
	if duration > 0 {
		mc.aggregator.Record(duration)
		promRequestDuration.Observe(duration.Seconds())
	}

	if !success {
		mc.totalErrors++
		if message != "" && len(mc.errorMessages) < 100 {
			mc.errorMessages = append(mc.errorMessages, message)
		}
		promRequestsTotal.WithLabelValues("error").Inc()
	} else {
		promRequestsTotal.WithLabelValues("success").Inc()
	}

	mc.statusCodes[statusCode]++

	// Record for RPS calculation
	now := time.Now()
	mc.requestCounts = append(mc.requestCounts, requestCount{
		timestamp: now,
		count:     1,
	})

	// Clean up old counts
	cutoff := now.Add(-mc.windowSize)
	validCounts := mc.requestCounts[:0]
	for _, rc := range mc.requestCounts {
		if rc.timestamp.After(cutoff) {
			validCounts = append(validCounts, rc)
		}
	}
	mc.requestCounts = validCounts
}

// RecordError records an error
func (mc *MetricsCollector) RecordError(err error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.totalErrors++
	if err != nil && len(mc.errorMessages) < 100 {
		mc.errorMessages = append(mc.errorMessages, err.Error())
	}
}

// GetSnapshot returns a metrics snapshot
func (mc *MetricsCollector) GetSnapshot() models.MetricSnapshot {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Calculate RPS
	var totalCount int64
	now := time.Now()
	cutoff := now.Add(-mc.windowSize)

	for _, rc := range mc.requestCounts {
		if rc.timestamp.After(cutoff) {
			totalCount += rc.count
		}
	}

	windowSeconds := int64(mc.windowSize / time.Second)
	if windowSeconds == 0 {
		windowSeconds = 1
	}
	currentRPS := float64(totalCount) / float64(windowSeconds)

	// Update peak RPS
	if currentRPS > mc.peakRPS {
		mc.peakRPS = currentRPS
	}

	// Calculate average RPS since start
	elapsed := time.Since(mc.startTime)
	avgRPS := float64(0)
	if elapsed.Seconds() > 0 {
		avgRPS = float64(mc.totalRequests) / elapsed.Seconds()
	}

	// Get latency percentiles
	min, max, mean, p50, p95, p99, p999, count := mc.aggregator.GetPercentiles()

	// Calculate error rate
	errorRate := float64(0)
	errorPercentage := float64(0)
	if mc.totalRequests > 0 {
		if elapsed.Seconds() > 0 {
			errorRate = float64(mc.totalErrors) / elapsed.Seconds()
		}
		errorPercentage = (float64(mc.totalErrors) / float64(mc.totalRequests)) * 100
	}

	// Copy status codes
	statusCodeCounts := make(map[int]int64)
	for k, v := range mc.statusCodes {
		statusCodeCounts[k] = v
	}

	// Copy error messages
	errMsgs := make([]string, len(mc.errorMessages))
	copy(errMsgs, mc.errorMessages)

	return models.MetricSnapshot{
		RPS: models.RPSSnapshot{
			Current:       currentRPS,
			Average:       avgRPS,
			Peak:          mc.peakRPS,
			WindowSeconds: windowSeconds,
		},
		Latency: models.LatencyHistogram{
			Min:   min,
			Max:   max,
			Mean:  mean,
			P50:   p50,
			P95:   p95,
			P99:   p99,
			P999:  p999,
			Count: count,
		},
		ErrorRate: models.ErrorRate{
			Rate:             errorRate,
			Percentage:       errorPercentage,
			StatusCodeCounts: statusCodeCounts,
			ErrorMessages:    errMsgs,
		},
		TotalRequests: mc.totalRequests,
		TotalErrors:   mc.totalErrors,
		Timestamp:     now,
	}
}

// Reset resets the metrics collector
func (mc *MetricsCollector) Reset() {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.aggregator.Reset()
	mc.requestCounts = mc.requestCounts[:0]
	mc.totalRequests = 0
	mc.totalErrors = 0
	mc.statusCodes = make(map[int]int64)
	mc.errorMessages = mc.errorMessages[:0]
	mc.peakRPS = 0
	mc.startTime = time.Now()
	mc.lastReset = time.Now()
}
