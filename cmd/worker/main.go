package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/vermakmanish001/go_sentinel/internal/tracer"
	"github.com/vermakmanish001/go_sentinel/internal/worker"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
	pbworker "github.com/vermakmanish001/go_sentinel/proto/worker"
	"github.com/vermakmanish001/go_sentinel/pkg/config"
	"github.com/vermakmanish001/go_sentinel/pkg/logger"
	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// WorkerServer implements the worker gRPC service
type WorkerServer struct {
	pbworker.UnimplementedWorkerServiceServer
	engine    *worker.Engine
	logger    *zap.Logger
	workerID  string
	startTime time.Time
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

	// Connect to orchestrator
	orchURL := cfg.Worker.OrchestratorURL
	if orchURL == "" {
		orchURL = "localhost:50051"
	}
	orchConn, err := grpc.Dial(orchURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Warn("failed to connect to orchestrator", zap.Error(err))
	}
	orchClient := pborchestrator.NewOrchestratorServiceClient(orchConn)

	// Create engine
	engine := worker.NewEngine(workerID, cfg.Worker.MaxVUs, orchClient, log)

	// Create gRPC server
	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Worker.Address, cfg.Worker.Port))
	if err != nil {
		log.Fatal("failed to listen", zap.Error(err))
	}

	grpcServer := grpc.NewServer()
	workerServer := &WorkerServer{
		engine:    engine,
		logger:    log,
		workerID:  workerID,
		startTime: time.Now(),
	}
	pbworker.RegisterWorkerServiceServer(grpcServer, workerServer)

	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Start Prometheus metrics server
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(":9091", mux); err != nil && err != http.ErrServerClosed {
			log.Warn("prometheus server error", zap.Error(err))
		}
	}()

	// Register with orchestrator
	go registerWithOrchestrator(workerID, cfg, orchClient, orchURL, log)

	// Keep LastSeen fresh and preserve accumulated metrics between tests. This
	// sends a full batch: while a test is running it overlaps with the engine's
	// own 1s reporter, and a partial batch would blank out this worker's RPS and
	// latency in the fleet aggregate every time it fired.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = orchClient.ReportMetrics(ctx, worker.NewMetricBatch(workerID, engine.GetMetrics()))
			cancel()
		}
	}()

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

	if req.Plan == nil {
		return &pbworker.RunResponse{Success: false, Message: "plan is required"}, nil
	}

	// Convert proto plan to models
	plan, err := convertProtoPlan(req.Plan)
	if err != nil {
		return &pbworker.RunResponse{Success: false, Message: err.Error()}, nil
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
	return &pbworker.HeartbeatResponse{
		Acknowledged: true,
		Message:      "heartbeat received",
	}, nil
}

// GetStatus returns worker status
func (s *WorkerServer) GetStatus(ctx context.Context, req *pbworker.StatusRequest) (*pbworker.StatusResponse, error) {
	workerStatus := &pbworker.WorkerStatus{
		Status:        pbworker.WorkerStatus_RUNNING,
		ActiveVus:     s.engine.GetActiveVUs(),
		UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
	}

	return &pbworker.StatusResponse{
		Status: workerStatus,
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

// registerWithOrchestrator registers this worker with the orchestrator (with retry)
func registerWithOrchestrator(workerID string, cfg *config.Config, client pborchestrator.OrchestratorServiceClient, orchURL string, log *zap.Logger) {
	log.Info("registering with orchestrator", zap.String("orchestrator_url", orchURL))

	// Use the container hostname so the orchestrator can dial back to us.
	// cfg.Worker.Address is typically "0.0.0.0" (listen-all), which is not
	// dialable from another container.
	host := cfg.Worker.Address
	if host == "" || host == "0.0.0.0" {
		if h, err := os.Hostname(); err == nil {
			host = h
		}
	}
	address := fmt.Sprintf("%s:%d", host, cfg.Worker.Port)

	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := client.RegisterWorker(ctx, &pborchestrator.RegisterWorkerRequest{
			WorkerId: workerID,
			Address:  address,
			MaxVus:   int32(cfg.Worker.MaxVUs),
		})
		cancel()

		if err == nil {
			log.Info("registered with orchestrator", zap.String("worker_id", workerID))
			return
		}

		log.Warn("failed to register with orchestrator",
			zap.Int("attempt", attempt),
			zap.Error(err),
		)

		if attempt < 3 {
			time.Sleep(2 * time.Second)
		}
	}

	log.Error("failed to register with orchestrator after 3 attempts")
}

// convertProtoPlan converts a proto TestPlan to models.TestPlan
func convertProtoPlan(protoPlan *pborchestrator.TestPlan) (*models.TestPlan, error) {
	stages := make([]models.Stage, 0, len(protoPlan.Stages))
	for _, protoStage := range protoPlan.Stages {
		duration, err := time.ParseDuration(protoStage.Duration)
		if err != nil {
			return nil, fmt.Errorf("invalid duration %q: %w", protoStage.Duration, err)
		}

		stage := models.Stage{
			Duration:  duration,
			TargetVUs: protoStage.TargetVus,
		}

		if protoStage.RampUp != "" {
			rampUp, err := time.ParseDuration(protoStage.RampUp)
			if err != nil {
				return nil, fmt.Errorf("invalid ramp_up %q: %w", protoStage.RampUp, err)
			}
			stage.RampUp = rampUp
		}

		stages = append(stages, stage)
	}

	var requests []models.Request
	if protoPlan.Http != nil {
		requests = make([]models.Request, 0, len(protoPlan.Http.Requests))
		for _, protoReq := range protoPlan.Http.Requests {
			req := models.Request{
				Method:     protoReq.Method,
				Path:       protoReq.Path,
				Headers:    protoReq.Headers,
				Body:       protoReq.Body,
				ThinkTime:  time.Duration(protoReq.ThinkTimeMs) * time.Millisecond,
				Assertions: []models.Assertion{},
			}

			for _, protoAssert := range protoReq.Assertions {
				assertion := models.Assertion{}
				switch a := protoAssert.Assertion.(type) {
				case *pborchestrator.Assertion_StatusCode:
					assertion.Type = models.AssertionTypeStatusCode
					assertion.Value = int(a.StatusCode.Expected)
				case *pborchestrator.Assertion_ResponseTime:
					assertion.Type = models.AssertionTypeResponseTime
					assertion.Value = a.ResponseTime.Percentile
					assertion.Threshold = time.Duration(a.ResponseTime.MaxMs) * time.Millisecond
				case *pborchestrator.Assertion_BodyContains:
					assertion.Type = models.AssertionTypeBodyContains
					assertion.Value = a.BodyContains.Substring
				}
				if assertion.Type != "" {
					req.Assertions = append(req.Assertions, assertion)
				}
			}

			requests = append(requests, req)
		}
	}

	baseURL := ""
	var headers map[string]string
	var timeout time.Duration
	if protoPlan.Http != nil {
		baseURL = protoPlan.Http.BaseUrl
		headers = protoPlan.Http.Headers
		timeout = time.Duration(protoPlan.Http.TimeoutMs) * time.Millisecond
	}

	return &models.TestPlan{
		ID:   protoPlan.Id,
		Name: protoPlan.Name,
		Stages: stages,
		HTTP: models.HTTPConfig{
			BaseURL:  baseURL,
			Requests: requests,
			Headers:  headers,
			Timeout:  timeout,
		},
		Variables:         protoPlan.Variables,
		TotalVirtualUsers: protoPlan.TotalVirtualUsers,
	}, nil
}
