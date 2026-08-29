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
	"github.com/vermakmanish001/go_sentinel/internal/store"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
)

// Server serves the HTTP API and, in production, the embedded frontend.
type Server struct {
	orch     pborchestrator.OrchestratorServiceClient
	parser   *runtime.Parser
	logger   *zap.Logger
	ui       fs.FS
	store    store.Store
	recorder *Recorder

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
func New(
	orch pborchestrator.OrchestratorServiceClient,
	parser *runtime.Parser,
	st store.Store,
	ui fs.FS,
	logger *zap.Logger,
) *Server {
	s := &Server{orch: orch, parser: parser, store: st, ui: ui, logger: logger}
	s.recorder = NewRecorder(orch, st, logger)
	return s
}

// Routes builds the HTTP handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/workers", s.handleWorkers)
	mux.HandleFunc("POST /api/runs", s.handleStartRun)
	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/runs/{id}", s.handleRunStatus)
	mux.HandleFunc("DELETE /api/runs/{id}", s.handleDeleteRun)
	mux.HandleFunc("GET /api/runs/{id}/series", s.handleRunSeries)
	mux.HandleFunc("POST /api/runs/{id}/stop", s.handleStopRun)
	mux.HandleFunc("GET /api/runs/{id}/stream", s.handleStreamRun)

	mux.HandleFunc("GET /api/plans", s.handleListPlans)
	mux.HandleFunc("POST /api/plans", s.handleCreatePlan)
	mux.HandleFunc("PUT /api/plans/{id}", s.handleUpdatePlan)
	mux.HandleFunc("DELETE /api/plans/{id}", s.handleDeletePlan)

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

	var peakVUs int
	for _, st := range scenario.Stages {
		if int(st.TargetVUs) > peakVUs {
			peakVUs = int(st.TargetVUs)
		}
	}
	specJSON, _ := json.Marshal(req.YAMLTestPlan)

	if err := s.store.CreateRun(r.Context(), store.Run{
		ID:        testID,
		Name:      scenario.Name,
		PlanSpec:  string(specJSON),
		Status:    "RUNNING",
		StartedAt: time.Now().UnixMilli(),
		Workers:   int(resp.WorkersAssigned),
		PeakVUs:   peakVUs,
	}); err != nil {
		s.logger.Error("failed to record run", zap.String("test_id", testID), zap.Error(err))
	}

	// The recorder persists samples and releases the run slot on completion,
	// so history is captured whether or not a dashboard is connected.
	s.recorder.Start(testID, func(string) { s.clearActiveRun(testID) })

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
	TestID        string     `json:"test_id"`
	Status        string     `json:"status"`
	ActiveWorkers int32      `json:"active_workers"`
	TotalVUs      int32      `json:"total_vus"`
	Metrics       *Metrics   `json:"metrics,omitempty"`
	Run           *store.Run `json:"run,omitempty"`
}

// handleRunStatus reports a run from history, merging in live metrics while it
// is still executing. History is authoritative so a restarted orchestrator does
// not erase past runs.
func (s *Server) handleRunStatus(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")

	run, err := s.store.GetRun(r.Context(), testID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("run not found: %s", testID))
		return
	}

	out := RunStatus{
		TestID: testID,
		Status: run.Status,
		Run:    &run,
	}

	if _, live := s.recorder.Session(testID); live {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if resp, err := s.orch.GetTestStatus(ctx, &pborchestrator.TestStatusRequest{TestId: testID}); err == nil {
			out.Status = resp.Status.String()
			out.ActiveWorkers = resp.ActiveWorkers
			out.TotalVUs = resp.TotalVus
			out.Metrics = metricsFromProto(resp.CurrentMetrics)
		}
	}

	writeJSON(w, http.StatusOK, out)
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
