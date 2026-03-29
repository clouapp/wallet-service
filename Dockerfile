# =============================================================================
# Multi-stage Dockerfile for Vault Custody Service
# =============================================================================

# -----------------------------------------------------------------------------
# Builder Stage - Build the Go binary
# -----------------------------------------------------------------------------
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache \
    git \
    ca-certificates \
    tzdata \
    make

# Set working directory
WORKDIR /app

# Copy dependency files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary for Lambda (arm64) — output outside ./bootstrap/ (Goravel package dir)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
    -ldflags="-s -w -X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo 'dev')" \
    -o /tmp/lambda-bootstrap \
    main.go

# Build the binary for local/Docker (amd64)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo 'dev')" \
    -o vault-server \
    main.go

# -----------------------------------------------------------------------------
# Final Stage - Minimal production image
# -----------------------------------------------------------------------------
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add \
    ca-certificates \
    tzdata \
    curl \
    postgresql-client

# Create non-root user
RUN addgroup -g 1000 vault && \
    adduser -D -u 1000 -G vault vault

# Set working directory
WORKDIR /home/vault

# Copy binary from builder
COPY --from=builder /app/vault-server /usr/local/bin/vault-server
COPY --from=builder /app/go.mod /app/go.sum ./

# Copy database migrations (for local dev)
COPY --from=builder /app/database /home/vault/database

# Change ownership
RUN chown -R vault:vault /home/vault

# Switch to non-root user
USER vault

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=120s --retries=5 \
    CMD curl -f http://localhost:8080/health || exit 1

# Expose port
EXPOSE 8080

# Default command
CMD ["/usr/local/bin/vault-server"]

# -----------------------------------------------------------------------------
# Lambda Stage - For AWS Lambda deployment (arm64)
# -----------------------------------------------------------------------------
FROM public.ecr.aws/lambda/provided:al2023 AS lambda

# Copy the bootstrap binary from builder
COPY --from=builder /tmp/lambda-bootstrap ${LAMBDA_RUNTIME_DIR}/bootstrap

# Set the CMD to your handler
CMD ["bootstrap"]
