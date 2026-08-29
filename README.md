# GoSentinel

A production-grade distributed load testing engine built with Go.

## Features

- **Distributed Architecture**: Scale load tests across multiple worker nodes
- **Real-time Metrics**: Live dashboard with RPS, latency, and error rate metrics
- **Consistent Hashing**: Intelligent load distribution using consistent hashing
- **OpenTelemetry Integration**: Full observability with Jaeger tracing
- **TUI Dashboard**: Beautiful terminal UI built with Bubbletea
- **YAML Test Plans**: Declarative test configuration
- **gRPC Communication**: High-performance inter-service communication
- **Worker Discovery**: Automatic worker registration via etcd

## Architecture

```
┌─────────────┐
│     CLI     │
└──────┬──────┘
       │ gRPC
       ▼
┌─────────────┐     ┌─────────────┐
│Orchestrator │◄────┤    etcd     │
└──────┬──────┘     └─────────────┘
       │ gRPC
       ├──────────┬──────────┐
       ▼          ▼          ▼
    ┌──────┐  ┌──────┐  ┌──────┐
    │Worker│  │Worker│  │Worker│
    └──────┘  └──────┘  └──────┘
```

## Quick Start

### Prerequisites

- Go 1.22+
- Docker and Docker Compose
- protoc (for building from source)

### Build

```bash
# Generate protobuf files
make proto

# Build all binaries
make build
```

### Run with Docker Compose

```bash
# Start full stack (orchestrator + 3 workers + etcd + jaeger + prometheus + grafana)
make up

# Scale workers
make scale

# Stop stack
make down
```

### Run Example Test

```bash
# Build CLI first
make build

# Run example test
make run-example
```

## Web dashboard

A browser UI for building and running tests — no YAML required. Enter a base
URL, add stages and requests with assertions, hit Run, and watch throughput,
latency percentiles and errors stream in live.

```bash
make api        # builds the React dashboard and embeds it in bin/api
./bin/api       # http://localhost:8090
```

The dashboard ships inside the Go binary via `go:embed`, so production is a
single artifact with no separate static host. It is also part of the Docker
stack (`make up`) on port 8090.

### Frontend development

```bash
./bin/api                    # terminal 1: API on :8090
make dev-ui                  # terminal 2: Vite with hot reload on :5173
```

Vite proxies `/api` to the Go server, so the browser sees one origin and no CORS
configuration is needed.

### API

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/runs` | Start a run from a JSON plan |
| `GET` | `/api/runs/{id}` | Run status and current metrics |
| `GET` | `/api/runs/{id}/stream` | Server-Sent Events: `metrics`, `status`, `end` |
| `POST` | `/api/runs/{id}/stop` | Stop a run on every assigned worker |
| `GET` | `/api/workers` | Registered workers and total VU capacity |
| `GET` | `/api/health` | Liveness plus orchestrator reachability |

The JSON plan uses the same schema as the YAML files in `examples/`, so a plan
built in the UI and one written by hand go through identical validation. Runs
are serialised — starting one while another is in flight returns `409`, because
concurrent runs on shared workers contend for the same connection pools and
distort the latency they are meant to measure.

## Configuration

Configuration can be provided via:
- Environment variables (prefixed with `GOSENTINEL_`)
- YAML config file (`configs/config.yaml`)
- Default values

Key configuration options:

```yaml
orchestrator:
  address: "0.0.0.0"
  port: 50051
  worker_timeout: "30s"

worker:
  port: 50052
  max_vus: 1000
  orchestrator_url: "localhost:50051"

etcd:
  endpoints:
    - "localhost:2379"
  prefix: "/gosentinel"

tracing:
  enabled: true
  endpoint: "localhost:4317"
  service_name: "gosentinel"
```

## Test Plan Format

Example test plan (`examples/basic_load_test.yaml`):

```yaml
name: Basic Load Test

stages:
  - duration: 30s
    target_vus: 50
  - duration: 1m
    target_vus: 200
  - duration: 30s
    target_vus: 50

http:
  base_url: "http://target-service:8080"
  timeout: 30s
  requests:
    - method: GET
      path: /api/health
      assertions:
        - status_code: 200
        - response_time_p99_ms: 500
```

## CLI Commands

```bash
# Run a test
gosentinel run <test-file.yaml>

# Get test status
gosentinel status <test-id>

# List worker nodes
gosentinel nodes

# Stop a test
gosentinel stop <test-id>
```

## Development

### Project Structure

```
gosentinel/
├── cmd/              # Entry points (orchestrator, worker, cli)
├── proto/            # Protobuf definitions
├── internal/         # Internal packages
│   ├── orchestrator/ # Orchestrator logic
│   ├── worker/       # Worker engine
│   ├── runtime/      # DSL parser, plugins
│   ├── tracer/       # OpenTelemetry integration
│   └── tui/          # Terminal UI
├── pkg/              # Shared packages
│   ├── config/       # Configuration
│   ├── logger/       # Logging
│   └── models/       # Domain models
├── examples/          # Example test plans
├── docker/            # Docker files
└── configs/          # Configuration files
```

### Running Tests

```bash
make test
```

### Code Quality

- All goroutines have defined exit conditions
- Context propagation throughout
- Structured logging with zap
- No global mutable state
- Interfaces for testability

## Monitoring

- **Jaeger**: Distributed tracing (http://localhost:16686)
- **Prometheus**: Metrics collection (http://localhost:9091)
- **Grafana**: Dashboards (http://localhost:3000)

## License

MIT
