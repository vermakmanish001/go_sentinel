package models

import (
	"time"
)

// TestStatus represents the status of a load test
type TestStatus string

const (
	TestStatusPending   TestStatus = "pending"
	TestStatusRunning   TestStatus = "running"
	TestStatusCompleted TestStatus = "completed"
	TestStatusFailed    TestStatus = "failed"
	TestStatusStopped   TestStatus = "stopped"
)

// WorkerNode represents a registered worker node
type WorkerNode struct {
	ID       string
	Address  string
	MaxVUs   int32
	Status   WorkerStatus
	Metadata map[string]string
	LastSeen time.Time
}

// WorkerStatus represents the status of a worker
type WorkerStatus string

const (
	WorkerStatusIdle     WorkerStatus = "idle"
	WorkerStatusRunning  WorkerStatus = "running"
	WorkerStatusStopping WorkerStatus = "stopping"
	WorkerStatusError    WorkerStatus = "error"
)

// TestPlan represents a load test plan
type TestPlan struct {
	ID                string
	Name              string
	Stages            []Stage
	HTTP              HTTPConfig
	Variables         map[string]string
	TotalVirtualUsers int32
}

// Stage represents a load test stage
type Stage struct {
	Duration  time.Duration
	TargetVUs int32
	RampUp    time.Duration
}

// HTTPConfig represents HTTP test configuration
type HTTPConfig struct {
	BaseURL  string
	Requests []Request
	Headers  map[string]string
	Timeout  time.Duration
}

// Request represents an HTTP request
type Request struct {
	Method     string
	Path       string
	Headers    map[string]string
	Body       []byte
	Assertions []Assertion
	ThinkTime  time.Duration
}

// Assertion represents a response assertion
type Assertion struct {
	Type      AssertionType
	Value     interface{}
	Threshold interface{}
}

// AssertionType represents the type of assertion
type AssertionType string

const (
	AssertionTypeStatusCode   AssertionType = "status_code"
	AssertionTypeResponseTime AssertionType = "response_time"
	AssertionTypeBodyContains AssertionType = "body_contains"
)

// MetricSnapshot represents aggregated metrics
type MetricSnapshot struct {
	RPS           RPSSnapshot
	Latency       LatencyHistogram
	ErrorRate     ErrorRate
	TotalRequests int64
	TotalErrors   int64
	Timestamp     time.Time
	WorkerID      string
}

// RPSSnapshot represents RPS metrics
type RPSSnapshot struct {
	Current       float64
	Average       float64
	Peak          float64
	WindowSeconds int64
}

// LatencyHistogram represents latency distribution
type LatencyHistogram struct {
	Min   time.Duration
	Max   time.Duration
	Mean  time.Duration
	P50   time.Duration
	P95   time.Duration
	P99   time.Duration
	P999  time.Duration
	Count int64
}

// ErrorRate represents error metrics
type ErrorRate struct {
	Rate             float64
	Percentage       float64
	StatusCodeCounts map[int]int64
	ErrorMessages    []string
}
