// Command api serves the GoSentinel dashboard and its JSON/SSE API, translating
// between the browser and the orchestrator's gRPC interface.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/vermakmanish001/go_sentinel/internal/api"
	"github.com/vermakmanish001/go_sentinel/internal/runtime"
	"github.com/vermakmanish001/go_sentinel/internal/store"
	"github.com/vermakmanish001/go_sentinel/pkg/config"
	"github.com/vermakmanish001/go_sentinel/pkg/logger"
	pborchestrator "github.com/vermakmanish001/go_sentinel/proto/orchestrator"
	"github.com/vermakmanish001/go_sentinel/web"
)

func main() {
	createUser := flag.String("create-user", "", "create a user and exit; the password is read from GOSENTINEL_ADMIN_PASSWORD or stdin")
	flag.Parse()

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

	st, err := store.Open(cfg.API.DBPath)
	if err != nil {
		log.Fatal("failed to open database", zap.String("path", cfg.API.DBPath), zap.Error(err))
	}
	defer st.Close()
	log.Info("history database ready", zap.String("path", cfg.API.DBPath))

	if *createUser != "" {
		if err := addUser(st, *createUser); err != nil {
			log.Fatal("could not create user", zap.Error(err))
		}
		log.Info("user created", zap.String("username", *createUser))
		return
	}

	allowlist := api.NewAllowlist(cfg.API.AllowedTargets)
	if allowlist.Unrestricted() {
		log.Warn("no target allowlist configured: this instance will load-test ANY host it can reach. " +
			"Set api.allowed_targets before exposing it beyond localhost.")
	} else {
		log.Info("target allowlist active", zap.Strings("allowed", cfg.API.AllowedTargets))
	}

	if cfg.API.AuthEnabled {
		created, err := api.BootstrapUser(context.Background(), st,
			cfg.API.BootstrapUser, cfg.API.BootstrapPassword)
		if err != nil {
			log.Fatal("authentication is enabled but no account exists. "+
				"Create one with --create-user, or set api.bootstrap_user and api.bootstrap_password",
				zap.Error(err))
		}
		if created {
			log.Warn("created bootstrap account from configuration; change its password",
				zap.String("username", cfg.API.BootstrapUser))
		}
	} else {
		log.Warn("authentication is DISABLED: every endpoint is open to anyone who can reach this port")
	}

	useTLS := cfg.API.TLSCert != "" && cfg.API.TLSKey != ""

	srv := api.New(
		pborchestrator.NewOrchestratorServiceClient(conn),
		runtime.NewParser(log),
		st,
		ui,
		api.AuthConfig{
			Enabled:       cfg.API.AuthEnabled,
			SessionTTL:    cfg.API.SessionTTL,
			SecureCookies: useTLS,
		},
		allowlist,
		log,
	)

	queueCtx, stopQueue := context.WithCancel(context.Background())
	defer stopQueue()
	srv.Start(queueCtx)

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
			zap.Bool("tls", useTLS),
			zap.Bool("auth", cfg.API.AuthEnabled),
		)

		var err error
		if useTLS {
			err = httpServer.ListenAndServeTLS(cfg.API.TLSCert, cfg.API.TLSKey)
		} else {
			err = httpServer.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
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

// addUser creates an account from the command line. The password comes from the
// environment when set, so it never lands in shell history, and otherwise from
// stdin.
func addUser(st store.Store, username string) error {
	password := os.Getenv("GOSENTINEL_ADMIN_PASSWORD")
	if password == "" {
		fmt.Fprintf(os.Stderr, "Password for %q: ", username)
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return err
		}
		password = strings.TrimSpace(line)
	}
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}

	hash, err := api.HashPassword(password)
	if err != nil {
		return err
	}
	_, err = st.CreateUser(context.Background(), username, hash)
	return err
}
