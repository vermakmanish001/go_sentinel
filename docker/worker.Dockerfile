# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build worker
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o worker ./cmd/worker

# Final stage
FROM alpine:3.19

# Install CA certificates so workers can make HTTPS requests
RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /build/worker /worker

EXPOSE 50052

ENTRYPOINT ["/worker"]
