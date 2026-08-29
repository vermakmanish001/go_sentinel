// Package api exposes the orchestrator's gRPC surface as JSON + Server-Sent
// Events so a browser can drive load tests. It holds no state of its own beyond
// which run is currently in flight; everything else lives in the orchestrator.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/vermakmanish001/go_sentinel/internal/runtime"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
)

// Server serves the HTTP API and, in production, the embedded frontend.
type Server struct {
	orch   pborchestrator.OrchestratorServiceClient
	parser *runtime.Parser
	logger *zap.Logger
	ui     fs.FS

	// A worker can only execute one test at a time, and concurrent runs would
	// contend for the same goroutine and connection pools — skewing the very
	// latency numbers the tool exists to measure. Runs are therefore serialised.
	mu          sync.Mutex
	activeRunID string
}

// uiBuilt reports whether the embedded frontend actually contains a build, as
// opposed to the placeholder that keeps `go build` working without Node.
func uiBuilt(ui fs.FS) bool {
	if ui == nil {
		return false
	}
	_, err := fs.Stat(ui, "index.html")
	return err == nil
}

// New creates an API server. ui may be nil, in which case only /api is served.
func New(orch pborchestrator.OrchestratorServiceClient, parser *runtime.Parser, ui fs.FS, logger *zap.Logger) *Server {
	return &Server{orch: orch, parser: parser, ui: ui, logger: logger}
}

// Routes builds the HTTP handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/workers", s.handleWorkers)
	mux.HandleFunc("POST /api/runs", s.handleStartRun)
	mux.HandleFunc("GET /api/runs/{id}", s.handleRunStatus)
	mux.HandleFunc("POST /api/runs/{id}/stop", s.handleStopRun)
	mux.HandleFunc("GET /api/runs/{id}/stream", s.handleStreamRun)

	if uiBuilt(s.ui) {
		mux.Handle("/", s.spaHandler())
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>GoSentinel</title>`+
				`<body style="font:14px system-ui;padding:40px;max-width:40em">`+
				`<h1>Dashboard not built</h1>`+
				`<p>The API is running. Build the frontend with <code>make ui</code>, `+
				`or run <code>npm run dev</code> in <code>web/</code> for hot reload.</p>`)
		})
	}
	return mux
}

// ---------- plans ----------

// StartRunRequest is the plan the browser submits. It reuses the same spec the
// YAML parser consumes, so a plan built in the UI and one written by hand are
// validated identically.
type StartRunRequest struct {
	runtime.YAMLTestPlan
}

type StartRunResponse struct {
	TestID          string           `json:"test_id"`
	WorkersAssigned int32            `json:"workers_assigned"`
	Distribution    map[string]int32 `json:"distribution"`
}

func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	var req StartRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	scenario, err := s.parser.FromSpec(req.YAMLTestPlan)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	s.mu.Lock()
	if s.activeRunID != "" {
		active := s.activeRunID
		s.mu.Unlock()
		writeError(w, http.StatusConflict, fmt.Sprintf("run %s is still in flight; only one run executes at a time", active))
		return
	}
	testID := fmt.Sprintf("run-%d", time.Now().UnixMilli())
	s.activeRunID = testID
	s.mu.Unlock()

	plan := runtime.ScenarioToProto(scenario, testID)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	resp, err := s.orch.DistributeTestPlan(ctx, &pborchestrator.DistributeRequest{Plan: plan})
	if err != nil {
		s.clearActiveRun(testID)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("orchestrator rejected the plan: %v", err))
		return
	}

	go s.watchRun(testID)

	s.logger.Info("run started",
		zap.String("test_id", testID),
		zap.String("name", scenario.Name),
		zap.Int32("workers", resp.WorkersAssigned),
	)

	writeJSON(w, http.StatusAccepted, StartRunResponse{
		TestID:          resp.TestId,
		WorkersAssigned: resp.WorkersAssigned,
		Distribution:    resp.WorkerVuDistribution,
	})
}

// watchRun releases the single-run slot once the orchestrator reports a
// terminal status, so the next run can start without the browser being open.
func (s *Server) watchRun(testID string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// Backstop: never hold the slot forever if the orchestrator goes silent.
	deadline := time.After(24 * time.Hour)

	for {
		select {
		case <-deadline:
			s.logger.Warn("run watcher timed out, releasing slot", zap.String("test_id", testID))
			s.clearActiveRun(testID)
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			resp, err := s.orch.GetTestStatus(ctx, &pborchestrator.TestStatusRequest{TestId: testID})
			cancel()
			if err != nil {
				continue
			}
			if isTerminal(resp.Status) {
				s.logger.Info("run finished",
					zap.String("test_id", testID),
					zap.String("status", resp.Status.String()),
				)
				s.clearActiveRun(testID)
				return
			}
		}
	}
}

func (s *Server) clearActiveRun(testID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeRunID == testID {
		s.activeRunID = ""
	}
}

func isTerminal(st pborchestrator.TestStatusResponse_Status) bool {
	switch st {
	case pborchestrator.TestStatusResponse_COMPLETED,
		pborchestrator.TestStatusResponse_FAILED,
		pborchestrator.TestStatusResponse_STOPPED:
		return true
	}
	return false
}

// ---------- run status / stop ----------

type RunStatus struct {
	TestID        string   `json:"test_id"`
	Status        string   `json:"status"`
	ActiveWorkers int32    `json:"active_workers"`
	TotalVUs      int32    `json:"total_vus"`
	Metrics       *Metrics `json:"metrics,omitempty"`
}

func (s *Server) handleRunStatus(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.orch.GetTestStatus(ctx, &pborchestrator.TestStatusRequest{TestId: testID})
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("run not found: %s", testID))
		return
	}

	writeJSON(w, http.StatusOK, RunStatus{
		TestID:        testID,
		Status:        resp.Status.String(),
		ActiveWorkers: resp.ActiveWorkers,
		TotalVUs:      resp.TotalVus,
		Metrics:       metricsFromProto(resp.CurrentMetrics),
	})
}

func (s *Server) handleStopRun(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	resp, err := s.orch.StopTest(ctx, &pborchestrator.StopTestRequest{TestId: testID})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !resp.Success {
		writeError(w, http.StatusConflict, resp.Message)
		return
	}

	s.clearActiveRun(testID)
	writeJSON(w, http.StatusOK, map[string]string{"message": resp.Message})
}

// ---------- workers / health ----------

type Worker struct {
	ID         string `json:"id"`
	Address    string `json:"address"`
	MaxVUs     int32  `json:"max_vus"`
	Status     string `json:"status"`
	LastSeenMs int64  `json:"last_seen_ms"`
}

func (s *Server) handleWorkers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.orch.ListWorkers(ctx, &pborchestrator.ListWorkersRequest{})
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("orchestrator unreachable: %v", err))
		return
	}

	workers := make([]Worker, 0, len(resp.Workers))
	var capacity int32
	for _, wk := range resp.Workers {
		capacity += wk.MaxVus
		workers = append(workers, Worker{
			ID:         wk.WorkerId,
			Address:    wk.Address,
			MaxVUs:     wk.MaxVus,
			Status:     wk.Status,
			LastSeenMs: wk.LastSeenMs,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"workers":  workers,
		"capacity": capacity,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	orchestrator := "ok"
	if _, err := s.orch.ListWorkers(ctx, &pborchestrator.ListWorkersRequest{}); err != nil {
		orchestrator = "unreachable"
	}

	s.mu.Lock()
	active := s.activeRunID
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"orchestrator":  orchestrator,
		"active_run_id": active,
	})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// spaHandler serves the embedded frontend, falling back to index.html so
// client-side routes survive a page reload.
func (s *Server) spaHandler() http.Handler {
	files := http.FileServer(http.FS(s.ui))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			files.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(s.ui, path[1:]); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}
