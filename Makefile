.PHONY: proto build ui api dev-ui docker-build up down scale run-example clean test deps

# Generate protobuf files
proto:
	@echo "Generating protobuf files..."
	@mkdir -p proto/orchestrator proto/worker proto/metrics
	@protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		--proto_path=. \
		proto/orchestrator/orchestrator.proto proto/worker/worker.proto proto/metrics/metrics.proto

# Build all binaries. Does not require Node: the dashboard is embedded from
# web/dist, which ships with a placeholder until `make ui` populates it.
build: proto
	@echo "Building binaries..."
	@go build -o bin/orchestrator ./cmd/orchestrator
	@go build -o bin/worker ./cmd/worker
	@go build -o bin/cli ./cmd/cli
	@go build -o bin/api ./cmd/api

# Build the React dashboard into web/dist
ui:
	@echo "Building dashboard..."
	@cd web && npm install --no-audit --no-fund --silent && npm run build

# Dashboard + API server, ready to serve as a single binary
api: ui
	@echo "Building api with embedded dashboard..."
	@go build -o bin/api ./cmd/api
	@echo "Run it with: ./bin/api   (dashboard on http://localhost:8090)"

# Frontend dev server with hot reload, proxying /api to a local api binary
dev-ui:
	@cd web && npm install --no-audit --no-fund --silent && npm run dev

# Build Docker images
docker-build:
	@echo "Building Docker images..."
	@docker-compose -f docker/docker-compose.yml build

# Start full stack
up: docker-build
	@echo "Starting full stack..."
	@docker-compose -f docker/docker-compose.yml up -d
	@echo "Stack started. Access:"
	@echo "  - Orchestrator: localhost:50051"
	@echo "  - Jaeger UI: http://localhost:16686"
	@echo "  - Prometheus: http://localhost:9091"
	@echo "  - Grafana: http://localhost:3000 (admin/admin)"

# Scale workers
scale:
	@echo "Scaling workers to 5..."
	@docker-compose -f docker/docker-compose.yml up -d --scale worker=5

# Run example test
run-example: build
	@echo "Running example test..."
	@./bin/cli run examples/basic_load_test.yaml

# Stop stack
down:
	@echo "Stopping stack..."
	@docker-compose -f docker/docker-compose.yml down

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf bin/
	@rm -rf proto/*/*.pb.go
	@rm -rf web/dist/assets web/node_modules
	@go clean

# Run tests
test:
	@echo "Running tests..."
	@go test ./...

# Install dependencies
deps:
	@echo "Installing dependencies..."
	@go mod download
	@go mod tidy
