.PHONY: help run build test clean dev docker-build docker-run lint fmt

# Default target
.DEFAULT_GOAL := help

# Binary name
BINARY_NAME=tides-api
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
	@echo "Starting tides-api server..."
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
	docker build -t tides-api:latest .
	@echo "Docker image built: tides-api:latest"

docker-run: ## Run Docker container
	@echo "Running Docker container..."
	docker run -p 8080:8080 --env-file .env -v $(PWD)/data:/app/data tides-api:latest

docker-clean: ## Remove Docker image
	@echo "Removing Docker image..."
	docker rmi tides-api:latest

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

# FES data management
FES_DIR := ./data/fes
FES_USER ?= $(shell cat .fes_credentials 2>/dev/null | head -1)
FES_PASS ?= $(shell cat .fes_credentials 2>/dev/null | tail -1)
FES_HOST := ftp-access.aviso.altimetry.fr
FES_PORT := 2221
FES_REMOTE_PATH := /auxiliary/tide_model/fes2014

fes-setup: ## Setup FES credentials (interactive)
	@echo "FES2014 Data Download Setup"
	@echo "============================"
	@echo ""
	@echo "You need AVISO+ credentials to download FES data."
	@echo "Register at: https://www.aviso.altimetry.fr/"
	@echo ""
	@read -p "Enter AVISO username: " user; \
	read -sp "Enter AVISO password: " pass; \
	echo ""; \
	echo "$$user" > .fes_credentials; \
	echo "$$pass" >> .fes_credentials; \
	chmod 600 .fes_credentials; \
	echo "Credentials saved to .fes_credentials"

fes-list: ## List available FES files on AVISO server
	@echo "Listing FES2014 files on AVISO server..."
	@if [ -z "$(FES_USER)" ] || [ -z "$(FES_PASS)" ]; then \
		echo "Error: FES credentials not found. Run 'make fes-setup' first."; \
		exit 1; \
	fi
	@lftp -u $(FES_USER),$(FES_PASS) sftp://$(FES_HOST):$(FES_PORT) -e "cd $(FES_REMOTE_PATH); ls; bye"

fes-download-constituent: ## Download a specific constituent (usage: make fes-download-constituent CONST=m2)
	@if [ -z "$(CONST)" ]; then \
		echo "Error: Please specify CONST=<constituent_name>"; \
		echo "Example: make fes-download-constituent CONST=m2"; \
		exit 1; \
	fi
	@if [ -z "$(FES_USER)" ] || [ -z "$(FES_PASS)" ]; then \
		echo "Error: FES credentials not found. Run 'make fes-setup' first."; \
		exit 1; \
	fi
	@echo "Downloading $(CONST) constituent from FES2014..."
	@mkdir -p $(FES_DIR)
	@lftp -u $(FES_USER),$(FES_PASS) sftp://$(FES_HOST):$(FES_PORT) -e "\
		cd $(FES_REMOTE_PATH); \
		mget -c $(CONST)*.nc -o $(FES_DIR)/; \
		bye"
	@echo "Downloaded $(CONST) files to $(FES_DIR)/"

fes-download-major: ## Download major constituents (M2, S2, K1, O1, N2, K2, P1, Q1)
	@if [ -z "$(FES_USER)" ] || [ -z "$(FES_PASS)" ]; then \
		echo "Error: FES credentials not found. Run 'make fes-setup' first."; \
		exit 1; \
	fi
	@echo "Downloading major tidal constituents from FES2014..."
	@echo "This may take several minutes..."
	@mkdir -p $(FES_DIR)
	@for const in m2 s2 k1 o1 n2 k2 p1 q1; do \
		echo "Downloading $$const..."; \
		$(MAKE) --no-print-directory fes-download-constituent CONST=$$const; \
	done
	@echo "All major constituents downloaded!"
	@echo "Files saved to $(FES_DIR)/"

fes-download-all: ## Download all available FES2014 constituents
	@if [ -z "$(FES_USER)" ] || [ -z "$(FES_PASS)" ]; then \
		echo "Error: FES credentials not found. Run 'make fes-setup' first."; \
		exit 1; \
	fi
	@echo "Downloading ALL FES2014 constituents..."
	@echo "WARNING: This will download ~5GB of data and may take 30+ minutes"
	@read -p "Continue? [y/N] " confirm; \
	if [ "$$confirm" != "y" ] && [ "$$confirm" != "Y" ]; then \
		echo "Cancelled."; \
		exit 1; \
	fi
	@mkdir -p $(FES_DIR)
	@lftp -u $(FES_USER),$(FES_PASS) sftp://$(FES_HOST):$(FES_PORT) -e "\
		cd $(FES_REMOTE_PATH); \
		mirror --continue --verbose --only-newer --parallel=3 . $(FES_DIR)/; \
		bye"
	@echo "All FES2014 files downloaded to $(FES_DIR)/"

fes-check: ## Check downloaded FES files
	@echo "Checking FES data files in $(FES_DIR)..."
	@if [ ! -d "$(FES_DIR)" ]; then \
		echo "FES directory not found: $(FES_DIR)"; \
		exit 1; \
	fi
	@echo ""
	@echo "NetCDF files found:"
	@find $(FES_DIR) -name "*.nc" -type f | while read file; do \
		echo "  - $$(basename $$file) ($$(du -h $$file | cut -f1))"; \
	done
	@echo ""
	@echo "Constituents detected:"
	@find $(FES_DIR) -name "*_amplitude.nc" -type f | while read file; do \
		basename=$$( basename $$file _amplitude.nc ); \
		echo "  - $$basename"; \
	done

fes-clean: ## Remove downloaded FES files
	@echo "WARNING: This will delete all FES NetCDF files in $(FES_DIR)/"
	@read -p "Are you sure? [y/N] " confirm; \
	if [ "$$confirm" = "y" ] || [ "$$confirm" = "Y" ]; then \
		rm -rf $(FES_DIR)/*.nc; \
		echo "FES files removed."; \
	else \
		echo "Cancelled."; \
	fi

fes-mock: ## Generate mock FES NetCDF files for testing (Pure Go, no Python required!)
	@echo "Generating mock FES NetCDF files using Go..."
	@go run ./cmd/fes-generator \
		-csv ./data/mock_tokyo_constituents.csv \
		-out $(FES_DIR) \
		-region japan \
		-resolution 0.1
	@echo "Mock FES files generated in $(FES_DIR)/"
	@echo ""
	@echo "Test with:"
	@echo "  make run"
	@echo "  curl 'http://localhost:8080/v1/tides/predictions?lat=35.6762&lon=139.6503&start=2025-10-21T00:00:00Z&end=2025-10-21T12:00:00Z&interval=10m'"

fes-mock-fast: ## Generate smaller mock FES (0.5° resolution, faster)
	@echo "Generating low-resolution mock FES..."
	@go run ./cmd/fes-generator \
		-csv ./data/mock_tokyo_constituents.csv \
		-out $(FES_DIR) \
		-region japan \
		-resolution 0.5
	@echo "Low-resolution mock FES generated!"

fes-mock-custom: ## Generate custom region mock FES (set LAT_MIN, LAT_MAX, LON_MIN, LON_MAX)
	@go run ./cmd/fes-generator \
		-csv ./data/mock_tokyo_constituents.csv \
		-out $(FES_DIR) \
		-region custom \
		-lat-min $(LAT_MIN) \
		-lat-max $(LAT_MAX) \
		-lon-min $(LON_MIN) \
		-lon-max $(LON_MAX) \
		-resolution $(RES)

# Alternative: Use curl for single file download
fes-download-curl: ## Download FES file using curl (usage: make fes-download-curl FILE=m2_amplitude.nc)
	@if [ -z "$(FILE)" ]; then \
		echo "Error: Please specify FILE=<filename>"; \
		echo "Example: make fes-download-curl FILE=m2_amplitude.nc"; \
		exit 1; \
	fi
	@if [ -z "$(FES_USER)" ] || [ -z "$(FES_PASS)" ]; then \
		echo "Error: FES credentials not found. Run 'make fes-setup' first."; \
		exit 1; \
	fi
	@echo "Downloading $(FILE) using curl..."
	@mkdir -p $(FES_DIR)
	@curl -u $(FES_USER):$(FES_PASS) \
		"sftp://$(FES_HOST):$(FES_PORT)$(FES_REMOTE_PATH)/$(FILE)" \
		-o "$(FES_DIR)/$(FILE)" \
		--create-dirs --progress-bar
	@echo "Downloaded to $(FES_DIR)/$(FILE)"

.PHONY: install all curl-health curl-constituents curl-tokyo curl-tokyo-extrema
.PHONY: fes-setup fes-list fes-download-constituent fes-download-major fes-download-all
.PHONY: fes-check fes-clean fes-mock fes-mock-fast fes-mock-custom fes-download-curl
