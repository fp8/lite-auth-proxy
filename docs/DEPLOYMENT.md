# Deployment Guide

This guide covers building, containerizing, and deploying lite-auth-proxy to production environments, with a focus on Google Cloud Run.

## Table of Contents

- [Build Variants](#build-variants)
- [Prerequisites](#prerequisites)
- [Local Docker Build](#local-docker-build)
- [Google Cloud Build](#google-cloud-build)
- [Container Registry Setup](#container-registry-setup)
- [Cloud Run Deployment](#cloud-run-deployment)
- [Configuration in Production](#configuration-in-production)
- [Monitoring & Logging](#monitoring--logging)
- [Security Best Practices](#security-best-practices)
- [Troubleshooting](#troubleshooting)

## Build Variants

The project ships two build variants:

| Image | Entry Point | Binary | Plugins | Use Case |
|-------|------------|--------|---------|----------|
| `flex-auth-proxy:X.Y.Z` | `cmd/flex` | `flex-auth-proxy` | all (ratelimit, admin, apikey, storage-firestore) | Full-featured proxy |
| `lite-auth-proxy:X.Y.Z` | `cmd/lite` | `lite-auth-proxy` | none | Minimal JWT-only proxy |

The **flex** build includes every plugin and behaves identically to pre-plugin versions. The **lite** build is a minimal JWT-validating reverse proxy with no rate limiting, admin API, API-key auth, or storage — ideal when you only need authentication.

### Building Both Variants

```bash
# Build both binaries
make build-all

# Build Docker images for both
make docker-build-all

# Or individually
make build-flex      # Flex build → ./bin/flex-auth-proxy
make build-lite      # Lite build → ./bin/lite-auth-proxy
make docker-build-flex  # Flex Docker image
make docker-build-lite  # Lite Docker image
```

### Custom Builds

You can create a custom build with only the plugins you need. See the [Plugin Guide](PLUGINS.md#creating-a-custom-build) for details.

### Choosing a Variant

| Scenario | Recommended Variant |
|----------|-------------------|
| JWT + rate limiting + admin API | Flex |
| JWT + API-key auth | Flex |
| Cross-instance rule sync (Firestore) | Flex |
| JWT auth only, minimal footprint | Lite |
| No rate limiting needed | Lite |

## Prerequisites

### Required Tools

- Docker (for local builds)
- gcloud CLI (for Google Cloud deployment)
- Make (for build automation)
- Git

### Required Google Cloud Setup

1. **Google Cloud Project** (set via GOOGLE_CLOUD_PROJECT) with billing enabled
2. **Required APIs** enabled:
   - Cloud Build API
   - Artifact Registry API
   - Cloud Run API
   - Secret Manager API (optional, for secrets)

3. **Service Account** with appropriate permissions:
   - Cloud Build Service Account
   - Cloud Run Admin
   - Artifact Registry Writer

### Environment Setup

Copy `.env.example` to `.env` and configure:

```bash
cp .env.example .env

# Edit with your values
export GOOGLE_CLOUD_PROJECT=your-project-id
export DOCKER_REGISTRY=europe-docker.pkg.dev
export DOCKER_PROJECT_ID=your-project-id
export DOCKER_REPO_NAME=docker
```

## Local Docker Build

### Build Image

```bash
# Source environment variables
source .env

# Build flex image (all plugins)
make docker-build-flex

# Build lite image (no plugins)
make docker-build-lite

# Or build manually
docker build \
  --build-arg VERSION=1.2.0 \
  -t flex-auth-proxy:local \
  -f Dockerfile.flex .

# Lite variant
docker build \
  --build-arg VERSION=1.2.0 \
  -t lite-auth-proxy:local \
  -f Dockerfile.lite .
```

### Test Locally

The container image defaults to `-config /config/config.toml` (set by Dockerfile `CMD`).

```bash
# Run flex container (all plugins)
docker run --rm -p 8888:8888 \
  -e GOOGLE_CLOUD_PROJECT=your-project \
  -e PROXY_SERVER_TARGET_URL=http://host.docker.internal:8080 \
  -e PROXY_AUTH_JWT_ENABLED=true \
  -e LOG_MODE=development \
  flex-auth-proxy:local

# Override config path if needed
docker run --rm -p 8888:8888 \
  -e PROXY_SERVER_TARGET_URL=http://host.docker.internal:8080 \
  flex-auth-proxy:local -config /path/to/custom-config.toml

# Test health check
curl http://localhost:8888/healthz

# Test with JWT
curl -H "Authorization: Bearer <YOUR_JWT>" \
  http://localhost:8888/api/endpoint
```

### Image Verification

```bash
# Check image size (should be < 15MB)
docker images flex-auth-proxy:local

# Inspect image layers
docker history flex-auth-proxy:local
```

## Google Cloud Build

### Setup Cloud Build

#### 1. Enable Required APIs

```bash
gcloud services enable \
  cloudbuild.googleapis.com \
  artifactregistry.googleapis.com \
  run.googleapis.com \
  secretmanager.googleapis.com \
  --project=$GOOGLE_CLOUD_PROJECT
```

#### 2. Create Artifact Registry Repository

```bash
gcloud artifacts repositories create docker \
  --repository-format=docker \
  --location=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT \
  --description="Docker images for flex-auth-proxy and lite-auth-proxy"
```

#### 3. Configure Docker Authentication

```bash
gcloud auth configure-docker europe-docker.pkg.dev
```

#### 4. Create Cloud Build Trigger (Optional)

For automated builds on git push:

```bash
gcloud builds triggers create github \
  --repo-name=lite-auth-proxy \
  --repo-owner=YOUR_GITHUB_ORG \
  --branch-pattern="^main$" \
  --build-config=cloudbuild.yaml \
  --name=lite-auth-proxy-main \
  --project=$GOOGLE_CLOUD_PROJECT
```

### Manual Cloud Build

Submit build manually:

```bash
# Using Make
source .env
make cloud-build

# Or using gcloud directly
gcloud builds submit \
  --config=cloudbuild.yaml \
  --project=$GOOGLE_CLOUD_PROJECT
```

### Cloud Build Configuration

The `cloudbuild.yaml` file defines the build process:

1. **Extract version** from `cmd/flex/main.go`
2. **Run tests** (unit tests only, fast validation)
3. **Build Docker image** with version tags
4. **Push to Artifact Registry** with full and major.minor tags

**Customization:**

You can override substitution variables:

```bash
gcloud builds submit \
  --config=cloudbuild.yaml \
  --substitutions=_DOCKER_REGISTRY=europe-docker.pkg.dev,_DOCKER_REPO_NAME=docker \
  --project=$GOOGLE_CLOUD_PROJECT
```

## Container Registry Setup

### Artifact Registry Permissions

Grant permissions to Cloud Build service account:

```bash
# Get Cloud Build service account
BUILD_SA=$(gcloud projects describe $GOOGLE_CLOUD_PROJECT \
  --format="value(projectNumber)")@cloudbuild.gserviceaccount.com

# Grant Artifact Registry Writer
gcloud artifacts repositories add-iam-policy-binding docker \
  --location=europe-west1 \
  --member=serviceAccount:$BUILD_SA \
  --role=roles/artifactregistry.writer \
  --project=$GOOGLE_CLOUD_PROJECT
```

### List Available Images

```bash
gcloud artifacts docker images list \
  europe-docker.pkg.dev/$GOOGLE_CLOUD_PROJECT/docker/flex-auth-proxy \
  --project=$GOOGLE_CLOUD_PROJECT
```

### Push Image Manually

```bash
# Tag image
docker tag flex-auth-proxy:local \
  europe-docker.pkg.dev/$GOOGLE_CLOUD_PROJECT/docker/flex-auth-proxy:1.0.0

# Push to registry
docker push europe-docker.pkg.dev/$GOOGLE_CLOUD_PROJECT/docker/flex-auth-proxy:1.0.0
```

## Cloud Run Deployment

### Basic Deployment

```bash
gcloud run deploy flex-auth-proxy \
  --image=europe-docker.pkg.dev/$GOOGLE_CLOUD_PROJECT/docker/flex-auth-proxy:1.0.0 \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT \
  --allow-unauthenticated \
  --port=8888 \
  --cpu=1 \
  --memory=128Mi \
  --min-instances=0 \
  --max-instances=10 \
  --concurrency=80
```

### Deployment with Environment Variables

```bash
gcloud run deploy flex-auth-proxy \
  --image=europe-docker.pkg.dev/$GOOGLE_CLOUD_PROJECT/docker/flex-auth-proxy:1.0.0 \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT \
  --set-env-vars GOOGLE_CLOUD_PROJECT=$GOOGLE_CLOUD_PROJECT,\
LOG_MODE=production,\
PROXY_SERVER_TARGET_URL=http://backend-service:8080,\
PROXY_AUTH_JWT_ENABLED=true,\
PROXY_AUTH_JWT_ISSUER=https://securetoken.google.com/$GOOGLE_CLOUD_PROJECT,\
PROXY_AUTH_JWT_AUDIENCE=$GOOGLE_CLOUD_PROJECT
```

### Deployment with Secrets

```bash
# Create secret in Secret Manager
echo -n "your-secure-api-key" | \
  gcloud secrets create api-key-secret \
    --data-file=- \
    --replication-policy=automatic \
    --project=$GOOGLE_CLOUD_PROJECT

# Grant access to Cloud Run service account
SERVICE_SA=$(gcloud run services describe flex-auth-proxy \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT \
  --format="value(spec.template.spec.serviceAccountName)")

gcloud secrets add-iam-policy-binding api-key-secret \
  --member=serviceAccount:$SERVICE_SA \
  --role=roles/secretmanager.secretAccessor \
  --project=$GOOGLE_CLOUD_PROJECT

# Deploy with secret
gcloud run deploy flex-auth-proxy \
  --image=europe-docker.pkg.dev/$GOOGLE_CLOUD_PROJECT/docker/flex-auth-proxy:1.0.0 \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT \
  --set-secrets=API_KEY_SECRET=api-key-secret:latest
```

### Sidecar Deployment Pattern

Deploy as a sidecar with a backend service:

```yaml
# cloud-run-service.yaml
apiVersion: serving.knative.dev/v1
kind: Service
metadata:
  name: my-service
spec:
  template:
    spec:
      containers:
      - name: backend
        image: gcr.io/your-project/backend:latest
        ports:
        - containerPort: 8080
      - name: proxy
        image: europe-docker.pkg.dev/your-project/docker/flex-auth-proxy:1.0.0
        ports:
        - containerPort: 8888
        env:
        - name: PROXY_SERVER_TARGET_URL
          value: http://localhost:8080
        - name: PROXY_AUTH_JWT_ENABLED
          value: "true"
        - name: LOG_MODE
          value: production
```

Apply with:
```bash
gcloud run services replace cloud-run-service.yaml \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT
```

### Update Existing Service

```bash
# Update image version
gcloud run services update flex-auth-proxy \
  --image=europe-docker.pkg.dev/$GOOGLE_CLOUD_PROJECT/docker/flex-auth-proxy:1.1.0 \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT

# Update environment variable
gcloud run services update flex-auth-proxy \
  --update-env-vars PROXY_SECURITY_RATE_LIMIT_ENABLED=true \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT
```

## Configuration in Production

### Environment Variables Strategy

1. **Non-sensitive config** → Environment variables in Cloud Run
2. **Sensitive config** → Secret Manager with volume mounts or env vars
3. **Dynamic config** → Config file in GCS (optional)

### Production Configuration Example

```bash
# Required
GOOGLE_CLOUD_PROJECT=your-project-id
LOG_MODE=production
LOG_LEVEL=info

# Server configuration
PROXY_SERVER_PORT=8888
PROXY_SERVER_TARGET_URL=http://backend:8080
PROXY_SERVER_HEALTH_CHECK_PATH=/healthz

# Security
PROXY_SECURITY_RATE_LIMIT_ENABLED=true
PROXY_SECURITY_RATE_LIMIT_REQUESTS_PER_MIN=100

# JWT Authentication
PROXY_AUTH_JWT_ENABLED=true
PROXY_AUTH_JWT_ISSUER=https://securetoken.google.com/your-project-id
PROXY_AUTH_JWT_AUDIENCE=your-project-id

# API Key (from Secret Manager)
PROXY_AUTH_API_KEY_ENABLED=false
API_KEY_SECRET=<from-secret>
```

### Health Check Configuration

Configure Cloud Run health checks:

```bash
gcloud run services update flex-auth-proxy \
  --http-health-check-path=/healthz \
  --http-health-check-port=8888 \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT
```

## Monitoring & Logging

### Cloud Logging

Logs are automatically sent to Cloud Logging when `LOG_MODE=production`.

View logs:
```bash
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=flex-auth-proxy" \
  --limit=50 \
  --format=json \
  --project=$GOOGLE_CLOUD_PROJECT
```

### Structured Logging

Production logs are JSON-formatted and include:
- `timestamp`: RFC3339 timestamp
- `severity`: Log level (INFO, WARN, ERROR)
- `message`: Log message
- Context fields: `request_id`, `method`, `path`, `status`, `duration_ms`

### Cloud Monitoring Metrics

Key metrics to monitor:

1. **Request Count** - Total requests
2. **Request Latency** - P50, P95, P99
3. **Error Rate** - 4xx and 5xx responses
4. **Container CPU** - CPU utilization
5. **Container Memory** - Memory usage
6. **Startup Latency** - Cold start time

## Security Best Practices

### 1. Use Minimal Container Image

The Dockerfile uses `gcr.io/distroless/static-debian12:nonroot`:
- No shell or package manager
- Runs as non-root user
- Minimal attack surface

### 2. Secrets Management

- Never hardcode secrets in config files or images
- Use Secret Manager for sensitive values
- Rotate secrets regularly
- Use IAM for access control

### 3. Network Security

```bash
# Restrict ingress to internal only
gcloud run services update flex-auth-proxy \
  --ingress=internal \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT

# Use VPC connector for private backends
gcloud run services update flex-auth-proxy \
  --vpc-connector=my-connector \
  --vpc-egress=private-ranges-only \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT
```

### 4. Resource Limits

Set appropriate resource limits:
- **CPU**: 1 vCPU recommended
- **Memory**: 128Mi-256Mi
- **Concurrency**: 80-100
- **Max Instances**: Based on expected load

## Troubleshooting

### Container Fails to Start

**Check logs:**
```bash
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=flex-auth-proxy" \
  --limit=20 \
  --project=$GOOGLE_CLOUD_PROJECT
```

**Common issues:**
- Missing required environment variables
- Invalid configuration
- Target URL unreachable

### High Memory Usage

**Solution:** Adjust JWKS cache settings:
```bash
gcloud run services update flex-auth-proxy \
  --update-env-vars PROXY_AUTH_JWT_JWKS_CACHE_TTL_MINUTES=60 \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT
```

### 502 Bad Gateway

**Causes:**
- Backend service is down
- Invalid `PROXY_SERVER_TARGET_URL`
- Network connectivity issues

**Check:**
```bash
# Test backend from proxy container
gcloud run services proxy flex-auth-proxy --port=8888
curl http://localhost:8888/healthz
```

### JWT Validation Fails

**Verify JWKS accessibility:**
```bash
# From proxy container
curl https://www.googleapis.com/oauth2/v3/certs
```

**Check issuer/audience config:**
```bash
gcloud run services describe flex-auth-proxy \
  --platform=managed \
  --region=europe-west1 \
  --format="yaml(spec.template.spec.containers[0].env)" \
  --project=$GOOGLE_CLOUD_PROJECT
```

## Rollback

```bash
# List revisions
gcloud run revisions list \
  --service=flex-auth-proxy \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT

# Rollback to previous revision
gcloud run services update-traffic flex-auth-proxy \
  --to-revisions=REVISION_NAME=100 \
  --platform=managed \
  --region=europe-west1 \
  --project=$GOOGLE_CLOUD_PROJECT
```

## See Also

- [Plugin Guide](PLUGINS.md) — Build variants, plugin configuration, custom builds
- [Configuration Guide](CONFIGURATION.md)
- [Environment Variables Guide](ENVIRONMENT.md)
- [Development Guide](DEVELOPMENT.md)
- [API Documentation](API.md)
