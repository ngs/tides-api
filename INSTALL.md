# Installation Guide

Complete installation guide for the Tide API, including dependencies and FES integration.

## Quick Install (CSV Mock Data Only)

If you only need CSV-based predictions (no FES integration):

```bash
# Install Go 1.22+
# On macOS:
brew install go

# Clone and setup
cd tide-api
go mod download
make run
```

✅ This works immediately without NetCDF dependencies.

---

## Full Install (with FES NetCDF Support)

For FES2014/2022 integration with lat/lon queries:

### macOS

```bash
# 1. Install NetCDF library
brew install netcdf

# 2. Install Go dependencies
go mod download

# 3. Build and test
make build
make run
```

### Ubuntu/Debian

```bash
# 1. Install NetCDF library and development headers
sudo apt-get update
sudo apt-get install -y libnetcdf-dev netcdf-bin pkg-config

# 2. Install Go 1.22+ (if not already installed)
wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# 3. Install Go dependencies
go mod download

# 4. Build and test
make build
make run
```

### CentOS/RHEL/Rocky Linux

```bash
# 1. Enable EPEL repository
sudo yum install -y epel-release

# 2. Install NetCDF library
sudo yum install -y netcdf-devel pkgconfig

# 3. Install Go 1.22+ (if not already installed)
wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# 4. Install Go dependencies
go mod download

# 5. Build and test
make build
make run
```

### Windows (WSL2 Recommended)

```powershell
# Option 1: Use WSL2 and follow Ubuntu instructions

# Option 2: Native Windows (Advanced)
# Install NetCDF via vcpkg or conda
conda install -c conda-forge netcdf4
# Then build with CGO_ENABLED=1
```

### Docker (No NetCDF Install Required)

```bash
# Build Docker image (NetCDF included in image)
make docker-build

# Run container
make docker-run

# Test
curl http://localhost:8080/healthz
```

---

## Verify Installation

### Test 1: Check NetCDF Library

```bash
# Should show version info
pkg-config --modversion netcdf

# Expected: 4.x.x (e.g., 4.9.0)
```

### Test 2: Build Go Binary

```bash
make build

# Should create ./tide-api binary
ls -lh tide-api
```

### Test 3: Run Tests

```bash
make test

# All tests should pass
```

### Test 4: Start Server

```bash
make run

# Should see:
# Starting Tide API server...
# Server listening on :8080
```

### Test 5: Test API

```bash
# In another terminal
curl http://localhost:8080/healthz

# Expected: {"status":"ok", "time":"..."}
```

---

## Troubleshooting

### Error: "Package netcdf was not found"

**Problem**: NetCDF C library not installed or not in pkg-config path.

**Solution**:

```bash
# macOS
brew install netcdf

# Ubuntu/Debian
sudo apt-get install libnetcdf-dev

# CentOS/RHEL
sudo yum install netcdf-devel

# Verify
pkg-config --modversion netcdf
```

### Error: "netcdf.h: No such file or directory"

**Problem**: NetCDF headers not found by CGO.

**Solution**:

```bash
# Find NetCDF installation
find /usr -name "netcdf.h" 2>/dev/null

# Set CGO flags manually
export CGO_CFLAGS="-I/usr/include"
export CGO_LDFLAGS="-L/usr/lib -lnetcdf"

# Rebuild
make build
```

### Error: "undefined reference to `nc_open`"

**Problem**: NetCDF library not linked correctly.

**Solution**:

```bash
# Check library location
ldconfig -p | grep netcdf

# Or on macOS
brew list netcdf | grep lib

# Set library path
export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH

# Rebuild
make clean build
```

### Build Works but FES Queries Fail

**Problem**: API built successfully but lat/lon queries return errors.

**Diagnosis**:

```bash
# Check if FES directory exists
ls -la data/fes/

# If empty, you need FES data
```

**Solution**:

```bash
# Option A: Download real FES data (requires AVISO account)
make fes-setup
make fes-download-major

# Option B: Generate mock data for testing
pip install numpy netCDF4
make fes-mock

# Then restart server
make run
```

### Performance Issues

**Problem**: First lat/lon request is very slow.

**Explanation**: This is expected. The API loads all NetCDF grids into memory on first request.

**Solutions**:

1. **Reduce Constituents**: Only download constituents you need
2. **Increase Memory**: Ensure server has adequate RAM
3. **Pre-warm Cache**: Add a startup routine to load grids
4. **Use Smaller Region**: Create regional extracts of FES data

---

## Development Setup

### Install Development Tools

```bash
# Go linter (optional but recommended)
brew install golangci-lint  # macOS
# or
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Hot reload (optional)
go install github.com/cosmtrek/air@latest

# NetCDF utilities (optional, for inspecting .nc files)
brew install netcdf  # includes ncdump, ncgen
```

### IDE Setup

#### VS Code

Install extensions:
- Go (by Go Team at Google)
- Even Better TOML
- Docker

Add to `.vscode/settings.json`:

```json
{
  "go.useLanguageServer": true,
  "go.lintTool": "golangci-lint",
  "go.lintOnSave": "package"
}
```

#### GoLand / IntelliJ IDEA

1. Open project
2. Enable Go modules support
3. Set GOROOT to Go 1.22+
4. Configure run configurations from Makefile targets

---

## Dependencies Overview

### Required

```
Go 1.22+              # Programming language
NetCDF C library      # For reading FES NetCDF files
```

### Go Packages (auto-installed via go mod)

```
github.com/gin-gonic/gin v1.11.0        # Web framework
github.com/fhs/go-netcdf v1.2.1         # NetCDF bindings
```

### Optional

```
lftp                  # For downloading FES data
Python 3 + numpy + netCDF4  # For generating mock FES files
jq                    # For pretty-printing JSON responses
```

---

## Platform-Specific Notes

### macOS (Apple Silicon M1/M2/M3)

NetCDF works out of the box with Homebrew:

```bash
# Install via Homebrew
brew install netcdf go

# Build for ARM64
make build

# Binary will be native ARM64
file tide-api
# tide-api: Mach-O 64-bit executable arm64
```

### Linux ARM (Raspberry Pi, AWS Graviton)

```bash
# Install NetCDF
sudo apt-get install libnetcdf-dev

# Build for ARM
GOARCH=arm64 make build
```

### Cloud Platforms

#### Google Cloud Run / Cloud Functions

Include NetCDF in Dockerfile:

```dockerfile
FROM golang:1.22-alpine AS builder
RUN apk add --no-cache netcdf-dev pkgconfig build-base
# ... rest of build
```

#### AWS Lambda

NetCDF integration requires custom runtime or layer:

```bash
# Build static binary with NetCDF
CGO_ENABLED=1 GOOS=linux go build -tags netgo -ldflags '-extldflags "-static"' ...
```

Consider using AWS Lambda layers for NetCDF library.

#### Kubernetes

Use the provided Dockerfile which includes NetCDF:

```bash
make docker-build
docker push your-registry/tide-api:latest
kubectl apply -f k8s/deployment.yaml
```

---

## Next Steps

After installation:

1. ✅ **Verify**: `make test` - All tests pass
2. 📝 **Configure**: Edit `.env` file
3. 🌊 **FES Data**: See [FES_SETUP.md](FES_SETUP.md) for downloading tidal data
4. 🚀 **Deploy**: Use Docker or binary deployment
5. 📚 **Learn**: Read [README.md](README.md) and [QUICKSTART.md](QUICKSTART.md)

---

## Minimal CSV-Only Deployment

If you want to avoid NetCDF dependencies entirely:

### Option 1: Build Tags (Not Implemented Yet)

Future version will support:

```bash
go build -tags "csv_only" ./cmd/server
```

### Option 2: Use CSV Store Only

Current workaround:

1. Only query with `station_id` (not lat/lon)
2. NetCDF code won't be executed
3. Server still requires NetCDF at build time

### Option 3: Docker

Use pre-built Docker image:

```bash
docker pull your-registry/tide-api:latest
docker run -p 8080:8080 tide-api:latest
```

---

## Support

- **NetCDF Issues**: https://www.unidata.ucar.edu/software/netcdf/
- **go-netcdf Issues**: https://github.com/fhs/go-netcdf
- **API Issues**: Check project GitHub Issues

**Last Updated**: 2025-10-21
