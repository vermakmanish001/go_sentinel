package main

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/vermakmanish001/go_sentinel/internal/runtime"
	"github.com/vermakmanish001/go_sentinel/internal/tui"
	"github.com/vermakmanish001/go_sentinel/pkg/config"
	"github.com/vermakmanish001/go_sentinel/pkg/logger"
	"github.com/vermakmanish001/go_sentinel/pkg/models"
	pbmetrics "github.com/vermakmanish001/go_sentinel/proto/metrics"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
)

var (
	orchestratorURL string
	testFile        string
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "gosentinel",
		Short: "GoSentinel - Distributed Load Testing Engine",
		Long:  "A production-grade distributed load testing engine built with Go",
	}

	var runCmd = &cobra.Command{
		Use:   "run <test-file>",
		Short: "Run a load test",
		Args:  cobra.ExactArgs(1),
		RunE:  runTest,
	}

	var statusCmd = &cobra.Command{
		Use:   "status <test-id>",
		Short: "Get test status",
		Args:  cobra.ExactArgs(1),
		RunE:  getStatus,
	}

	var nodesCmd = &cobra.Command{
		Use:   "nodes",
		Short: "List worker nodes",
		RunE:  listNodes,
	}

	var stopCmd = &cobra.Command{
		Use:   "stop <test-id>",
		Short: "Stop a running test",
		Args:  cobra.ExactArgs(1),
		RunE:  stopTest,
	}

	runCmd.Flags().StringVar(&orchestratorURL, "orchestrator", "", "Orchestrator URL (default: from config)")
	rootCmd.AddCommand(runCmd, statusCmd, nodesCmd, stopCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runTest(cmd *cobra.Command, args []string) error {
	testFile := args[0]

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize logger
	if err := logger.Init(cfg.Logging.Level, cfg.Logging.Development); err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	defer logger.Sync()

	log := logger.Get()

	// Parse test plan
	parser := runtime.NewParser(log)
	scenario, err := parser.ParseFile(testFile)
	if err != nil {
		return fmt.Errorf("failed to parse test plan: %w", err)
	}

	// Connect to orchestrator
	url := orchestratorURL
	if url == "" {
		url = cfg.CLI.OrchestratorURL
	}

	conn, err := grpc.Dial(url, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to orchestrator: %w", err)
	}
	defer conn.Close()

	client := pborchestrator.NewOrchestratorServiceClient(conn)

	// Convert scenario to proto plan
	plan := runtime.ScenarioToProto(scenario, fmt.Sprintf("test-%d", time.Now().Unix()))

	// Submit test plan
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DistributeTestPlan(ctx, &pborchestrator.DistributeRequest{
		Plan: plan,
	})
	if err != nil {
		return fmt.Errorf("failed to distribute test plan: %w", err)
	}

	log.Info("test plan distributed",
		zap.String("test_id", resp.TestId),
		zap.Int32("workers", resp.WorkersAssigned),
	)

	// Start TUI
	model := tui.NewModel(resp.TestId)
	p := tea.NewProgram(model, tea.WithAltScreen())

	// Start metrics streaming in background
	go streamMetrics(client, resp.TestId, p, log)

	// Poll worker nodes every 3 seconds and send to TUI
	go func() {
		for {
			time.Sleep(3 * time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			r, err := client.ListWorkers(ctx, &pborchestrator.ListWorkersRequest{})
			cancel()
			if err != nil {
				continue
			}
			nodes := make([]*models.WorkerNode, 0, len(r.Workers))
			for _, w := range r.Workers {
				nodes = append(nodes, &models.WorkerNode{
					ID:      w.WorkerId,
					Address: w.Address,
					MaxVUs:  w.MaxVus,
					Status:  models.WorkerStatus(w.Status),
				})
			}
			p.Send(tui.NodesUpdateMsg(nodes))
		}
	}()

	// Run TUI
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

func getStatus(cmd *cobra.Command, args []string) error {
	testID := args[0]

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	conn, err := grpc.Dial(cfg.CLI.OrchestratorURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pborchestrator.NewOrchestratorServiceClient(conn)
	ctx := context.Background()

	resp, err := client.GetTestStatus(ctx, &pborchestrator.TestStatusRequest{
		TestId: testID,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Test ID: %s\n", testID)
	fmt.Printf("Status: %s\n", resp.Status)
	fmt.Printf("Active Workers: %d\n", resp.ActiveWorkers)
	fmt.Printf("Total VUs: %d\n", resp.TotalVus)
	if resp.CurrentMetrics != nil && resp.CurrentMetrics.Rps != nil {
		fmt.Printf("Current RPS: %.2f\n", resp.CurrentMetrics.Rps.Current)
		fmt.Printf("Error Rate: %.2f%%\n", resp.CurrentMetrics.ErrorRate.Percentage)
	}

	return nil
}

func listNodes(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	conn, err := grpc.Dial(cfg.CLI.OrchestratorURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pborchestrator.NewOrchestratorServiceClient(conn)
	ctx := context.Background()

	resp, err := client.ListWorkers(ctx, &pborchestrator.ListWorkersRequest{})
	if err != nil {
		return err
	}

	if len(resp.Workers) == 0 {
		fmt.Println("No workers registered")
		return nil
	}

	fmt.Printf("%-24s %-22s %-10s %-12s %-22s\n", "ID", "Address", "Max VUs", "Status", "Last Seen")
	fmt.Printf("%-24s %-22s %-10s %-12s %-22s\n",
		"------------------------",
		"----------------------",
		"----------",
		"------------",
		"----------------------",
	)
	for _, w := range resp.Workers {
		lastSeen := time.UnixMilli(w.LastSeenMs).Format("2006-01-02 15:04:05")
		fmt.Printf("%-24s %-22s %-10d %-12s %-22s\n", w.WorkerId, w.Address, w.MaxVus, w.Status, lastSeen)
	}

	return nil
}

func stopTest(cmd *cobra.Command, args []string) error {
	testID := args[0]

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	conn, err := grpc.Dial(cfg.CLI.OrchestratorURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pborchestrator.NewOrchestratorServiceClient(conn)
	ctx := context.Background()

	resp, err := client.StopTest(ctx, &pborchestrator.StopTestRequest{
		TestId: testID,
	})
	if err != nil {
		return err
	}

	if resp.Success {
		fmt.Printf("Test %s stopped successfully\n", testID)
	} else {
		fmt.Printf("Failed to stop test: %s\n", resp.Message)
	}

	return nil
}

func streamMetrics(client pborchestrator.OrchestratorServiceClient, testID string, p *tea.Program, log *zap.Logger) {
	ctx := context.Background()
	stream, err := client.StreamMetrics(ctx, &pborchestrator.StreamMetricsRequest{
		TestId: testID,
	})
	if err != nil {
		log.Error("failed to stream metrics", zap.Error(err))
		return
	}

	for {
		resp, err := stream.Recv()
		if err != nil {
			log.Error("metrics stream error", zap.Error(err))
			return
		}

		if resp.Snapshot == nil {
			continue
		}

		snapshot := protoToSnapshot(resp.Snapshot)
		p.Send(tui.MetricsUpdateMsg(snapshot))
	}
}

// protoToSnapshot converts a proto MetricSnapshot to models.MetricSnapshot
func protoToSnapshot(s *pbmetrics.MetricSnapshot) models.MetricSnapshot {
	snap := models.MetricSnapshot{
		TotalRequests: s.TotalRequests,
		TotalErrors:   s.TotalErrors,
		Timestamp:     time.UnixMilli(s.TimestampMs),
	}
	if s.Rps != nil {
		snap.RPS = models.RPSSnapshot{
			Current:       s.Rps.Current,
			Average:       s.Rps.Average,
			Peak:          s.Rps.Peak,
			WindowSeconds: s.Rps.WindowSeconds,
		}
	}
	if s.Latency != nil {
		snap.Latency = models.LatencyHistogram{
			Min:   time.Duration(s.Latency.MinMs) * time.Millisecond,
			Max:   time.Duration(s.Latency.MaxMs) * time.Millisecond,
			Mean:  time.Duration(s.Latency.MeanMs) * time.Millisecond,
			P50:   time.Duration(s.Latency.P50Ms) * time.Millisecond,
			P95:   time.Duration(s.Latency.P95Ms) * time.Millisecond,
			P99:   time.Duration(s.Latency.P99Ms) * time.Millisecond,
			P999:  time.Duration(s.Latency.P999Ms) * time.Millisecond,
			Count: s.Latency.Count,
		}
	}
	if s.ErrorRate != nil {
		snap.ErrorRate = models.ErrorRate{
			Rate:          s.ErrorRate.Rate,
			Percentage:    s.ErrorRate.Percentage,
			ErrorMessages: s.ErrorRate.ErrorMessages,
		}
	}
	return snap
}
