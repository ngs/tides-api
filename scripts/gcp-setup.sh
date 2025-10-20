#!/bin/bash

# GCP Setup Script for Tide API
# Automates GCP infrastructure setup for GitHub Actions deployment

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
PROJECT_ID="${PROJECT_ID:-$(gcloud config get-value project 2>/dev/null)}"
REGION="${REGION:-asia-northeast1}"
SERVICE_NAME="${SERVICE_NAME:-tides-api}"
REPOSITORY="${REPOSITORY:-cloud-run-source-deploy}"
GITHUB_ORG="${GITHUB_ORG:-ngs}"
GITHUB_REPO="${GITHUB_REPO:-tides-api}"

# Workload Identity Federation
WIF_POOL_NAME="github-pool"
WIF_PROVIDER_NAME="github-provider"
WIF_SA_NAME="github-actions-sa"
WIF_SA_EMAIL="${WIF_SA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"

# Print functions
print_info() {
    echo -e "${BLUE}ℹ${NC} $1"
}

print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

print_header() {
    echo ""
    echo -e "${BLUE}================================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}================================================${NC}"
    echo ""
}

# Check prerequisites
check_prerequisites() {
    print_header "Checking Prerequisites"

    # Check gcloud
    if ! command -v gcloud &> /dev/null; then
        print_error "gcloud CLI not found. Please install: https://cloud.google.com/sdk/docs/install"
        exit 1
    fi
    print_success "gcloud CLI found"

    # Check project
    if [ -z "$PROJECT_ID" ]; then
        print_error "PROJECT_ID not set. Set with: export PROJECT_ID=your-project-id"
        exit 1
    fi
    print_success "Project ID: $PROJECT_ID"

    # Check authentication
    if ! gcloud auth list --filter=status:ACTIVE --format="value(account)" &> /dev/null; then
        print_error "Not authenticated. Run: gcloud auth login"
        exit 1
    fi
    print_success "Authenticated to GCP"
}

# Enable required APIs
enable_apis() {
    print_header "Enabling Required APIs"

    local apis=(
        "iamcredentials.googleapis.com"
        "iam.googleapis.com"
        "cloudresourcemanager.googleapis.com"
        "sts.googleapis.com"
        "cloudbuild.googleapis.com"
        "run.googleapis.com"
        "artifactregistry.googleapis.com"
        "containerregistry.googleapis.com"
        "storage-api.googleapis.com"
        "serviceusage.googleapis.com"
        "logging.googleapis.com"
    )

    print_info "Enabling APIs (this may take a few minutes)..."
    gcloud services enable "${apis[@]}" --project="$PROJECT_ID"
    print_success "All APIs enabled"

    print_info "Waiting for APIs to be fully propagated..."
    sleep 10
    print_success "APIs ready"
}

# Setup Workload Identity Federation
setup_wif() {
    print_header "Setting Up Workload Identity Federation"

    # Create Workload Identity Pool
    print_info "Creating Workload Identity Pool: $WIF_POOL_NAME"
    if gcloud iam workload-identity-pools describe "$WIF_POOL_NAME" \
        --location="global" \
        --project="$PROJECT_ID" &>/dev/null; then
        print_warning "Pool already exists, skipping creation"
    else
        gcloud iam workload-identity-pools create "$WIF_POOL_NAME" \
            --location="global" \
            --display-name="GitHub Actions Pool" \
            --description="Workload Identity Pool for GitHub Actions" \
            --project="$PROJECT_ID"
        print_success "Workload Identity Pool created"
    fi

    # Create OIDC Provider
    print_info "Creating OIDC Provider: $WIF_PROVIDER_NAME"
    if gcloud iam workload-identity-pools providers describe "$WIF_PROVIDER_NAME" \
        --workload-identity-pool="$WIF_POOL_NAME" \
        --location="global" \
        --project="$PROJECT_ID" &>/dev/null; then
        print_warning "Provider already exists, skipping creation"
    else
        gcloud iam workload-identity-pools providers create-oidc "$WIF_PROVIDER_NAME" \
            --workload-identity-pool="$WIF_POOL_NAME" \
            --location="global" \
            --issuer-uri="https://token.actions.githubusercontent.com" \
            --attribute-mapping="google.subject=assertion.sub,attribute.actor=assertion.actor,attribute.repository=assertion.repository" \
            --project="$PROJECT_ID"
        print_success "OIDC Provider created"
    fi

    # Create Service Account
    print_info "Creating Service Account: $WIF_SA_NAME"
    if gcloud iam service-accounts describe "$WIF_SA_EMAIL" \
        --project="$PROJECT_ID" &>/dev/null; then
        print_warning "Service account already exists, skipping creation"
    else
        gcloud iam service-accounts create "$WIF_SA_NAME" \
            --display-name="GitHub Actions Service Account" \
            --description="Service account for GitHub Actions deployments" \
            --project="$PROJECT_ID"
        print_success "Service account created"
    fi

    # Bind WIF to Service Account
    print_info "Binding Workload Identity to Service Account"
    gcloud iam service-accounts add-iam-policy-binding "$WIF_SA_EMAIL" \
        --role="roles/iam.workloadIdentityUser" \
        --member="principalSet://iam.googleapis.com/projects/$(gcloud projects describe $PROJECT_ID --format='value(projectNumber)')/locations/global/workloadIdentityPools/${WIF_POOL_NAME}/attribute.repository/${GITHUB_ORG}/${GITHUB_REPO}" \
        --project="$PROJECT_ID"
    print_success "Workload Identity binding complete"

    # Output WIF configuration
    print_success "Workload Identity Federation setup complete!"
    echo ""
    print_info "Add these secrets to GitHub repository settings:"
    echo ""
    echo "  WIF_PROVIDER: projects/$(gcloud projects describe $PROJECT_ID --format='value(projectNumber)')/locations/global/workloadIdentityPools/${WIF_POOL_NAME}/providers/${WIF_PROVIDER_NAME}"
    echo "  WIF_SERVICE_ACCOUNT: ${WIF_SA_EMAIL}"
    echo "  PROJECT_ID: ${PROJECT_ID}"
    echo ""
}

# Setup IAM permissions
setup_permissions() {
    print_header "Setting Up IAM Permissions"

    # GitHub Actions Service Account permissions
    print_info "Granting permissions to GitHub Actions service account"
    local gh_roles=(
        "roles/run.developer"
        "roles/storage.admin"
        "roles/artifactregistry.writer"
        "roles/iam.serviceAccountUser"
        "roles/cloudbuild.builds.editor"
    )

    for role in "${gh_roles[@]}"; do
        gcloud projects add-iam-policy-binding "$PROJECT_ID" \
            --member="serviceAccount:${WIF_SA_EMAIL}" \
            --role="$role" \
            --condition=None \
            --quiet
        print_success "Granted $role"
    done

    # Cloud Build Service Account permissions
    print_info "Granting permissions to Cloud Build service account"
    CB_SA="$(gcloud projects describe $PROJECT_ID --format='value(projectNumber)')@cloudbuild.gserviceaccount.com"

    local cb_roles=(
        "roles/run.admin"
        "roles/iam.serviceAccountUser"
        "roles/storage.admin"
        "roles/artifactregistry.writer"
        "roles/logging.logWriter"
    )

    for role in "${cb_roles[@]}"; do
        gcloud projects add-iam-policy-binding "$PROJECT_ID" \
            --member="serviceAccount:${CB_SA}" \
            --role="$role" \
            --condition=None \
            --quiet
        print_success "Granted $role to Cloud Build"
    done

    print_success "IAM permissions configured"
}

# Setup Artifact Registry
setup_registry() {
    print_header "Setting Up Artifact Registry"

    print_info "Creating Docker repository: $REPOSITORY"
    if gcloud artifacts repositories describe "$REPOSITORY" \
        --location="$REGION" \
        --project="$PROJECT_ID" &>/dev/null; then
        print_warning "Repository already exists, skipping creation"
    else
        gcloud artifacts repositories create "$REPOSITORY" \
            --repository-format=docker \
            --location="$REGION" \
            --description="Cloud Run source deployments for Tide API" \
            --project="$PROJECT_ID"
        print_success "Artifact Registry repository created"
    fi

    print_info "Configuring Docker authentication"
    gcloud auth configure-docker "${REGION}-docker.pkg.dev" --quiet
    print_success "Docker authentication configured"
}

# Setup custom domain mapping
setup_domain() {
    print_header "Setting Up Custom Domain"

    if [ -z "$DOMAIN" ]; then
        print_error "DOMAIN environment variable not set"
        echo "Usage: DOMAIN=api.yourdomain.com ./scripts/gcp-setup.sh domain"
        exit 1
    fi

    print_info "Mapping domain: $DOMAIN to service: $SERVICE_NAME"
    gcloud run domain-mappings create \
        --service="$SERVICE_NAME" \
        --domain="$DOMAIN" \
        --region="$REGION" \
        --project="$PROJECT_ID"

    print_success "Domain mapping created"
    echo ""
    print_warning "Update your DNS records with the following:"
    gcloud run domain-mappings describe "$DOMAIN" \
        --region="$REGION" \
        --project="$PROJECT_ID" \
        --format="value(status.resourceRecords)"
}

# Allow public access to Cloud Run service
allow_public_access() {
    print_header "Allowing Public Access"

    print_info "Configuring public access for: $SERVICE_NAME"
    if gcloud run services describe "$SERVICE_NAME" \
        --region="$REGION" \
        --project="$PROJECT_ID" &>/dev/null; then
        gcloud run services add-iam-policy-binding "$SERVICE_NAME" \
            --region="$REGION" \
            --member="allUsers" \
            --role="roles/run.invoker" \
            --project="$PROJECT_ID"
        print_success "Public access granted"
    else
        print_warning "Service not found. Deploy the service first."
    fi
}

# Check current configuration status
check_status() {
    print_header "Configuration Status"

    echo "Project: $PROJECT_ID"
    echo "Region: $REGION"
    echo "Service: $SERVICE_NAME"
    echo "Repository: $REPOSITORY"
    echo ""

    # Check APIs
    print_info "Checking enabled APIs..."
    local required_apis=("run" "cloudbuild" "artifactregistry")
    for api in "${required_apis[@]}"; do
        if gcloud services list --enabled --project="$PROJECT_ID" | grep -q "$api"; then
            print_success "$api API enabled"
        else
            print_warning "$api API not enabled"
        fi
    done
    echo ""

    # Check WIF
    print_info "Checking Workload Identity Federation..."
    if gcloud iam workload-identity-pools describe "$WIF_POOL_NAME" \
        --location="global" \
        --project="$PROJECT_ID" &>/dev/null; then
        print_success "WIF Pool exists"
    else
        print_warning "WIF Pool not found"
    fi

    if gcloud iam service-accounts describe "$WIF_SA_EMAIL" \
        --project="$PROJECT_ID" &>/dev/null; then
        print_success "GitHub Actions SA exists"
    else
        print_warning "GitHub Actions SA not found"
    fi
    echo ""

    # Check Artifact Registry
    print_info "Checking Artifact Registry..."
    if gcloud artifacts repositories describe "$REPOSITORY" \
        --location="$REGION" \
        --project="$PROJECT_ID" &>/dev/null; then
        print_success "Artifact Registry repository exists"
    else
        print_warning "Artifact Registry repository not found"
    fi
    echo ""

    # Check Cloud Run service
    print_info "Checking Cloud Run service..."
    if gcloud run services describe "$SERVICE_NAME" \
        --region="$REGION" \
        --project="$PROJECT_ID" &>/dev/null; then
        SERVICE_URL=$(gcloud run services describe "$SERVICE_NAME" \
            --region="$REGION" \
            --project="$PROJECT_ID" \
            --format='value(status.url)')
        print_success "Service deployed at: $SERVICE_URL"
    else
        print_warning "Service not deployed yet"
    fi
}

# Display help
show_help() {
    cat << EOF
Tide API - GCP Setup Script

Usage: ./scripts/gcp-setup.sh [COMMAND]

Commands:
  all           Run complete setup (default)
  wif           Setup Workload Identity Federation only
  permissions   Setup IAM permissions only
  registry      Setup Artifact Registry only
  domain        Setup custom domain mapping (requires DOMAIN env var)
  public        Allow public access to Cloud Run service
  status        Check current configuration status
  help          Show this help message

Environment Variables:
  PROJECT_ID    GCP Project ID (required)
  REGION        GCP Region (default: asia-northeast1)
  SERVICE_NAME  Cloud Run service name (default: tides-api)
  REPOSITORY    Artifact Registry repository (default: cloud-run-source-deploy)
  GITHUB_ORG    GitHub organization (default: ngs)
  GITHUB_REPO   GitHub repository (default: tides-api)
  DOMAIN        Custom domain for Cloud Run (required for 'domain' command)

Examples:
  # Complete setup
  PROJECT_ID=my-project ./scripts/gcp-setup.sh all

  # Setup WIF only
  PROJECT_ID=my-project ./scripts/gcp-setup.sh wif

  # Check status
  PROJECT_ID=my-project ./scripts/gcp-setup.sh status

  # Setup custom domain
  PROJECT_ID=my-project DOMAIN=api.example.com ./scripts/gcp-setup.sh domain

EOF
}

# Main execution
main() {
    local command="${1:-all}"

    case "$command" in
        all)
            check_prerequisites
            enable_apis
            setup_wif
            setup_permissions
            setup_registry
            print_header "Setup Complete!"
            print_success "All infrastructure configured successfully"
            echo ""
            check_status
            ;;
        wif)
            check_prerequisites
            enable_apis
            setup_wif
            ;;
        permissions)
            check_prerequisites
            setup_permissions
            ;;
        registry)
            check_prerequisites
            enable_apis
            setup_registry
            ;;
        domain)
            check_prerequisites
            setup_domain
            ;;
        public)
            check_prerequisites
            allow_public_access
            ;;
        status)
            check_prerequisites
            check_status
            ;;
        help|--help|-h)
            show_help
            ;;
        *)
            print_error "Unknown command: $command"
            echo ""
            show_help
            exit 1
            ;;
    esac
}

# Run main function with all arguments
main "$@"
