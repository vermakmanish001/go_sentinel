package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/vermakmanish001/go_sentinel/internal/orchestrator"
	"github.com/vermakmanish001/go_sentinel/internal/runtime"
	"github.com/vermakmanish001/go_sentinel/internal/tracer"
	"github.com/vermakmanish001/go_sentinel/pkg/config"
	"github.com/vermakmanish001/go_sentinel/pkg/logger"
	pbmetrics "github.com/vermakmanish001/go_sentinel/proto/metrics"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	if err := logger.Init(cfg.Logging.Level, cfg.Logging.Development); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	log := logger.Get()

	// In dev mode we start a lightweight in-memory orchestrator that does not
	// depend on etcd or tracing. This makes the project runnable with just
	// Go and Docker (for the target service) installed.
	if os.Getenv("GOSENTINEL_DEV_MODE") == "true" {
		log.Info("starting orchestrator in DEV mode (no etcd/tracing)", cfg.LogFields()...)
		if err := startDevOrchestrator(cfg, log); err != nil {
			log.Fatal("dev orchestrator failed", zap.Error(err))
		}
		return
	}

	log.Info("starting orchestrator", cfg.LogFields()...)

	// Initialize tracing
	var tp *trace.TracerProvider
	if cfg.Tracing.Enabled {
		ctx := context.Background()
		tp, err = tracer.Setup(ctx, cfg.Tracing.ServiceName, cfg.Tracing.Environment, cfg.Tracing.Endpoint, log)
		if err != nil {
			log.Warn("failed to initialize tracing", zap.Error(err))
		} else {
			defer tracer.Shutdown(context.Background(), tp, log)
		}
	}

	// Connect to etcd
	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Etcd.Endpoints,
		DialTimeout: cfg.Etcd.DialTimeout,
	})
	if err != nil {
		log.Fatal("failed to connect to etcd", zap.Error(err))
	}
	defer etcdClient.Close()

	// Create components
	nodeManager := orchestrator.NewNodeManager(etcdClient, cfg.Etcd.Prefix, log)
	planDistributor := orchestrator.NewPlanDistributor(nodeManager, log)
	metricsAggregator := orchestrator.NewMetricsAggregator(log)
	parser := runtime.NewParser(log)
	server := orchestrator.NewServer(nodeManager, planDistributor, metricsAggregator, parser, log)

	// Start health checks
	nodeManager.StartHealthCheck(cfg.Orchestrator.HealthCheckInterval, cfg.Orchestrator.WorkerTimeout)

	// Start Prometheus metrics server
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.Orchestrator.MetricsPort), mux); err != nil && err != http.ErrServerClosed {
			log.Warn("prometheus server error", zap.Error(err))
		}
	}()

	// Start server in goroutine
	serverAddr := fmt.Sprintf("%s:%d", cfg.Orchestrator.Address, cfg.Orchestrator.Port)
	go func() {
		if err := server.Start(serverAddr); err != nil {
			log.Fatal("server failed", zap.Error(err))
		}
	}()

	log.Info("orchestrator started", zap.String("address", serverAddr))

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Info("shutting down orchestrator")
	
	// Shutdown components
	if err := nodeManager.Shutdown(); err != nil {
		log.Warn("error shutting down node manager", zap.Error(err))
	}
}

// startDevOrchestrator starts a minimal orchestrator implementation that
// satisfies the gRPC interface but keeps all state in-memory. It is intended
// only for local development and examples.
func startDevOrchestrator(cfg *config.Config, log *zap.Logger) error {
	addr := fmt.Sprintf("%s:%d", cfg.Orchestrator.Address, cfg.Orchestrator.Port)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	grpcServer := grpc.NewServer()
	pborchestrator.RegisterOrchestratorServiceServer(grpcServer, &devOrchestratorServer{
		log:          log,
		tests:        make(map[string]*pborchestrator.TestPlan),
		createdAt:    make(map[string]time.Time),
		workerCounts: make(map[string]int32),
		workers:      make(map[string]*pborchestrator.WorkerInfo),
	})

	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Start Prometheus metrics server
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(":9090", mux); err != nil && err != http.ErrServerClosed {
			log.Warn("prometheus server error", zap.Error(err))
		}
	}()

	log.Info("dev orchestrator started", zap.String("address", addr))

	// Handle graceful shutdown on SIGINT/SIGTERM.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatal("dev orchestrator gRPC server failed", zap.Error(err))
		}
	}()

	<-sigChan
	log.Info("shutting down dev orchestrator")
	grpcServer.GracefulStop()
	return nil
}

// devOrchestratorServer is a minimal in-memory implementation of the
// OrchestratorService used only for local development.
type devOrchestratorServer struct {
	pborchestrator.UnimplementedOrchestratorServiceServer

	log          *zap.Logger
	mu           sync.RWMutex
	tests        map[string]*pborchestrator.TestPlan
	createdAt    map[string]time.Time
	workerCounts map[string]int32
	workers      map[string]*pborchestrator.WorkerInfo
}

func (s *devOrchestratorServer) DistributeTestPlan(ctx context.Context, req *pborchestrator.DistributeRequest) (*pborchestrator.DistributeResponse, error) {
	if req.GetPlan() == nil {
		return nil, fmt.Errorf("plan is required")
	}

	testID := req.Plan.GetId()
	if testID == "" {
		testID = fmt.Sprintf("test-%d", time.Now().UnixNano())
		req.Plan.Id = testID
	}

	s.tests[testID] = req.Plan
	s.createdAt[testID] = time.Now()

	// For dev mode we pretend we always have 3 workers.
	s.workerCounts[testID] = 3

	s.log.Info("dev: received test plan", zap.String("test_id", testID), zap.String("name", req.Plan.GetName()))

	return &pborchestrator.DistributeResponse{
		TestId:            testID,
		WorkersAssigned:   3,
		WorkerVuDistribution: map[string]int32{
			"worker-1": 10,
			"worker-2": 10,
			"worker-3": 10,
		},
	}, nil
}

func (s *devOrchestratorServer) StreamMetrics(req *pborchestrator.StreamMetricsRequest, stream pborchestrator.OrchestratorService_StreamMetricsServer) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
			if err := stream.Send(&pborchestrator.StreamMetricsResponse{
				Snapshot:    &pbmetrics.MetricSnapshot{},
				TimestampMs: time.Now().UnixMilli(),
			}); err != nil {
				return nil
			}
		}
	}
}

func (s *devOrchestratorServer) RegisterWorker(_ context.Context, req *pborchestrator.RegisterWorkerRequest) (*pborchestrator.RegisterWorkerResponse, error) {
	s.mu.Lock()
	s.workers[req.WorkerId] = &pborchestrator.WorkerInfo{
		WorkerId:   req.WorkerId,
		Address:    req.Address,
		MaxVus:     req.MaxVus,
		Status:     "idle",
		LastSeenMs: time.Now().UnixMilli(),
	}
	s.mu.Unlock()
	s.log.Info("dev: worker registered", zap.String("worker_id", req.WorkerId), zap.String("address", req.Address))
	return &pborchestrator.RegisterWorkerResponse{Success: true, Message: "registered"}, nil
}

func (s *devOrchestratorServer) ListWorkers(_ context.Context, _ *pborchestrator.ListWorkersRequest) (*pborchestrator.ListWorkersResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	workers := make([]*pborchestrator.WorkerInfo, 0, len(s.workers))
	for _, w := range s.workers {
		workers = append(workers, w)
	}
	return &pborchestrator.ListWorkersResponse{Workers: workers}, nil
}

func (s *devOrchestratorServer) ReportMetrics(_ context.Context, batch *pbmetrics.MetricBatch) (*pborchestrator.ReportMetricsResponse, error) {
	if batch == nil {
		return &pborchestrator.ReportMetricsResponse{Accepted: false}, nil
	}
	s.mu.Lock()
	if w, ok := s.workers[batch.WorkerId]; ok {
		w.LastSeenMs = time.Now().UnixMilli()
		w.Status = "running"
	}
	s.mu.Unlock()
	return &pborchestrator.ReportMetricsResponse{Accepted: true}, nil
}

func (s *devOrchestratorServer) GetTestStatus(ctx context.Context, req *pborchestrator.TestStatusRequest) (*pborchestrator.TestStatusResponse, error) {
	plan, ok := s.tests[req.GetTestId()]
	if !ok {
		return &pborchestrator.TestStatusResponse{
			Status: pborchestrator.TestStatusResponse_UNKNOWN,
		}, nil
	}

	// Very naive status: RUNNING for 30s, then COMPLETED.
	created := s.createdAt[req.GetTestId()]
	status := pborchestrator.TestStatusResponse_RUNNING
	if time.Since(created) > 30*time.Second {
		status = pborchestrator.TestStatusResponse_COMPLETED
	}

	return &pborchestrator.TestStatusResponse{
		Status:        status,
		ActiveWorkers: s.workerCounts[req.GetTestId()],
		TotalVus:      plan.GetTotalVirtualUsers(),
	}, nil
}

func (s *devOrchestratorServer) StopTest(ctx context.Context, req *pborchestrator.StopTestRequest) (*pborchestrator.StopTestResponse, error) {
	delete(s.tests, req.GetTestId())
	delete(s.createdAt, req.GetTestId())
	delete(s.workerCounts, req.GetTestId())

	return &pborchestrator.StopTestResponse{
		Success: true,
		Message: "dev mode - test removed",
	}, nil
}
