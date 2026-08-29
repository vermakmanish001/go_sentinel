# Stage 1: build the dashboard
FROM node:20-alpine AS ui

WORKDIR /ui
COPY web/package.json web/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY web/ ./
RUN npm run build

# Stage 2: build the Go binary with the dashboard embedded
FROM golang:1.23-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Replace the placeholder dist with the real build before go:embed runs
COPY --from=ui /ui/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o api ./cmd/api

# Stage 3: runtime
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /build/api /api

EXPOSE 8090

ENTRYPOINT ["/api"]
