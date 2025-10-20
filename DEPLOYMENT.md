# Deployment Guide - Cloud Run

Complete guide for deploying the Tide API to Google Cloud Run with cost optimization.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Quick Deployment](#quick-deployment)
3. [Cost Optimization](#cost-optimization)
4. [Configuration](#configuration)
5. [Monitoring](#monitoring)
6. [Troubleshooting](#troubleshooting)

## Prerequisites

### 1. Google Cloud Setup

```bash
# Install gcloud CLI (if not already installed)
# macOS:
brew install google-cloud-sdk

# Linux:
curl https://sdk.cloud.google.com | bash

# Login to Google Cloud
gcloud auth login

# Set your project
gcloud config set project YOUR_PROJECT_ID

# Enable required APIs
gcloud services enable \
  run.googleapis.com \
  cloudbuild.googleapis.com \
  artifactregistry.googleapis.com \
  storage-api.googleapis.com
```

### 2. Create Artifact Registry Repository

```bash
# Create repository for Docker images
gcloud artifacts repositories create tides-api \
  --repository-format=docker \
  --location=asia-northeast1 \
  --description="Tide API container images"

# Configure Docker authentication
gcloud auth configure-docker asia-northeast1-docker.pkg.dev
```

### 3. Create GCS Bucket for FES Data

```bash
# Create bucket (replace YOUR_PROJECT_ID)
gsutil mb -l asia-northeast1 gs://YOUR_PROJECT_ID-fes-data

# Upload mock FES data
gsutil -m cp -r ./data/fes/* gs://YOUR_PROJECT_ID-fes-data/
```

## Quick Deployment

### Option 1: Deploy with Mock FES Data (Recommended for Testing)

```bash
# 1. Build and tag Docker image
docker build -t asia-northeast1-docker.pkg.dev/YOUR_PROJECT_ID/tides-api/tides-api:latest .

# 2. Push to Artifact Registry
docker push asia-northeast1-docker.pkg.dev/YOUR_PROJECT_ID/tides-api/tides-api:latest

# 3. Deploy to Cloud Run (cost-optimized configuration)
gcloud run deploy tides-api \
  --image asia-northeast1-docker.pkg.dev/YOUR_PROJECT_ID/tides-api/tides-api:latest \
  --region asia-northeast1 \
  --platform managed \
  --allow-unauthenticated \
  --min-instances 0 \
  --max-instances 10 \
  --memory 512Mi \
  --cpu 1 \
  --timeout 60s \
  --concurrency 80 \
  --set-env-vars "PORT=8080,DATA_DIR=/app/data,FES_DIR=/app/data/fes,TZ=Asia/Tokyo"
```

### Option 2: Deploy with GCS-backed FES Data (Production)

```bash
# Deploy with GCS mount (requires Cloud Run v2 API)
gcloud run deploy tides-api \
  --image asia-northeast1-docker.pkg.dev/YOUR_PROJECT_ID/tides-api/tides-api:latest \
  --region asia-northeast1 \
  --platform managed \
  --allow-unauthenticated \
  --min-instances 0 \
  --max-instances 10 \
  --memory 1Gi \
  --cpu 1 \
  --timeout 60s \
  --concurrency 80 \
  --set-env-vars "PORT=8080,DATA_DIR=/app/data,FES_DIR=/gcs/fes,TZ=Asia/Tokyo" \
  --add-volume name=fes-data,type=cloud-storage,bucket=YOUR_PROJECT_ID-fes-data \
  --add-volume-mount volume=fes-data,mount-path=/gcs/fes
```

## Cost Optimization

### Pricing Breakdown (as of 2025)

**Monthly costs for asia-northeast1:**

| Component | Configuration | Monthly Cost |
|-----------|---------------|--------------|
| Cloud Run (min=0) | 100 req/day, 50ms avg | $0.05 - $0.50 |
| Cloud Run (min=1) | 1 instance always on | $69.00 |
| GCS Storage | 11.5MB mock FES | $0.0003 |
| GCS Storage | 720MB real FES | $0.018 |
| GCS Egress | 1GB/month | $0.12 |
| Artifact Registry | 1 image (~500MB) | $0.10 |

**Recommended Configurations:**

1. **Development/Low Traffic** (min-instances=0)
   - Total: ~$0.30/month
   - Cold start: 1-3 seconds
   - Best for: Testing, personal projects

2. **Production/High Traffic** (min-instances=1)
   - Total: ~$69/month
   - No cold starts
   - Best for: Public APIs, SLA requirements

3. **Hybrid** (min-instances=0 with Cloud Scheduler warmup)
   - Total: ~$1/month
   - Cold starts during business hours: 0
   - Best for: Business hour traffic patterns

### Hybrid Configuration (Recommended)

```bash
# Deploy with min-instances=0
gcloud run deploy tides-api \
  --image asia-northeast1-docker.pkg.dev/YOUR_PROJECT_ID/tides-api/tides-api:latest \
  --region asia-northeast1 \
  --min-instances 0 \
  --max-instances 10 \
  # ... other flags

# Create Cloud Scheduler job to keep warm during business hours
# Runs every 5 minutes, 9 AM - 6 PM JST, weekdays
gcloud scheduler jobs create http tides-api-warmup \
  --location asia-northeast1 \
  --schedule "*/5 9-18 * * 1-5" \
  --time-zone "Asia/Tokyo" \
  --uri "https://YOUR_SERVICE_URL/healthz" \
  --http-method GET

# Cost: ~$0.10/month (5000 free invocations/month, then $0.10 per million)
```

### Regional FES Data (Cost Optimization)

The Go-based FES generator creates regional datasets that are 98% smaller:

```bash
# Generate Japan-only FES data (11.5MB vs 720MB global)
make fes-mock

# Upload to GCS
gsutil -m cp -r ./data/fes/* gs://YOUR_PROJECT_ID-fes-data/

# Monthly storage cost: $0.0003 vs $0.018 (94% savings)
```

## Configuration

### Environment Variables

| Variable | Default | Description | Cloud Run Example |
|----------|---------|-------------|-------------------|
| `PORT` | `8080` | Server port | Set by Cloud Run |
| `DATA_DIR` | `./data` | CSV data directory | `/app/data` |
| `FES_DIR` | `./data/fes` | FES NetCDF directory | `/gcs/fes` or `/app/data/fes` |
| `TZ` | `Asia/Tokyo` | Display timezone | `Asia/Tokyo`, `UTC` |

### Dockerfile Optimization

The current Dockerfile is already optimized for Cloud Run:

```dockerfile
# Multi-stage build
FROM golang:1.22-alpine AS builder
# ... build stage (installs netcdf, builds binary)

FROM alpine:latest
# ... runtime stage (only runtime dependencies)
```

**Image size:** ~100MB (vs 1GB+ with full Go toolchain)

### Memory and CPU Recommendations

| FES Data Size | Memory | CPU | Max Concurrent | Cold Start |
|---------------|--------|-----|----------------|------------|
| Mock (11.5MB) | 512Mi | 1 | 80 | 1-2s |
| Real 8 constituents (720MB) | 1Gi | 1 | 50 | 5-10s |
| Real 34 constituents (3GB) | 2Gi | 2 | 30 | 15-20s |

## Monitoring

### Cloud Logging

```bash
# View recent logs
gcloud run services logs read tides-api \
  --region asia-northeast1 \
  --limit 50

# Follow logs in real-time
gcloud run services logs tail tides-api \
  --region asia-northeast1

# Filter for errors
gcloud run services logs read tides-api \
  --region asia-northeast1 \
  --filter "severity>=ERROR" \
  --limit 100
```

### Cloud Monitoring

**Key Metrics to Monitor:**

1. **Request Count** - Track traffic patterns
2. **Request Latency** - P50, P95, P99 response times
3. **Container Instance Count** - Scale behavior
4. **Memory Utilization** - Detect memory leaks
5. **Cold Start Count** - If using min-instances=0

**Create Alert Policy:**

```bash
# Alert if P95 latency > 500ms
gcloud alpha monitoring policies create \
  --notification-channels=YOUR_CHANNEL_ID \
  --display-name="Tide API High Latency" \
  --condition-display-name="P95 latency > 500ms" \
  --condition-threshold-value=0.5 \
  --condition-threshold-duration=60s
```

### Custom Metrics (Future Enhancement)

Add Prometheus metrics to track:
- FES cache hit/miss ratio
- Constituent load times
- Grid interpolation performance

## Troubleshooting

### Issue 1: Cold Start Timeouts

**Symptom:** First request after idle period times out

**Solutions:**

1. **Increase timeout:**
```bash
gcloud run services update tides-api \
  --region asia-northeast1 \
  --timeout 120s
```

2. **Use min-instances=1:**
```bash
gcloud run services update tides-api \
  --region asia-northeast1 \
  --min-instances 1
```

3. **Pre-warm with Cloud Scheduler** (see Hybrid Configuration above)

### Issue 2: Out of Memory

**Symptom:** Container killed with exit code 137

**Solutions:**

1. **Increase memory allocation:**
```bash
gcloud run services update tides-api \
  --region asia-northeast1 \
  --memory 2Gi
```

2. **Use regional FES data instead of global** (reduce from 3GB to 11.5MB)

3. **Implement lazy loading** of constituents (future enhancement)

### Issue 3: FES Data Not Found

**Symptom:** API returns "FES data directory does not exist"

**Solutions:**

1. **Verify GCS bucket mount:**
```bash
# Check deployment configuration
gcloud run services describe tides-api \
  --region asia-northeast1 \
  --format "value(spec.template.spec.volumes)"
```

2. **Upload FES data to GCS:**
```bash
gsutil ls gs://YOUR_PROJECT_ID-fes-data/
# Should show: m2_amplitude.nc, m2_phase.nc, s2_amplitude.nc, etc.
```

3. **Use embedded FES data in container** (for small datasets):
```dockerfile
# In Dockerfile, before final COPY
COPY data/fes /app/data/fes
```

### Issue 4: NetCDF Library Missing

**Symptom:** Container fails to start with "libnetcdf.so not found"

**Solution:** Already handled in Dockerfile:
```dockerfile
RUN apk add --no-cache netcdf
```

If issue persists, rebuild image:
```bash
docker build --no-cache -t asia-northeast1-docker.pkg.dev/YOUR_PROJECT_ID/tides-api/tides-api:latest .
```

### Issue 5: 403 Forbidden on Public Access

**Symptom:** API returns 403 when accessed from browser

**Solution:** Allow unauthenticated access:
```bash
gcloud run services add-iam-policy-binding tides-api \
  --region asia-northeast1 \
  --member "allUsers" \
  --role "roles/run.invoker"
```

## Advanced Configurations

### Custom Domain

```bash
# Map custom domain
gcloud run domain-mappings create \
  --service tides-api \
  --domain api.yourdomain.com \
  --region asia-northeast1

# Update DNS records (shown in output)
```

### CDN with Cloud Load Balancer

For caching tide predictions:

```bash
# Create serverless NEG
gcloud compute network-endpoint-groups create tides-api-neg \
  --region asia-northeast1 \
  --network-endpoint-type serverless \
  --cloud-run-service tides-api

# Create backend service with CDN enabled
gcloud compute backend-services create tides-api-backend \
  --global \
  --enable-cdn \
  --cache-mode CACHE_ALL_STATIC

# Configure cache TTL
gcloud compute backend-services update tides-api-backend \
  --global \
  --default-ttl 3600 \
  --max-ttl 86400 \
  --client-ttl 3600
```

### Multi-Region Deployment

For global availability:

```bash
# Deploy to multiple regions
for region in asia-northeast1 us-central1 europe-west1; do
  gcloud run deploy tides-api \
    --image asia-northeast1-docker.pkg.dev/YOUR_PROJECT_ID/tides-api/tides-api:latest \
    --region $region \
    # ... other flags
done

# Use Cloud Load Balancer for global routing
```

## CI/CD Pipeline

### Cloud Build Configuration

Create `cloudbuild.yaml`:

```yaml
steps:
  # Build Docker image
  - name: 'gcr.io/cloud-builders/docker'
    args:
      - 'build'
      - '-t'
      - 'asia-northeast1-docker.pkg.dev/${PROJECT_ID}/tides-api/tides-api:${COMMIT_SHA}'
      - '-t'
      - 'asia-northeast1-docker.pkg.dev/${PROJECT_ID}/tides-api/tides-api:latest'
      - '.'

  # Push to Artifact Registry
  - name: 'gcr.io/cloud-builders/docker'
    args:
      - 'push'
      - '--all-tags'
      - 'asia-northeast1-docker.pkg.dev/${PROJECT_ID}/tides-api/tides-api'

  # Deploy to Cloud Run
  - name: 'gcr.io/google.com/cloudsdktool/cloud-sdk'
    entrypoint: gcloud
    args:
      - 'run'
      - 'deploy'
      - 'tides-api'
      - '--image'
      - 'asia-northeast1-docker.pkg.dev/${PROJECT_ID}/tides-api/tides-api:${COMMIT_SHA}'
      - '--region'
      - 'asia-northeast1'
      - '--platform'
      - 'managed'

images:
  - 'asia-northeast1-docker.pkg.dev/${PROJECT_ID}/tides-api/tides-api:${COMMIT_SHA}'
  - 'asia-northeast1-docker.pkg.dev/${PROJECT_ID}/tides-api/tides-api:latest'
```

### Trigger on Git Push

```bash
# Create Cloud Build trigger
gcloud builds triggers create github \
  --repo-name=tides-api \
  --repo-owner=YOUR_GITHUB_USERNAME \
  --branch-pattern="^main$" \
  --build-config=cloudbuild.yaml
```

## Security Best Practices

### 1. Use Secret Manager for AVISO Credentials

```bash
# Create secret
echo -n "YOUR_AVISO_USERNAME" | gcloud secrets create aviso-username --data-file=-
echo -n "YOUR_AVISO_PASSWORD" | gcloud secrets create aviso-password --data-file=-

# Grant Cloud Run access
gcloud secrets add-iam-policy-binding aviso-username \
  --member="serviceAccount:YOUR_PROJECT_NUMBER-compute@developer.gserviceaccount.com" \
  --role="roles/secretmanager.secretAccessor"

# Mount secrets in Cloud Run
gcloud run services update tides-api \
  --region asia-northeast1 \
  --update-secrets AVISO_USER=aviso-username:latest \
  --update-secrets AVISO_PASS=aviso-password:latest
```

### 2. Enable VPC Connector (Private GCS Access)

```bash
# Create VPC connector
gcloud compute networks vpc-access connectors create tides-api-connector \
  --region asia-northeast1 \
  --range 10.8.0.0/28

# Use connector in Cloud Run
gcloud run services update tides-api \
  --region asia-northeast1 \
  --vpc-connector tides-api-connector \
  --vpc-egress private-ranges-only
```

### 3. Rate Limiting with Cloud Armor

For DDoS protection (requires Cloud Load Balancer):

```bash
# Create security policy
gcloud compute security-policies create tides-api-policy \
  --description "Rate limiting for Tide API"

# Add rate limiting rule
gcloud compute security-policies rules create 1000 \
  --security-policy tides-api-policy \
  --expression "true" \
  --action "rate-based-ban" \
  --rate-limit-threshold-count 100 \
  --rate-limit-threshold-interval-sec 60 \
  --ban-duration-sec 600
```

## Cost Calculator

Use this formula to estimate your monthly costs:

```
Total Cost = Cloud Run + GCS Storage + GCS Egress + Artifact Registry

Where:
  Cloud Run = (if min=0) $0.05-0.50 OR (if min=1) $69.00
  GCS Storage = FES_SIZE_GB * 0.023
  GCS Egress = REQUESTS * AVG_RESPONSE_KB * 0.00012
  Artifact Registry = IMAGE_SIZE_GB * 0.20

Example (min=0, 11.5MB FES, 1000 req/month):
  = $0.30 + (0.0115 * 0.023) + (1000 * 5 * 0.00012) + (0.5 * 0.20)
  = $0.30 + $0.0003 + $0.60 + $0.10
  = $1.00/month
```

## Production Checklist

- [ ] Enable Cloud Monitoring alerts
- [ ] Configure Cloud Logging retention
- [ ] Set up custom domain with SSL
- [ ] Implement API key authentication (if needed)
- [ ] Enable Cloud Armor rate limiting
- [ ] Configure Cloud CDN for caching
- [ ] Set up Cloud Build CI/CD pipeline
- [ ] Create staging environment
- [ ] Document API for users (Swagger/OpenAPI)
- [ ] Test failover and disaster recovery
- [ ] Configure backup for GCS bucket
- [ ] Review IAM permissions (least privilege)

## Next Steps

1. **Deploy to staging:**
   ```bash
   gcloud run deploy tides-api-staging \
     --image asia-northeast1-docker.pkg.dev/YOUR_PROJECT_ID/tides-api/tides-api:latest \
     --region asia-northeast1 \
     --min-instances 0
   ```

2. **Test API:**
   ```bash
   SERVICE_URL=$(gcloud run services describe tides-api-staging --region asia-northeast1 --format 'value(status.url)')
   curl "${SERVICE_URL}/healthz"
   curl "${SERVICE_URL}/v1/constituents"
   curl "${SERVICE_URL}/v1/tides/predictions?lat=35.6762&lon=139.6503&start=2025-10-21T00:00:00Z&end=2025-10-21T12:00:00Z&interval=10m"
   ```

3. **Promote to production:**
   ```bash
   gcloud run services update-traffic tides-api \
     --region asia-northeast1 \
     --to-latest
   ```

## Support

For issues or questions:
- Check [TROUBLESHOOTING.md](TROUBLESHOOTING.md)
- Review Cloud Run logs: `gcloud run services logs read tides-api`
- Create GitHub issue: [github.com/ngs/tides-api/issues](https://github.com/ngs/tides-api/issues)

---

**Last Updated:** 2025-10-21
**Cloud Run API Version:** v2
**Region:** asia-northeast1 (Tokyo)
