package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/vermakmanish001/go_sentinel/internal/tracer"
	"github.com/vermakmanish001/go_sentinel/internal/worker"
	pbworker "github.com/vermakmanish001/go_sentinel/proto/worker"
	"github.com/vermakmanish001/go_sentinel/pkg/config"
	"github.com/vermakmanish001/go_sentinel/pkg/logger"
	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// WorkerServer implements the worker gRPC service
type WorkerServer struct {
	pbworker.UnimplementedWorkerServiceServer
	engine *worker.Engine
	logger *zap.Logger
	workerID string
}

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

	// Generate worker ID if not set
	workerID := cfg.Worker.ID
	if workerID == "" {
		hostname, _ := os.Hostname()
		workerID = fmt.Sprintf("%s-%d", hostname, os.Getpid())
	}

	log.Info("starting worker", 
		zap.String("worker_id", workerID),
		zap.Int("max_vus", cfg.Worker.MaxVUs),
	)

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

	// Create engine
	engine := worker.NewEngine(workerID, cfg.Worker.MaxVUs, log)

	// Create gRPC server
	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Worker.Address, cfg.Worker.Port))
	if err != nil {
		log.Fatal("failed to listen", zap.Error(err))
	}

	grpcServer := grpc.NewServer()
	workerServer := &WorkerServer{
		engine:   engine,
		logger:   log,
		workerID: workerID,
	}
	pbworker.RegisterWorkerServiceServer(grpcServer, workerServer)

	// Register with orchestrator
	go registerWithOrchestrator(workerID, cfg, log)

	// Start server
	log.Info("worker started", zap.String("address", lis.Addr().String()))

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Info("shutting down worker")
		grpcServer.GracefulStop()
		engine.Shutdown()
	}()

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatal("server failed", zap.Error(err))
	}
}

// RunTest runs a test plan
func (s *WorkerServer) RunTest(ctx context.Context, req *pbworker.RunRequest) (*pbworker.RunResponse, error) {
	s.logger.Info("received run request",
		zap.String("test_id", req.TestId),
		zap.Int32("assigned_vus", req.AssignedVus),
	)

	// Convert proto plan to models (simplified - TODO: full conversion)
	plan := &models.TestPlan{
		ID:   req.TestId,
		Stages: []models.Stage{}, // TODO: Convert from req.Plan
		HTTP: models.HTTPConfig{
			BaseURL:  "", // TODO: Extract from req.Plan
			Requests: []models.Request{},
		},
	}

	// Run test in goroutine
	go func() {
		if err := s.engine.RunTest(context.Background(), req.TestId, plan, req.AssignedVus); err != nil {
			s.logger.Error("test failed", zap.Error(err))
		}
	}()

	return &pbworker.RunResponse{
		Success: true,
		Message: "test started",
	}, nil
}

// Heartbeat sends heartbeat to orchestrator
func (s *WorkerServer) Heartbeat(ctx context.Context, req *pbworker.HeartbeatRequest) (*pbworker.HeartbeatResponse, error) {
	// TODO: Implement heartbeat logic
	return &pbworker.HeartbeatResponse{
		Acknowledged: true,
		Message:      "heartbeat received",
	}, nil
}

// GetStatus returns worker status
func (s *WorkerServer) GetStatus(ctx context.Context, req *pbworker.StatusRequest) (*pbworker.StatusResponse, error) {
	metrics := s.engine.GetMetrics()
	
	status := &pbworker.WorkerStatus{
		Status:        pbworker.WorkerStatus_RUNNING,
		ActiveVus:     int32(0), // TODO: Get from engine
		UptimeSeconds: int64(0), // TODO: Calculate uptime
	}

	return &pbworker.StatusResponse{
		Status: status,
	}, nil
}

// StopTest stops a test
func (s *WorkerServer) StopTest(ctx context.Context, req *pbworker.StopRequest) (*pbworker.StopResponse, error) {
	if err := s.engine.StopTest(); err != nil {
		return &pbworker.StopResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}

	return &pbworker.StopResponse{
		Success: true,
		Message: "test stopped",
	}, nil
}

// registerWithOrchestrator registers this worker with the orchestrator
func registerWithOrchestrator(workerID string, cfg *config.Config, log *zap.Logger) {
	// TODO: Implement registration via gRPC
	// This would connect to the orchestrator and call RegisterWorker
	log.Info("registering with orchestrator", zap.String("orchestrator_url", cfg.Worker.OrchestratorURL))
	
	// For now, just log
	time.Sleep(1 * time.Second)
}
