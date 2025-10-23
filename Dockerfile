# Multi-stage build for minimal final image

# Use specific Alpine version for both stages to ensure NetCDF compatibility
ARG ALPINE_VERSION=3.22

# Stage 1: Build
FROM golang:1.23-alpine AS builder

# Install build dependencies including NetCDF
RUN apk add --no-cache \
    git \
    make \
    gcc \
    g++ \
    musl-dev \
    netcdf \
    netcdf-dev

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build binary with CGO for NetCDF support
RUN CGO_ENABLED=1 go build -ldflags="-w -s" -o tides-api ./cmd/server/main.go

# Stage 2: Runtime
FROM alpine:${ALPINE_VERSION}

# Install runtime dependencies including NetCDF
RUN apk --no-cache add \
    ca-certificates \
    tzdata \
    netcdf \
    wget

# Create app user
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/tides-api .

# Copy entrypoint script
COPY docker-entrypoint.sh /app/

# Copy data directory
COPY data ./data

# Change ownership
RUN chown -R appuser:appuser /app && \
    chmod +x /app/docker-entrypoint.sh

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8080

# Set environment variables
ENV PORT=8080
ENV DATA_DIR=/app/data
ENV FES_DIR=/app/data/fes
ENV TZ=Asia/Tokyo
ENV GIN_MODE=release

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:${PORT}/health || exit 1

# Use entrypoint script
ENTRYPOINT ["/app/docker-entrypoint.sh"]

# Default command (can be overridden)
CMD ["./tides-api"]
