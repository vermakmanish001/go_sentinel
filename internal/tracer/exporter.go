package tracer

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
)

// InMemoryExporter is a custom span exporter that stores spans in memory
// for consumption by the TUI
type InMemoryExporter struct {
	logger    *zap.Logger
	spans     []SpanData
	mu        sync.RWMutex
	maxSpans  int
	callbacks []func([]SpanData)
}

// SpanData represents a span for TUI consumption
type SpanData struct {
	Name      string
	Duration  int64 // milliseconds
	Status    string
	Timestamp int64
}

// NewInMemoryExporter creates a new in-memory exporter
func NewInMemoryExporter(logger *zap.Logger, maxSpans int) *InMemoryExporter {
	return &InMemoryExporter{
		logger:   logger,
		spans:    make([]SpanData, 0, maxSpans),
		maxSpans: maxSpans,
	}
}

// ExportSpans exports spans to memory
func (e *InMemoryExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, span := range spans {
		spanData := SpanData{
			Name:      span.Name(),
			Duration:  int64(span.EndTime().Sub(span.StartTime()) / 1000000), // nanoseconds to milliseconds
			Status:    span.Status().Code.String(),
			Timestamp: span.StartTime().UnixMilli(),
		}

		e.spans = append(e.spans, spanData)

		// Keep only the most recent spans
		if len(e.spans) > e.maxSpans {
			e.spans = e.spans[1:]
		}
	}

	// Notify callbacks
	for _, cb := range e.callbacks {
		cb(e.spans)
	}

	return nil
}

// Shutdown shuts down the exporter
func (e *InMemoryExporter) Shutdown(ctx context.Context) error {
	return nil
}

// GetSpans returns a copy of the current spans
func (e *InMemoryExporter) GetSpans() []SpanData {
	e.mu.RLock()
	defer e.mu.RUnlock()

	spans := make([]SpanData, len(e.spans))
	copy(spans, e.spans)
	return spans
}

// RegisterCallback registers a callback that is called when new spans are exported
func (e *InMemoryExporter) RegisterCallback(cb func([]SpanData)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.callbacks = append(e.callbacks, cb)
}
