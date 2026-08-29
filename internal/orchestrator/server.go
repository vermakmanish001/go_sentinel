package orchestrator

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/vermakmanish001/go_sentinel/internal/runtime"
	pbmetrics "github.com/vermakmanish001/go_sentinel/proto/metrics"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
	pbworker "github.com/vermakmanish001/go_sentinel/proto/worker"
	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// Server is the gRPC server for the orchestrator
type Server struct {
	pborchestrator.UnimplementedOrchestratorServiceServer
	
	nodeManager      *NodeManager
	planDistributor  *PlanDistributor
	metricsAggregator *MetricsAggregator
	parser           *runtime.Parser
	logger           *zap.Logger
}

// NewServer creates a new orchestrator server
func NewServer(
	nodeManager *NodeManager,
	planDistributor *PlanDistributor,
	metricsAggregator *MetricsAggregator,
	parser *runtime.Parser,
	logger *zap.Logger,
) *Server {
	return &Server{
		nodeManager:      nodeManager,
		planDistributor:  planDistributor,
		metricsAggregator: metricsAggregator,
		parser:           parser,
		logger:           logger,
	}
}

// DistributeTestPlan distributes a test plan across workers
func (s *Server) DistributeTestPlan(ctx context.Context, req *pborchestrator.DistributeRequest) (*pborchestrator.DistributeResponse, error) {
	s.logger.Info("distributing test plan",
		zap.String("test_id", req.Plan.Id),
		zap.String("name", req.Plan.Name),
	)

	// Convert proto plan to models
	plan, err := s.convertPlan(req.Plan)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert plan: %v", err)
	}

	// Distribute plan
	distribution, err := s.planDistributor.DistributePlan(ctx, req.Plan.Id, plan)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to distribute plan: %v", err)
	}

	// Actually send the plan to each worker
	go s.dispatchToWorkers(req.Plan, distribution)

	// Convert distribution to proto format
	workerVUDistribution := make(map[string]int32)
	for workerID, vus := range distribution {
		workerVUDistribution[workerID] = vus
	}

	return &pborchestrator.DistributeResponse{
		TestId:               req.Plan.Id,
		WorkersAssigned:      int32(len(distribution)),
		WorkerVuDistribution: workerVUDistribution,
	}, nil
}

// dispatchToWorkers sends the test plan to each assigned worker via RunTest RPC
func (s *Server) dispatchToWorkers(protoPlan *pborchestrator.TestPlan, distribution map[string]int32) {
	// Each worker gets a plan whose stage targets are its own share of that
	// stage. A worker only knows how many VUs it was assigned for the test as a
	// whole, so an unscaled plan would make it run the same VU count in every
	// stage and the fleet would flatten the plan's load curve.
	stageAllocations := make([]map[string]int32, len(protoPlan.Stages))
	for i, stage := range protoPlan.Stages {
		stageAllocations[i] = allocateStageVUs(distribution, stage.TargetVus)
	}

	for workerID, assignedVUs := range distribution {
		node, ok := s.nodeManager.GetNode(workerID)
		if !ok {
			s.logger.Warn("worker not found for dispatch", zap.String("worker_id", workerID))
			continue
		}

		addr := dialableAddress(node.Address)
		wid := workerID
		vus := assignedVUs
		workerPlan := planForWorker(protoPlan, wid, vus, stageAllocations)

		go func() {
			conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				s.logger.Error("failed to connect to worker", zap.String("worker_id", wid), zap.Error(err))
				return
			}
			defer conn.Close()

			client := pbworker.NewWorkerServiceClient(conn)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			resp, err := client.RunTest(ctx, &pbworker.RunRequest{
				TestId:      protoPlan.Id,
				Plan:        workerPlan,
				AssignedVus: vus,
				WorkerId:    wid,
			})
			if err != nil {
				s.logger.Error("failed to dispatch to worker",
					zap.String("worker_id", wid), zap.Error(err))
				return
			}
			s.logger.Info("worker started test",
				zap.String("worker_id", wid),
				zap.Int32("assigned_vus", vus),
				zap.Bool("success", resp.Success),
				zap.String("message", resp.Message),
			)
		}()
	}
}

// planForWorker returns a copy of the plan with every stage's VU target replaced
// by the share allocated to this worker.
func planForWorker(
	protoPlan *pborchestrator.TestPlan,
	workerID string,
	assignedVUs int32,
	stageAllocations []map[string]int32,
) *pborchestrator.TestPlan {
	plan := proto.Clone(protoPlan).(*pborchestrator.TestPlan)
	plan.TotalVirtualUsers = assignedVUs

	for i, stage := range plan.Stages {
		if i < len(stageAllocations) {
			stage.TargetVus = stageAllocations[i][workerID]
		}
	}

	return plan
}

// dialableAddress converts a listen address (0.0.0.0:port) to a dialable one
func dialableAddress(addr string) string {
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "localhost:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

// StreamMetrics streams aggregated metrics
func (s *Server) StreamMetrics(req *pborchestrator.StreamMetricsRequest, stream pborchestrator.OrchestratorService_StreamMetricsServer) error {
	s.logger.Info("starting metrics stream", zap.String("test_id", req.TestId))

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
			snapshot := s.metricsAggregator.GetAggregatedMetrics(req.TestId)

			// Convert to proto
			protoSnapshot := &pbmetrics.MetricSnapshot{
				Rps: &pbmetrics.RPSSnapshot{
					Current:      snapshot.RPS.Current,
					Average:      snapshot.RPS.Average,
					Peak:         snapshot.RPS.Peak,
					WindowSeconds: snapshot.RPS.WindowSeconds,
				},
				Latency: &pbmetrics.LatencyHistogram{
					MinMs:   int64(snapshot.Latency.Min / time.Millisecond),
					MaxMs:   int64(snapshot.Latency.Max / time.Millisecond),
					MeanMs:  int64(snapshot.Latency.Mean / time.Millisecond),
					P50Ms:   int64(snapshot.Latency.P50 / time.Millisecond),
					P95Ms:   int64(snapshot.Latency.P95 / time.Millisecond),
					P99Ms:   int64(snapshot.Latency.P99 / time.Millisecond),
					P999Ms:  int64(snapshot.Latency.P999 / time.Millisecond),
					Count:   snapshot.Latency.Count,
				},
				ErrorRate: &pbmetrics.ErrorRate{
					Rate:       snapshot.ErrorRate.Rate,
					Percentage: snapshot.ErrorRate.Percentage,
				},
				TotalRequests: snapshot.TotalRequests,
				TotalErrors:   snapshot.TotalErrors,
				TimestampMs:   snapshot.Timestamp.UnixMilli(),
			}

			if err := stream.Send(&pborchestrator.StreamMetricsResponse{
				Snapshot:   protoSnapshot,
				TimestampMs: time.Now().UnixMilli(),
			}); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		}
	}
}

// RegisterWorker registers a worker node
func (s *Server) RegisterWorker(ctx context.Context, req *pborchestrator.RegisterWorkerRequest) (*pborchestrator.RegisterWorkerResponse, error) {
	node := &models.WorkerNode{
		ID:       req.WorkerId,
		Address:  req.Address,
		MaxVUs:   req.MaxVus,
		Status:   models.WorkerStatusIdle,
		Metadata: req.Metadata,
		LastSeen: time.Now(),
	}

	if err := s.nodeManager.RegisterNode(node); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to register worker: %v", err)
	}

	return &pborchestrator.RegisterWorkerResponse{
		Success: true,
		Message: "worker registered successfully",
	}, nil
}

// GetTestStatus returns the status of a test
func (s *Server) GetTestStatus(ctx context.Context, req *pborchestrator.TestStatusRequest) (*pborchestrator.TestStatusResponse, error) {
	test, ok := s.planDistributor.GetActiveTest(req.TestId)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "test not found: %s", req.TestId)
	}

	snapshot := s.metricsAggregator.GetAggregatedMetrics(req.TestId)
	testStatus, testWorkers := test.Snapshot()

	// Convert status
	var protoStatus pborchestrator.TestStatusResponse_Status
	switch testStatus {
	case models.TestStatusPending:
		protoStatus = pborchestrator.TestStatusResponse_PENDING
	case models.TestStatusRunning:
		protoStatus = pborchestrator.TestStatusResponse_RUNNING
	case models.TestStatusCompleted:
		protoStatus = pborchestrator.TestStatusResponse_COMPLETED
	case models.TestStatusFailed:
		protoStatus = pborchestrator.TestStatusResponse_FAILED
	case models.TestStatusStopped:
		protoStatus = pborchestrator.TestStatusResponse_STOPPED
	default:
		protoStatus = pborchestrator.TestStatusResponse_UNKNOWN
	}

	// Convert metrics
	protoSnapshot := &pbmetrics.MetricSnapshot{
		Rps: &pbmetrics.RPSSnapshot{
			Current:      snapshot.RPS.Current,
			Average:      snapshot.RPS.Average,
			Peak:         snapshot.RPS.Peak,
			WindowSeconds: snapshot.RPS.WindowSeconds,
		},
		Latency: &pbmetrics.LatencyHistogram{
			MinMs:   int64(snapshot.Latency.Min / time.Millisecond),
			MaxMs:   int64(snapshot.Latency.Max / time.Millisecond),
			MeanMs:  int64(snapshot.Latency.Mean / time.Millisecond),
			P50Ms:   int64(snapshot.Latency.P50 / time.Millisecond),
			P95Ms:   int64(snapshot.Latency.P95 / time.Millisecond),
			P99Ms:   int64(snapshot.Latency.P99 / time.Millisecond),
			P999Ms:  int64(snapshot.Latency.P999 / time.Millisecond),
			Count:   snapshot.Latency.Count,
		},
		ErrorRate: &pbmetrics.ErrorRate{
			Rate:       snapshot.ErrorRate.Rate,
			Percentage: snapshot.ErrorRate.Percentage,
		},
		TotalRequests: snapshot.TotalRequests,
		TotalErrors:   snapshot.TotalErrors,
		TimestampMs:   snapshot.Timestamp.UnixMilli(),
	}

	var totalVUs int32
	for _, vus := range testWorkers {
		totalVUs += vus
	}

	return &pborchestrator.TestStatusResponse{
		Status:         protoStatus,
		ActiveWorkers:  int32(len(testWorkers)),
		TotalVus:       totalVUs,
		CurrentMetrics: protoSnapshot,
	}, nil
}

// ReportMetrics receives a metric batch from a worker
func (s *Server) ReportMetrics(ctx context.Context, batch *pbmetrics.MetricBatch) (*pborchestrator.ReportMetricsResponse, error) {
	if batch == nil {
		return &pborchestrator.ReportMetricsResponse{Accepted: false}, nil
	}
	if batch.RpsSnapshot == nil {
		batch.RpsSnapshot = &pbmetrics.RPSSnapshot{}
	}
	if batch.LatencyHistogram == nil {
		batch.LatencyHistogram = &pbmetrics.LatencyHistogram{}
	}
	if batch.ErrorRate == nil {
		batch.ErrorRate = &pbmetrics.ErrorRate{}
	}
	s.metricsAggregator.UpdateWorkerMetrics(batch.TestId, batch.WorkerId, batch)
	s.nodeManager.UpdateLastSeen(batch.WorkerId)

	workerStatus := models.WorkerStatusIdle
	if batch.TestActive {
		workerStatus = models.WorkerStatusRunning
	}
	s.nodeManager.UpdateStatus(batch.WorkerId, workerStatus)

	// A worker signals it has finished a test by reporting test_active=false.
	// Once every assigned worker has done so, the run is complete — this is the
	// only completion signal the orchestrator receives.
	if batch.TestId != "" && !batch.TestActive {
		if s.planDistributor.MarkWorkerDone(batch.TestId, batch.WorkerId) {
			s.planDistributor.SetStatus(batch.TestId, models.TestStatusCompleted)
			s.logger.Info("test completed", zap.String("test_id", batch.TestId))
		}
	}

	return &pborchestrator.ReportMetricsResponse{Accepted: true}, nil
}

// ListWorkers returns all registered worker nodes
func (s *Server) ListWorkers(ctx context.Context, req *pborchestrator.ListWorkersRequest) (*pborchestrator.ListWorkersResponse, error) {
	nodes := s.nodeManager.GetNodes()
	workers := make([]*pborchestrator.WorkerInfo, len(nodes))
	for i, node := range nodes {
		workers[i] = &pborchestrator.WorkerInfo{
			WorkerId:   node.ID,
			Address:    node.Address,
			MaxVus:     node.MaxVUs,
			Status:     string(node.Status),
			LastSeenMs: node.LastSeen.UnixMilli(),
		}
	}
	return &pborchestrator.ListWorkersResponse{Workers: workers}, nil
}

// StopTest stops a running test by telling each assigned worker to abort it.
func (s *Server) StopTest(ctx context.Context, req *pborchestrator.StopTestRequest) (*pborchestrator.StopTestResponse, error) {
	test, ok := s.planDistributor.GetActiveTest(req.TestId)
	if !ok {
		return &pborchestrator.StopTestResponse{
			Success: false,
			Message: fmt.Sprintf("test not found: %s", req.TestId),
		}, nil
	}

	_, workers := test.Snapshot()

	var wg sync.WaitGroup
	var mu sync.Mutex
	stopped, failed := 0, 0

	for workerID := range workers {
		node, ok := s.nodeManager.GetNode(workerID)
		if !ok {
			mu.Lock()
			failed++
			mu.Unlock()
			continue
		}

		wg.Add(1)
		go func(wid, addr string) {
			defer wg.Done()
			if err := s.stopWorkerTest(addr, wid, req.TestId); err != nil {
				s.logger.Error("failed to stop test on worker",
					zap.String("worker_id", wid), zap.Error(err))
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			mu.Lock()
			stopped++
			mu.Unlock()
		}(workerID, dialableAddress(node.Address))
	}
	wg.Wait()

	s.planDistributor.SetStatus(req.TestId, models.TestStatusStopped)

	if failed > 0 {
		return &pborchestrator.StopTestResponse{
			Success: false,
			Message: fmt.Sprintf("stopped on %d/%d workers", stopped, len(workers)),
		}, nil
	}
	return &pborchestrator.StopTestResponse{
		Success: true,
		Message: fmt.Sprintf("stopped on %d workers", stopped),
	}, nil
}

// stopWorkerTest issues the StopTest RPC to a single worker.
func (s *Server) stopWorkerTest(addr, workerID, testID string) error {
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := pbworker.NewWorkerServiceClient(conn).StopTest(ctx, &pbworker.StopRequest{TestId: testID})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("worker %s refused: %s", workerID, resp.Message)
	}
	return nil
}

// convertPlan converts a proto plan to a models plan
func (s *Server) convertPlan(protoPlan *pborchestrator.TestPlan) (*models.TestPlan, error) {
	stages := make([]models.Stage, 0, len(protoPlan.Stages))
	for _, protoStage := range protoPlan.Stages {
		duration, err := time.ParseDuration(protoStage.Duration)
		if err != nil {
			return nil, fmt.Errorf("invalid duration: %w", err)
		}

		stage := models.Stage{
			Duration:  duration,
			TargetVUs: protoStage.TargetVus,
		}

		if protoStage.RampUp != "" {
			rampUp, err := time.ParseDuration(protoStage.RampUp)
			if err != nil {
				return nil, fmt.Errorf("invalid ramp_up: %w", err)
			}
			stage.RampUp = rampUp
		}

		stages = append(stages, stage)
	}

	requests := make([]models.Request, 0, len(protoPlan.Http.Requests))
	for _, protoReq := range protoPlan.Http.Requests {
		req := models.Request{
			Method:     protoReq.Method,
			Path:       protoReq.Path,
			Headers:    protoReq.Headers,
			Body:       protoReq.Body,
			Assertions: []models.Assertion{},
		}

		// Parse assertions
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
			req.Assertions = append(req.Assertions, assertion)
		}

		requests = append(requests, req)
	}

	return &models.TestPlan{
		ID:   protoPlan.Id,
		Name: protoPlan.Name,
		Stages: stages,
		HTTP: models.HTTPConfig{
			BaseURL:  protoPlan.Http.BaseUrl,
			Requests: requests,
			Headers:  protoPlan.Http.Headers,
			Timeout:  time.Duration(protoPlan.Http.TimeoutMs) * time.Millisecond,
		},
		Variables:       protoPlan.Variables,
		TotalVirtualUsers: protoPlan.TotalVirtualUsers,
	}, nil
}

// Start starts the gRPC server
func (s *Server) Start(address string) error {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	grpcServer := grpc.NewServer()
	pborchestrator.RegisterOrchestratorServiceServer(grpcServer, s)

	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	s.logger.Info("orchestrator server starting", zap.String("address", address))

	if err := grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}
