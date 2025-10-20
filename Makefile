.PHONY: help run build test clean dev docker-build docker-run lint fmt

# Default target
.DEFAULT_GOAL := help

# Binary name
BINARY_NAME=tide-api
BINARY_PATH=./$(BINARY_NAME)

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GORUN=$(GOCMD) run
GOCLEAN=$(GOCMD) clean
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

help: ## Display this help screen
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

run: ## Run the server locally
	@echo "Starting tide-api server..."
	$(GORUN) ./cmd/server/main.go

build: ## Build the binary
	@echo "Building $(BINARY_NAME)..."
	$(GOBUILD) -o $(BINARY_PATH) -v ./cmd/server/main.go
	@echo "Binary created at: $(BINARY_PATH)"

test: ## Run all tests
	@echo "Running tests..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	@echo "Coverage report:"
	$(GOCMD) tool cover -func=coverage.out

test-coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report saved to coverage.html"

test-unit: ## Run unit tests only (fast)
	@echo "Running unit tests..."
	$(GOTEST) -v -short ./internal/...

clean: ## Remove build artifacts and caches
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -f $(BINARY_PATH)
	rm -f coverage.out coverage.html
	@echo "Clean complete"

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy
	@echo "Dependencies updated"

fmt: ## Format Go code
	@echo "Formatting code..."
	$(GOFMT) ./...
	@echo "Code formatted"

lint: ## Run linters (requires golangci-lint)
	@echo "Running linters..."
	@which golangci-lint > /dev/null || (echo "golangci-lint not found. Install: https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run ./...

dev: ## Run in development mode (same as run)
	@echo "Starting in development mode..."
	$(GORUN) ./cmd/server/main.go

# Docker targets
docker-build: ## Build Docker image
	@echo "Building Docker image..."
	docker build -t tide-api:latest .
	@echo "Docker image built: tide-api:latest"

docker-run: ## Run Docker container
	@echo "Running Docker container..."
	docker run -p 8080:8080 --env-file .env -v $(PWD)/data:/app/data tide-api:latest

docker-clean: ## Remove Docker image
	@echo "Removing Docker image..."
	docker rmi tide-api:latest

# API testing targets
curl-health: ## Test health endpoint
	@echo "Testing /healthz endpoint..."
	curl -s http://localhost:8080/healthz | jq .

curl-constituents: ## Test constituents endpoint
	@echo "Testing /v1/constituents endpoint..."
	curl -s http://localhost:8080/v1/constituents | jq .

curl-tokyo: ## Test predictions for Tokyo
	@echo "Testing predictions for Tokyo (12 hours, 10min intervals)..."
	curl -s 'http://localhost:8080/v1/tides/predictions?station_id=tokyo&start=2025-10-21T00:00:00Z&end=2025-10-21T12:00:00Z&interval=10m' | jq .

curl-tokyo-extrema: ## Test predictions for Tokyo (show extrema only)
	@echo "Testing predictions for Tokyo (extrema only)..."
	curl -s 'http://localhost:8080/v1/tides/predictions?station_id=tokyo&start=2025-10-21T00:00:00Z&end=2025-10-22T00:00:00Z&interval=10m' | jq '.extrema'

# Development workflow
install: deps ## Install dependencies and prepare for development
	@echo "Setting up development environment..."
	@cp -n .env.example .env || true
	@echo "Development environment ready. Edit .env if needed."

all: clean deps fmt test build ## Run all checks and build

.PHONY: install all curl-health curl-constituents curl-tokyo curl-tokyo-extrema
