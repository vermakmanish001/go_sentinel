package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"

	"github.com/vermakmanish001/go_sentinel/internal/orchestrator"
	"github.com/vermakmanish001/go_sentinel/internal/runtime"
	"github.com/vermakmanish001/go_sentinel/internal/tracer"
	"github.com/vermakmanish001/go_sentinel/pkg/config"
	"github.com/vermakmanish001/go_sentinel/pkg/logger"
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
