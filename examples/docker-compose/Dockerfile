# Dockerfile for go-cluster example nodes
#
# This Dockerfile builds the go-cluster binary and runs the example application.
# For production use, prefer the pre-built images from ghcr.io/ozanturksever/go-cluster

# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk --no-cache add ca-certificates tzdata

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the example binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /go-cluster-example ./examples/docker-compose/main.go

# Runtime stage
FROM alpine:3.19

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata wget

# Create non-root user
RUN adduser -D -g '' app

# Copy binary from builder
COPY --from=builder /go-cluster-example /usr/local/bin/go-cluster-example

# Create data directory
RUN mkdir -p /data && chown app:app /data

# Use non-root user
USER app

WORKDIR /data

# Health check endpoint
EXPOSE 8080
# Metrics endpoint
EXPOSE 9090

ENTRYPOINT ["/usr/local/bin/go-cluster-example"]
