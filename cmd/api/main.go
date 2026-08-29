// Command api serves the GoSentinel dashboard and its JSON/SSE API, translating
// between the browser and the orchestrator's gRPC interface.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/vermakmanish001/go_sentinel/internal/api"
	"github.com/vermakmanish001/go_sentinel/internal/runtime"
	"github.com/vermakmanish001/go_sentinel/pkg/config"
	"github.com/vermakmanish001/go_sentinel/pkg/logger"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
	"github.com/vermakmanish001/go_sentinel/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	if err := logger.Init(cfg.Logging.Level, cfg.Logging.Development); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()
	log := logger.Get()

	orchURL := cfg.API.OrchestratorURL
	conn, err := grpc.NewClient(orchURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal("failed to create orchestrator client", zap.Error(err))
	}
	defer conn.Close()

	ui, err := web.Dist()
	if err != nil {
		log.Fatal("failed to open embedded frontend", zap.Error(err))
	}

	srv := api.New(
		pborchestrator.NewOrchestratorServiceClient(conn),
		runtime.NewParser(log),
		ui,
		log,
	)

	addr := fmt.Sprintf("%s:%d", cfg.API.Address, cfg.API.Port)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv.Routes(),
		// No WriteTimeout: SSE connections are long-lived by design.
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("api server starting",
			zap.String("address", addr),
			zap.String("orchestrator", orchURL),
		)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("api server failed", zap.Error(err))
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Info("shutting down api server")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Warn("graceful shutdown failed", zap.Error(err))
	}
}
