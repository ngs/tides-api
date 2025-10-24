.PHONY: help run build test clean dev docker-build docker-run lint fmt

# Default target
.DEFAULT_GOAL := help

# Binary name
BINARY_NAME=tides-api
BINARY_PATH=./$(BINARY_NAME)

# GCP parameters
PROJECT_ID ?= $(shell gcloud config get-value project 2>/dev/null)
REGION ?= asia-northeast1
GCS_FES_BUCKET ?= tides-app-fes
GCS_BATHY_BUCKET ?= tides-app-bathymetry

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
FES_PORT := 21
FES_REMOTE_PATH := /auxiliary/tide_model/fes2014_elevations_and_load/fes2014b_elevations

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
	@lftp -u $(FES_USER),$(FES_PASS) ftp://$(FES_HOST):$(FES_PORT) -e "cd $(FES_REMOTE_PATH); ls; bye"

fes-download-ocean-tide: ## Download FES2014 ocean tide data archive (~1.9GB)
	@if [ -z "$(FES_USER)" ] || [ -z "$(FES_PASS)" ]; then \
		echo "Error: FES credentials not found. Run 'make fes-setup' first."; \
		exit 1; \
	fi
	@echo "Downloading FES2014 ocean tide data archive..."
	@echo "This is a 1.9GB file and will take several minutes..."
	@mkdir -p $(FES_DIR)
	@lftp -u $(FES_USER),$(FES_PASS) ftp://$(FES_HOST):$(FES_PORT) -e "\
		cd $(FES_REMOTE_PATH); \
		lcd $(FES_DIR); \
		get -c ocean_tide.tar.xz; \
		get -c ocean_tide.tar.xz.sha256sum; \
		bye"
	@echo "Downloaded ocean_tide.tar.xz to $(FES_DIR)/"
	@echo "Verifying checksum..."
	@cd $(FES_DIR) && sha256sum -c ocean_tide.tar.xz.sha256sum
	@echo "Extracting archive..."
	@cd $(FES_DIR) && tar -xJf ocean_tide.tar.xz
	@echo "Cleaning up archive..."
	@rm -f $(FES_DIR)/ocean_tide.tar.xz $(FES_DIR)/ocean_tide.tar.xz.sha256sum
	@echo "FES2014 ocean tide data extracted to $(FES_DIR)/"

fes-download-major: fes-download-ocean-tide ## Alias for downloading FES2014 ocean tide data

fes-download-all: fes-download-ocean-tide ## Alias for downloading FES2014 ocean tide data (same as fes-download-major)

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

# GCP Cloud Storage targets
gcs-check-project: ## Check current GCP project
	@echo "Current GCP project: $(PROJECT_ID)"
	@echo "FES bucket name: $(GCS_FES_BUCKET)"
	@echo "Region: $(REGION)"

gcs-create-bucket: ## Create GCS bucket for FES data
	@echo "Creating Cloud Storage bucket for FES data..."
	@if [ -z "$(PROJECT_ID)" ]; then \
		echo "Error: PROJECT_ID not set. Run 'gcloud config set project YOUR_PROJECT_ID'"; \
		exit 1; \
	fi
	@gsutil ls -b gs://$(GCS_FES_BUCKET) 2>/dev/null || \
	(gsutil mb -l $(REGION) -b on gs://$(GCS_FES_BUCKET) && \
	 echo "Bucket created: gs://$(GCS_FES_BUCKET)")
	@echo "Storage cost estimate: ~¥13/month for 4.3GB in $(REGION)"

gcs-upload-fes: ## Upload FES data to Cloud Storage
	@echo "Uploading FES data to Cloud Storage..."
	@if [ ! -d "$(FES_DIR)" ]; then \
		echo "Error: FES directory not found: $(FES_DIR)"; \
		echo "Run 'make fes-download-major' or 'make fes-mock' first."; \
		exit 1; \
	fi
	@echo "Checking bucket exists..."
	@gsutil ls -b gs://$(GCS_FES_BUCKET) >/dev/null 2>&1 || \
	(echo "Error: Bucket gs://$(GCS_FES_BUCKET) not found. Run 'make gcs-create-bucket' first." && exit 1)
	@echo "Starting upload (this may take a few minutes)..."
	@gsutil -m rsync -r -d $(FES_DIR) gs://$(GCS_FES_BUCKET)/
	@echo ""
	@echo "Upload complete!"
	@echo "Bucket: gs://$(GCS_FES_BUCKET)"
	@echo "Files uploaded:"
	@gsutil du -sh gs://$(GCS_FES_BUCKET)
	@echo ""
	@echo "Monthly cost estimate: ~¥13 (Standard Storage in $(REGION))"

gcs-download-fes: ## Download FES data from Cloud Storage
	@echo "Downloading FES data from Cloud Storage..."
	@mkdir -p $(FES_DIR)
	@gsutil -m rsync -r gs://$(GCS_FES_BUCKET)/ $(FES_DIR)/
	@echo "Download complete: $(FES_DIR)/"

gcs-list-fes: ## List FES files in Cloud Storage
	@echo "Listing FES files in gs://$(GCS_FES_BUCKET)..."
	@gsutil ls -lh gs://$(GCS_FES_BUCKET)/

gcs-check-fes: ## Check FES data in Cloud Storage
	@echo "Checking FES data in Cloud Storage..."
	@echo "Bucket: gs://$(GCS_FES_BUCKET)"
	@echo "Region: $(REGION)"
	@echo ""
	@gsutil du -sh gs://$(GCS_FES_BUCKET)
	@echo ""
	@echo "Files:"
	@gsutil ls gs://$(GCS_FES_BUCKET)/*.nc 2>/dev/null | wc -l | xargs -I {} echo "  NetCDF files: {}"
	@echo ""
	@echo "Monthly cost estimate: ~¥13 (Standard Storage)"

gcs-delete-bucket: ## Delete GCS bucket (WARNING: destroys all FES data in cloud)
	@echo "WARNING: This will delete the bucket and all FES data in Cloud Storage!"
	@echo "Bucket: gs://$(GCS_FES_BUCKET)"
	@read -p "Are you sure? [y/N] " confirm; \
	if [ "$$confirm" = "y" ] || [ "$$confirm" = "Y" ]; then \
		gsutil -m rm -r gs://$(GCS_FES_BUCKET); \
		echo "Bucket deleted."; \
	else \
		echo "Cancelled."; \
	fi

# Bathymetry and geoid data management
BATHY_DIR := ./data/bathymetry
GEBCO_URL := https://dap.ceda.ac.uk/bodc/gebco/global/gebco_2025/ice_surface_elevation/netcdf/gebco_2025.zip?download=1
GEBCO_FILE := GEBCO_2025.nc
DTU_MSS_URL := https://data.dtu.dk/ndownloader/files/53106539
DTU_MSS_FILE := DTU21MSS_1min_WGS84.nc
EGM2008_URL := https://earth-info.nga.mil/php/download.php?file=egm-08-raster
EGM2008_FILE := egm2008-1.pgm
EGM2008_NC := egm2008_1m.nc
USER_AGENT := Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36

bathy-setup: ## Create bathymetry data directory
	@echo "Creating bathymetry data directory..."
	@mkdir -p $(BATHY_DIR)
	@echo "Directory created: $(BATHY_DIR)/"

bathy-download-gebco: ## Download GEBCO 2025 bathymetry data (~4GB compressed, 7.5GB uncompressed)
	@echo "Downloading GEBCO 2025 bathymetry data..."
	@echo "WARNING: This is a 4GB compressed file (7.5GB uncompressed) and will take significant time."
	@echo "Source: General Bathymetric Chart of the Oceans (GEBCO)"
	@echo "License: Public Domain"
	@echo ""
	@mkdir -p $(BATHY_DIR)
	@if [ -f "$(BATHY_DIR)/$(GEBCO_FILE)" ]; then \
		echo "✓ GEBCO file already exists: $(BATHY_DIR)/$(GEBCO_FILE)"; \
		echo "Skipping download."; \
		ls -lh $(BATHY_DIR)/$(GEBCO_FILE); \
	else \
		echo "Downloading GEBCO_2025 Grid (NetCDF format)..."; \
		if [ -f "$(BATHY_DIR)/gebco_2025.zip" ]; then \
			echo "Found existing gebco_2025.zip, checking integrity..."; \
			if unzip -t $(BATHY_DIR)/gebco_2025.zip > /dev/null 2>&1; then \
				echo "✓ Existing zip file is complete, extracting..."; \
			else \
				echo "⚠ Existing zip file is incomplete, resuming download..."; \
				cd $(BATHY_DIR) && curl -C - -L -o gebco_2025.zip "$(GEBCO_URL)"; \
			fi; \
		else \
			echo "Downloading gebco_2025.zip (4GB)..."; \
			cd $(BATHY_DIR) && curl -L -o gebco_2025.zip "$(GEBCO_URL)"; \
		fi; \
		echo "Extracting NetCDF file..."; \
		cd $(BATHY_DIR) && \
		unzip -j gebco_2025.zip "GEBCO_2025.nc" -d . && \
		mv GEBCO_2025.nc $(GEBCO_FILE) && \
		rm gebco_2025.zip && \
		echo "" && \
		echo "Download complete: $(BATHY_DIR)/$(GEBCO_FILE)" && \
		ls -lh $(GEBCO_FILE); \
	fi

bathy-download-dtu-mss: ## Download DTU21 Mean Sea Surface data (~500MB)
	@echo "Downloading DTU21 Mean Sea Surface data..."
	@echo "Size: ~500MB"
	@echo "Source: DTU Space (Technical University of Denmark)"
	@echo "License: Free for scientific and commercial use"
	@echo ""
	@mkdir -p $(BATHY_DIR)
	@if [ -f "$(BATHY_DIR)/$(DTU_MSS_FILE)" ]; then \
		echo "✓ DTU MSS file already exists: $(BATHY_DIR)/$(DTU_MSS_FILE)"; \
		echo "Skipping download."; \
		ls -lh $(BATHY_DIR)/$(DTU_MSS_FILE); \
	else \
		echo "Downloading $(DTU_MSS_FILE)..."; \
		echo "Note: Using browser User-Agent to access DTU data repository"; \
		cd $(BATHY_DIR) && \
		curl -C - -o $(DTU_MSS_FILE) -L -A "$(USER_AGENT)" "$(DTU_MSS_URL)"; \
		echo ""; \
		echo "Download complete: $(BATHY_DIR)/$(DTU_MSS_FILE)"; \
		ls -lh $(DTU_MSS_FILE); \
	fi

geoid-download-egm2008: ## Download EGM2008 geoid data (~100MB, requires GMT)
	@echo "Downloading EGM2008 geoid data..."
	@echo "Size: ~100MB (1 arc-minute resolution)"
	@echo "Source: NASA/NGA Earth Gravitational Model 2008"
	@echo "License: Public Domain"
	@echo ""
	@mkdir -p $(BATHY_DIR)
	@if [ -f "$(BATHY_DIR)/$(EGM2008_NC)" ]; then \
		echo "✓ EGM2008 file already exists: $(BATHY_DIR)/$(EGM2008_NC)"; \
		echo "Skipping download."; \
		ls -lh $(BATHY_DIR)/$(EGM2008_NC); \
	else \
		echo "Checking for GMT (Generic Mapping Tools)..."; \
		if command -v gmt >/dev/null 2>&1; then \
			echo "✓ GMT found, downloading geoid grid..."; \
			echo "Note: This uses GMT's @earth_geoid_01m remote dataset"; \
			cd $(BATHY_DIR) && \
			gmt grdcut @earth_geoid_01m -R-180/180/-90/90 -G$(EGM2008_NC) && \
			echo "" && \
			echo "Download complete: $(BATHY_DIR)/$(EGM2008_NC)" && \
			ls -lh $(EGM2008_NC); \
		else \
			echo "✗ GMT not found. Please install GMT first:"; \
			echo "  brew install gmt  (macOS)"; \
			echo "  apt-get install gmt  (Ubuntu/Debian)"; \
			echo ""; \
			echo "Alternative: Download manually from:"; \
			echo "  https://earth-info.nga.mil/index.php?dir=wgs84&action=wgs84#tab_egm2008"; \
			echo "  Then convert PGM to NetCDF using GDAL or GMT"; \
			exit 1; \
		fi; \
	fi

bathy-download-all: bathy-setup bathy-download-dtu-mss bathy-download-gebco geoid-download-egm2008 ## Download all bathymetry and geoid data

bathy-check: ## Check downloaded bathymetry files
	@echo "Checking bathymetry data files in $(BATHY_DIR)..."
	@if [ ! -d "$(BATHY_DIR)" ]; then \
		echo "Bathymetry directory not found: $(BATHY_DIR)"; \
		echo "Run 'make bathy-setup' first."; \
		exit 1; \
	fi
	@echo ""
	@echo "NetCDF files found:"
	@find $(BATHY_DIR) -name "*.nc" -type f | while read file; do \
		echo "  - $$(basename $$file) ($$(du -h $$file | cut -f1))"; \
	done || echo "  No .nc files found"
	@echo ""
	@if [ -f "$(BATHY_DIR)/$(GEBCO_FILE)" ]; then \
		echo "✓ GEBCO bathymetry data found"; \
	else \
		echo "✗ GEBCO bathymetry data not found"; \
		echo "  Download from: https://www.gebco.net/"; \
	fi
	@if [ -f "$(BATHY_DIR)/$(DTU_MSS_FILE)" ]; then \
		echo "✓ DTU MSS data found"; \
	else \
		echo "✗ DTU MSS data not found"; \
		echo "  Run 'make bathy-download-dtu-mss'"; \
	fi

bathy-clean: ## Remove downloaded bathymetry files
	@echo "WARNING: This will delete all bathymetry files in $(BATHY_DIR)/"
	@read -p "Are you sure? [y/N] " confirm; \
	if [ "$$confirm" = "y" ] || [ "$$confirm" = "Y" ]; then \
		rm -rf $(BATHY_DIR)/*.nc; \
		echo "Bathymetry files removed."; \
	else \
		echo "Cancelled."; \
	fi

# GCS Bathymetry data targets
gcs-create-bathy-bucket: ## Create GCS bucket for bathymetry data
	@echo "Creating Cloud Storage bucket for bathymetry data..."
	@if [ -z "$(PROJECT_ID)" ]; then \
		echo "Error: PROJECT_ID not set. Run 'gcloud config set project YOUR_PROJECT_ID'"; \
		exit 1; \
	fi
	@gsutil ls -b gs://$(GCS_BATHY_BUCKET) 2>/dev/null || \
	(gsutil mb -l $(REGION) -b on gs://$(GCS_BATHY_BUCKET) && \
	 echo "Bucket created: gs://$(GCS_BATHY_BUCKET)")
	@echo "Storage cost estimate: ~¥150/month for 7GB in $(REGION)"

gcs-upload-bathy: ## Upload bathymetry data to Cloud Storage
	@echo "Uploading bathymetry data to Cloud Storage..."
	@if [ ! -d "$(BATHY_DIR)" ]; then \
		echo "Error: Bathymetry directory not found: $(BATHY_DIR)"; \
		echo "Run 'make bathy-download-all' first."; \
		exit 1; \
	fi
	@echo "Checking bucket exists..."
	@gsutil ls -b gs://$(GCS_BATHY_BUCKET) >/dev/null 2>&1 || \
	(echo "Error: Bucket gs://$(GCS_BATHY_BUCKET) not found. Run 'make gcs-create-bathy-bucket' first." && exit 1)
	@echo "Starting upload (this may take 10-20 minutes for large files)..."
	@gsutil -m rsync -r -d $(BATHY_DIR) gs://$(GCS_BATHY_BUCKET)/
	@echo ""
	@echo "Upload complete!"
	@echo "Bucket: gs://$(GCS_BATHY_BUCKET)"
	@echo "Files uploaded:"
	@gsutil du -sh gs://$(GCS_BATHY_BUCKET)
	@echo ""
	@echo "Monthly cost estimate: ~¥150 (Standard Storage in $(REGION))"
	@echo ""
	@echo "To use with Cloud Run, mount as volume:"
	@echo "  --add-volume name=bathymetry,type=cloud-storage,bucket=$(GCS_BATHY_BUCKET),readonly=true"
	@echo "  --add-volume-mount volume=bathymetry,mount-path=/mnt/bathymetry"
	@echo "  --set-env-vars BATHYMETRY_GEBCO_PATH=/mnt/bathymetry/$(GEBCO_FILE)"
	@echo "  --set-env-vars BATHYMETRY_MSS_PATH=/mnt/bathymetry/$(DTU_MSS_FILE)"

gcs-download-bathy: ## Download bathymetry data from Cloud Storage
	@echo "Downloading bathymetry data from Cloud Storage..."
	@mkdir -p $(BATHY_DIR)
	@gsutil -m rsync -r gs://$(GCS_BATHY_BUCKET)/ $(BATHY_DIR)/
	@echo "Download complete: $(BATHY_DIR)/"

gcs-list-bathy: ## List bathymetry files in Cloud Storage
	@echo "Listing bathymetry files in gs://$(GCS_BATHY_BUCKET)..."
	@gsutil ls -lh gs://$(GCS_BATHY_BUCKET)/

gcs-check-bathy: ## Check bathymetry data in Cloud Storage
	@echo "Checking bathymetry data in Cloud Storage..."
	@echo "Bucket: gs://$(GCS_BATHY_BUCKET)"
	@echo "Region: $(REGION)"
	@echo ""
	@gsutil du -sh gs://$(GCS_BATHY_BUCKET)
	@echo ""
	@echo "Files:"
	@gsutil ls gs://$(GCS_BATHY_BUCKET)/*.nc 2>/dev/null | wc -l | xargs -I {} echo "  NetCDF files: {}"
	@echo ""
	@echo "Monthly cost estimate: ~¥150 (Standard Storage)"

.PHONY: install all curl-health curl-constituents curl-tokyo curl-tokyo-extrema
.PHONY: fes-setup fes-list fes-download-ocean-tide fes-download-major fes-download-all
.PHONY: fes-check fes-clean fes-mock fes-mock-fast fes-mock-custom
.PHONY: gcs-check-project gcs-create-bucket gcs-upload-fes gcs-download-fes gcs-list-fes gcs-check-fes gcs-delete-bucket
.PHONY: bathy-setup bathy-download-gebco bathy-download-dtu-mss geoid-download-egm2008 bathy-download-all bathy-check bathy-clean
.PHONY: gcs-create-bathy-bucket gcs-upload-bathy gcs-download-bathy gcs-list-bathy gcs-check-bathy
