# CI/CD Setup Guide

Complete guide for setting up Continuous Integration and Continuous Deployment for Tide API using GitHub Actions and Google Cloud Run.

## Table of Contents

1. [Overview](#overview)
2. [Prerequisites](#prerequisites)
3. [Initial Setup](#initial-setup)
4. [GitHub Actions Workflows](#github-actions-workflows)
5. [Deployment Methods](#deployment-methods)
6. [Testing](#testing)
7. [Troubleshooting](#troubleshooting)

## Overview

The Tide API uses a modern CI/CD pipeline based on the jplaw2epub-web-api architecture:

- **CI Pipeline** (`ci.yml`): Runs tests, builds, and linting on every push
- **Deploy Pipeline** (`deploy.yml`): Automated deployment to Cloud Run on master/main branch
- **Simple Deploy** (`deploy-simple.yml`): First-time setup with fallback authentication

### Key Features

- Workload Identity Federation (WIF) for secure authentication
- Multiple deployment methods (source, artifact-registry, cloud-build)
- Automated testing of deployed endpoints
- Docker multi-stage builds with NetCDF support
- Cost-optimized Cloud Run configuration

## Prerequisites

### Local Requirements

```bash
# Install gcloud CLI
brew install google-cloud-sdk  # macOS
# Or see: https://cloud.google.com/sdk/docs/install

# Install required tools
brew install jq  # JSON processing

# Verify installations
gcloud --version
jq --version
```

### Google Cloud Project

```bash
# Create project (if needed)
gcloud projects create YOUR_PROJECT_ID --name="Tide API"

# Set project
gcloud config set project YOUR_PROJECT_ID

# Enable billing (required for Cloud Run)
# Visit: https://console.cloud.google.com/billing
```

### GitHub Repository

1. Fork or clone the repository
2. Enable GitHub Actions in repository settings
3. Prepare to add required secrets

## Initial Setup

### Step 1: Run GCP Setup Script

The `scripts/gcp-setup.sh` script automates all GCP infrastructure setup:

```bash
# Complete setup (recommended)
PROJECT_ID=your-project-id ./scripts/gcp-setup.sh all

# This will:
# - Enable required Google Cloud APIs
# - Create Workload Identity Pool and Provider
# - Create GitHub Actions service account
# - Configure IAM permissions
# - Create Artifact Registry repository
```

**What it creates:**

- Workload Identity Pool: `github-pool`
- OIDC Provider: `github-provider`
- Service Account: `github-actions-sa@YOUR_PROJECT_ID.iam.gserviceaccount.com`
- Artifact Registry: `cloud-run-source-deploy`

### Step 2: Add GitHub Secrets

After running the setup script, add these secrets to your GitHub repository:

**Repository Settings → Secrets and variables → Actions → New repository secret**

Required secrets:

```bash
# From gcp-setup.sh output
WIF_PROVIDER=projects/123456789/locations/global/workloadIdentityPools/github-pool/providers/github-provider
WIF_SERVICE_ACCOUNT=github-actions-sa@YOUR_PROJECT_ID.iam.gserviceaccount.com
PROJECT_ID=your-project-id

# Optional (for Codecov)
CODECOV_TOKEN=your-codecov-token
```

### Step 3: Verify Setup

```bash
# Check configuration status
PROJECT_ID=your-project-id ./scripts/gcp-setup.sh status
```

Expected output:
- All required APIs enabled
- WIF Pool exists
- GitHub Actions SA exists
- Artifact Registry repository exists

## GitHub Actions Workflows

### CI Workflow (`.github/workflows/ci.yml`)

**Triggers:**
- Every push to any branch
- Pull requests

**Jobs:**

1. **Test**
   - Installs NetCDF library
   - Runs tests with race detection
   - Uploads coverage to Codecov

2. **Build**
   - Compiles Go binary
   - Verifies build succeeds

3. **Docker**
   - Builds Docker image
   - Starts container
   - Tests all API endpoints
   - Verifies both CSV and FES sources

4. **Lint**
   - Runs golangci-lint

**Example run:**

```bash
# Triggered automatically on git push
git add .
git commit -m "Update API handler"
git push origin feature-branch
```

### Deploy Workflow (`.github/workflows/deploy.yml`)

**Triggers:**
- Push to `master` or `main` branch
- Manual trigger with deployment method choice

**Steps:**

1. Authenticate with Workload Identity Federation
2. Enable required GCP APIs
3. Deploy using selected method
4. Wait for service to be ready
5. Test health, constituents, and predictions endpoints
6. Display deployment summary

**Manual deployment:**

```bash
# Via GitHub UI:
# Actions → Deploy to Cloud Run → Run workflow
# Select deployment method: source, artifact-registry, or cloud-build
```

**Deployment methods:**

| Method | Description | Use Case |
|--------|-------------|----------|
| `source` | Direct source deployment | Fastest, recommended for most cases |
| `artifact-registry` | Build image → Push → Deploy | Better caching, versioning |
| `cloud-build` | Uses cloudbuild.yaml | Advanced CI/CD pipelines |

### Simple Deploy Workflow (`.github/workflows/deploy-simple.yml`)

**Purpose:** First-time deployment when WIF might not be fully configured

**Features:**
- Tries WIF authentication first
- Falls back to service account key
- Enables all required APIs
- Creates Artifact Registry repository
- Configures public access

**When to use:**
- Initial deployment before WIF is set up
- Testing deployment without full setup
- Emergency deployments

**Usage:**

```bash
# Via GitHub UI:
# Actions → Deploy (Simple - First Time Setup) → Run workflow
# Select deployment method: source or artifact-registry
```

## Deployment Methods

### Method 1: Source Deployment (Recommended)

Deploys directly from source code. Cloud Build handles the build.

**Pros:**
- Fastest deployment
- No manual image management
- Automatic caching

**Cons:**
- Less control over build process

**Command:**

```bash
# Via GitHub Actions (automatic on push to main)
git push origin main

# Manual via gcloud
gcloud run deploy tides-api \
  --source . \
  --region asia-northeast1 \
  --project YOUR_PROJECT_ID
```

### Method 2: Artifact Registry

Builds Docker image, pushes to registry, then deploys.

**Pros:**
- Image versioning with tags
- Better for rollbacks
- Explicit image management

**Cons:**
- Slightly slower
- Requires Artifact Registry setup

**Command:**

```bash
# Build and push
gcloud builds submit \
  --tag asia-northeast1-docker.pkg.dev/YOUR_PROJECT_ID/cloud-run-source-deploy/tides-api:latest

# Deploy
gcloud run deploy tides-api \
  --image asia-northeast1-docker.pkg.dev/YOUR_PROJECT_ID/cloud-run-source-deploy/tides-api:latest \
  --region asia-northeast1
```

### Method 3: Cloud Build (cloudbuild.yaml)

Uses custom Cloud Build configuration for complex pipelines.

**Pros:**
- Full control over build steps
- Custom testing
- Multi-stage deployments

**Cons:**
- Most complex
- Longer build times

**Command:**

```bash
gcloud builds submit \
  --config cloudbuild.yaml \
  --substitutions _REGION=asia-northeast1,_SERVICE_NAME=tides-api
```

## Testing

### Local Testing

```bash
# Build Docker image
docker build -t tides-api:test .

# Run container
docker run -d -p 8080:8080 --name tides-api-test tides-api:test

# Test endpoints
curl http://localhost:8080/healthz
curl http://localhost:8080/v1/constituents
curl 'http://localhost:8080/v1/tides/predictions?station_id=tokyo&start=2025-10-21T00:00:00Z&end=2025-10-21T12:00:00Z&interval=10m'

# Stop container
docker stop tides-api-test
docker rm tides-api-test
```

### CI Testing

Tests run automatically on every push:

```bash
# View CI results
# GitHub → Actions → CI workflow

# Test locally before pushing
make test          # Run all tests
make docker-build  # Build Docker image
make docker-run    # Run container
```

### Deployment Testing

After deployment, workflows automatically test:

1. **Health Endpoint**
   ```bash
   curl https://tides-api-HASH-an.a.run.app/healthz
   ```

2. **Constituents Endpoint**
   ```bash
   curl https://tides-api-HASH-an.a.run.app/v1/constituents
   ```

3. **Predictions (CSV)**
   ```bash
   curl 'https://tides-api-HASH-an.a.run.app/v1/tides/predictions?station_id=tokyo&start=2025-10-21T00:00:00Z&end=2025-10-21T03:00:00Z&interval=30m'
   ```

4. **Predictions (FES)**
   ```bash
   curl 'https://tides-api-HASH-an.a.run.app/v1/tides/predictions?lat=35.6762&lon=139.6503&start=2025-10-21T00:00:00Z&end=2025-10-21T03:00:00Z&interval=30m'
   ```

## Troubleshooting

### Issue 1: Workload Identity Federation Errors

**Error:**
```
Error: google-github-actions/auth failed with: retry function failed after 3 attempts
```

**Solutions:**

1. **Verify WIF configuration:**
   ```bash
   PROJECT_ID=your-project-id ./scripts/gcp-setup.sh status
   ```

2. **Check GitHub secrets:**
   - `WIF_PROVIDER` format: `projects/NUMBER/locations/global/workloadIdentityPools/POOL/providers/PROVIDER`
   - `WIF_SERVICE_ACCOUNT` format: `SA_NAME@PROJECT_ID.iam.gserviceaccount.com`

3. **Verify IAM binding:**
   ```bash
   gcloud iam service-accounts get-iam-policy github-actions-sa@YOUR_PROJECT_ID.iam.gserviceaccount.com
   ```

4. **Re-run setup:**
   ```bash
   PROJECT_ID=your-project-id ./scripts/gcp-setup.sh wif
   ```

### Issue 2: Build Fails - NetCDF Library Not Found

**Error:**
```
Package netcdf was not found in the pkg-config search path
```

**Solutions:**

1. **Dockerfile includes NetCDF:**
   - Verify line 13 in Dockerfile: `netcdf \`
   - Rebuild without cache: `docker build --no-cache -t tides-api .`

2. **For CI (Ubuntu):**
   - CI workflow already includes: `sudo apt-get install -y libnetcdf-dev`

3. **For macOS local:**
   ```bash
   brew install netcdf
   ```

### Issue 3: Deployment Succeeds but Service Fails

**Error:**
```
Service failed to be ready within timeout
```

**Solutions:**

1. **Check Cloud Run logs:**
   ```bash
   gcloud run services logs read tides-api \
     --region asia-northeast1 \
     --limit 100
   ```

2. **Verify environment variables:**
   ```bash
   gcloud run services describe tides-api \
     --region asia-northeast1 \
     --format "value(spec.template.spec.containers[0].env)"
   ```

3. **Increase timeout:**
   - Edit `.github/workflows/deploy.yml`
   - Change `--timeout 60s` to `--timeout 120s`

4. **Check FES data:**
   - Mock FES files are included in Docker image
   - Verify `data/fes/*.nc` exists in repository

### Issue 4: Permission Denied Errors

**Error:**
```
ERROR: (gcloud.run.deploy) User [SA] does not have permission to access service [tides-api]
```

**Solutions:**

1. **Re-run permissions setup:**
   ```bash
   PROJECT_ID=your-project-id ./scripts/gcp-setup.sh permissions
   ```

2. **Verify service account roles:**
   ```bash
   gcloud projects get-iam-policy YOUR_PROJECT_ID \
     --flatten="bindings[].members" \
     --filter="bindings.members:serviceAccount:github-actions-sa@*"
   ```

3. **Required roles:**
   - `roles/run.developer`
   - `roles/iam.serviceAccountUser`
   - `roles/storage.admin`
   - `roles/artifactregistry.writer`
   - `roles/cloudbuild.builds.editor`

### Issue 5: First Deployment Fails

**Error:**
```
ERROR: (gcloud.run.services.create) PERMISSION_DENIED: Permission denied on resource project
```

**Solution:** Use deploy-simple workflow:

```bash
# Via GitHub UI:
# Actions → Deploy (Simple - First Time Setup) → Run workflow

# This will:
# - Enable all required APIs
# - Create Artifact Registry repository
# - Configure public access
```

### Issue 6: Docker Build Slow or Times Out

**Error:**
```
ERROR: build step 0 "gcr.io/cloud-builders/docker" failed: step exited with non-zero status: 1
```

**Solutions:**

1. **Increase build timeout:**
   - Edit `cloudbuild.yaml`
   - Change `timeout: '1200s'` to `'1800s'`

2. **Use larger machine type:**
   ```yaml
   options:
     machineType: 'E2_HIGHCPU_32'  # More powerful
   ```

3. **Optimize Dockerfile:**
   - Multi-stage build already implemented
   - Dependency caching already configured

## Advanced Configuration

### Custom Domain

```bash
# Setup domain mapping
DOMAIN=api.yourdomain.com PROJECT_ID=your-project-id ./scripts/gcp-setup.sh domain

# Update DNS records (shown in output)
```

### Enable Cloud Scheduler Warmup

Keep service warm during business hours:

```bash
gcloud scheduler jobs create http tides-api-warmup \
  --location asia-northeast1 \
  --schedule "*/5 9-18 * * 1-5" \
  --time-zone "Asia/Tokyo" \
  --uri "https://YOUR_SERVICE_URL/healthz" \
  --http-method GET
```

### Multiple Environments

Create staging and production:

```bash
# Staging
gcloud run deploy tides-api-staging \
  --source . \
  --region asia-northeast1 \
  --no-allow-unauthenticated  # Requires authentication

# Production
gcloud run deploy tides-api \
  --source . \
  --region asia-northeast1 \
  --allow-unauthenticated
```

## Security Best Practices

1. **Never commit secrets to git:**
   - Use `.env` (git-ignored)
   - Use GitHub Secrets for CI/CD
   - Use Secret Manager for production

2. **Use Workload Identity Federation:**
   - No service account keys needed
   - Short-lived tokens
   - Automatic rotation

3. **Least privilege IAM:**
   - Only grant necessary roles
   - Separate service accounts for different purposes

4. **Enable Cloud Armor** (for production):
   ```bash
   # Rate limiting, DDoS protection
   # See DEPLOYMENT.md for details
   ```

## Monitoring

### View Logs

```bash
# Real-time logs
gcloud run services logs tail tides-api --region asia-northeast1

# Recent logs
gcloud run services logs read tides-api --region asia-northeast1 --limit 100

# Error logs only
gcloud run services logs read tides-api \
  --region asia-northeast1 \
  --filter "severity>=ERROR"
```

### Metrics

View in Cloud Console:
- https://console.cloud.google.com/run/detail/asia-northeast1/tides-api/metrics

Key metrics:
- Request count
- Request latency (P50, P95, P99)
- Container instance count
- Memory utilization
- Cold start count

## Cost Optimization

Current configuration (min-instances=0):
- **Monthly cost:** ~$0.30-1.00
- **Included:** 2 million requests, 360,000 GB-seconds free

See [DEPLOYMENT.md](DEPLOYMENT.md) for detailed cost analysis.

## Next Steps

1. **Setup CI/CD:**
   ```bash
   PROJECT_ID=your-project-id ./scripts/gcp-setup.sh all
   ```

2. **Add GitHub secrets** (from setup output)

3. **Push to main branch:**
   ```bash
   git push origin main
   ```

4. **Verify deployment:**
   ```bash
   # Check GitHub Actions
   # Visit: https://github.com/YOUR_ORG/tides-api/actions
   ```

5. **Test deployed service:**
   ```bash
   SERVICE_URL=$(gcloud run services describe tides-api --region asia-northeast1 --format 'value(status.url)')
   curl $SERVICE_URL/healthz
   ```

## Support

- **Documentation:** [README.md](README.md), [DEPLOYMENT.md](DEPLOYMENT.md)
- **GCP Setup:** `./scripts/gcp-setup.sh help`
- **GitHub Issues:** [Create Issue](https://github.com/ngs/tides-api/issues)

---

**Last Updated:** 2025-10-21
**Architecture:** Based on jplaw2epub-web-api CI/CD pattern
**Region:** asia-northeast1 (Tokyo)
