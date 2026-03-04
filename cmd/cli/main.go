package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/vermakmanish001/go_sentinel/internal/runtime"
	"github.com/vermakmanish001/go_sentinel/internal/tui"
	"github.com/vermakmanish001/go_sentinel/pkg/config"
	"github.com/vermakmanish001/go_sentinel/pkg/logger"
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
	plan := convertScenarioToProto(scenario)

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

	// Start metrics streaming in background
	go streamMetrics(client, resp.TestId, model, log)

	// Run TUI
	p := bubbletea.NewProgram(model, bubbletea.WithAltScreen())
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
	if resp.CurrentMetrics != nil {
		fmt.Printf("Current RPS: %.2f\n", resp.CurrentMetrics.Rps.Current)
		fmt.Printf("Error Rate: %.2f%%\n", resp.CurrentMetrics.ErrorRate.Percentage)
	}

	return nil
}

func listNodes(cmd *cobra.Command, args []string) error {
	// TODO: Implement node listing
	fmt.Println("Node listing not yet implemented")
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

func streamMetrics(client pborchestrator.OrchestratorServiceClient, testID string, model *tui.Model, log *zap.Logger) {
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
		_ = resp // TODO: Convert proto metrics to models and update TUI
	}
}

func convertScenarioToProto(scenario *runtime.Scenario) *pborchestrator.TestPlan {
	// TODO: Implement full conversion from scenario to proto
	return &pborchestrator.TestPlan{
		Id:   fmt.Sprintf("test-%d", time.Now().Unix()),
		Name: scenario.Name,
		// TODO: Convert stages, HTTP config, etc.
	}
}
