package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// Config represents the application configuration
type Config struct {
	Orchestrator OrchestratorConfig
	Worker       WorkerConfig
	CLI          CLIConfig
	API          APIConfig
	Etcd         EtcdConfig
	Tracing      TracingConfig
	Logging      LoggingConfig
}

// OrchestratorConfig represents orchestrator-specific configuration
type OrchestratorConfig struct {
	Address             string
	Port                int
	MetricsPort         int
	WorkerTimeout       time.Duration
	HealthCheckInterval time.Duration
}

// WorkerConfig represents worker-specific configuration
type WorkerConfig struct {
	ID                string
	Address           string
	Port              int
	MaxVUs            int
	OrchestratorURL   string
	HeartbeatInterval time.Duration
}

// APIConfig represents HTTP API / dashboard configuration
type APIConfig struct {
	Address         string
	Port            int
	OrchestratorURL string
	// DBPath is the SQLite file holding run history and saved plans.
	DBPath string
}

// CLIConfig represents CLI-specific configuration
type CLIConfig struct {
	OrchestratorURL string
	RefreshInterval time.Duration
}

// EtcdConfig represents etcd configuration
type EtcdConfig struct {
	Endpoints   []string
	DialTimeout time.Duration
	Prefix      string
}

// TracingConfig represents OpenTelemetry tracing configuration
type TracingConfig struct {
	Enabled     bool
	Endpoint    string
	ServiceName string
	Environment string
}

// LoggingConfig represents logging configuration
type LoggingConfig struct {
	Level       string
	Development bool
}

// Load loads configuration from environment variables and config files
func Load() (*Config, error) {
	viper.SetDefault("orchestrator.address", "0.0.0.0")
	viper.SetDefault("orchestrator.port", 50051)
	viper.SetDefault("orchestrator.metrics_port", 9090)
	viper.SetDefault("orchestrator.worker_timeout", "30s")
	viper.SetDefault("orchestrator.health_check_interval", "5s")

	viper.SetDefault("worker.port", 50052)
	viper.SetDefault("worker.max_vus", 1000)
	viper.SetDefault("worker.heartbeat_interval", "10s")

	viper.SetDefault("api.address", "0.0.0.0")
	// 8080 is taken by the bundled httpbin load target in docker-compose.
	viper.SetDefault("api.port", 8090)
	viper.SetDefault("api.orchestrator_url", "localhost:50051")
	viper.SetDefault("api.db_path", "gosentinel.db")

	viper.SetDefault("cli.orchestrator_url", "localhost:50051")
	viper.SetDefault("cli.refresh_interval", "1s")

	viper.SetDefault("etcd.endpoints", []string{"localhost:2379"})
	viper.SetDefault("etcd.dial_timeout", "5s")
	viper.SetDefault("etcd.prefix", "/gosentinel")

	viper.SetDefault("tracing.enabled", true)
	viper.SetDefault("tracing.endpoint", "localhost:4317")
	viper.SetDefault("tracing.service_name", "gosentinel")
	viper.SetDefault("tracing.environment", "development")

	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.development", false)

	// Read from environment — replace dots with underscores so that
	// "worker.orchestrator_url" maps to GOSENTINEL_WORKER_ORCHESTRATOR_URL.
	viper.SetEnvPrefix("GOSENTINEL")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Read from config file if present
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./configs")
	viper.AddConfigPath("/etc/gosentinel")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		// Config file not found is okay, we'll use defaults/env vars
	}

	cfg := &Config{}

	// Orchestrator config
	cfg.Orchestrator.Address = viper.GetString("orchestrator.address")
	cfg.Orchestrator.Port = viper.GetInt("orchestrator.port")
	cfg.Orchestrator.MetricsPort = viper.GetInt("orchestrator.metrics_port")
	cfg.Orchestrator.WorkerTimeout = viper.GetDuration("orchestrator.worker_timeout")
	cfg.Orchestrator.HealthCheckInterval = viper.GetDuration("orchestrator.health_check_interval")

	// Worker config
	cfg.Worker.ID = viper.GetString("worker.id")
	cfg.Worker.Address = viper.GetString("worker.address")
	cfg.Worker.Port = viper.GetInt("worker.port")
	cfg.Worker.MaxVUs = viper.GetInt("worker.max_vus")
	cfg.Worker.OrchestratorURL = viper.GetString("worker.orchestrator_url")
	cfg.Worker.HeartbeatInterval = viper.GetDuration("worker.heartbeat_interval")

	// API config
	cfg.API.Address = viper.GetString("api.address")
	cfg.API.Port = viper.GetInt("api.port")
	cfg.API.OrchestratorURL = viper.GetString("api.orchestrator_url")
	cfg.API.DBPath = viper.GetString("api.db_path")

	// CLI config
	cfg.CLI.OrchestratorURL = viper.GetString("cli.orchestrator_url")
	cfg.CLI.RefreshInterval = viper.GetDuration("cli.refresh_interval")

	// Etcd config
	cfg.Etcd.Endpoints = viper.GetStringSlice("etcd.endpoints")
	cfg.Etcd.DialTimeout = viper.GetDuration("etcd.dial_timeout")
	cfg.Etcd.Prefix = viper.GetString("etcd.prefix")

	// Tracing config
	cfg.Tracing.Enabled = viper.GetBool("tracing.enabled")
	cfg.Tracing.Endpoint = viper.GetString("tracing.endpoint")
	cfg.Tracing.ServiceName = viper.GetString("tracing.service_name")
	cfg.Tracing.Environment = viper.GetString("tracing.environment")

	// Logging config
	cfg.Logging.Level = viper.GetString("logging.level")
	cfg.Logging.Development = viper.GetBool("logging.development")

	return cfg, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Orchestrator.Port <= 0 {
		return fmt.Errorf("orchestrator port must be positive")
	}
	if c.Worker.Port <= 0 {
		return fmt.Errorf("worker port must be positive")
	}
	if len(c.Etcd.Endpoints) == 0 {
		return fmt.Errorf("etcd endpoints cannot be empty")
	}
	return nil
}

// LogFields returns zap fields for logging the config
func (c *Config) LogFields() []zap.Field {
	return []zap.Field{
		zap.String("orchestrator_address", c.Orchestrator.Address),
		zap.Int("orchestrator_port", c.Orchestrator.Port),
		zap.String("worker_id", c.Worker.ID),
		zap.Int("worker_port", c.Worker.Port),
		zap.Bool("tracing_enabled", c.Tracing.Enabled),
	}
}
