#!/bin/bash
set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INTEGRATION_TESTS_DIR="$(dirname "$SCRIPT_DIR")"
CONFIG_DIR="$INTEGRATION_TESTS_DIR/config"

log_info "Setting up Tekton test environment..."

# Install required tools
log_info "Installing required tools..."

# Install kubectl
if ! command -v kubectl &> /dev/null; then
    log_info "Installing kubectl..."
    curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
    chmod +x kubectl
    sudo mv kubectl /usr/local/bin/
    log_success "kubectl installed"
else
    log_info "kubectl already installed"
fi

# Install kind
if ! command -v kind &> /dev/null; then
    log_info "Installing kind..."
    curl -Lo ./kind "https://kind.sigs.k8s.io/dl/v0.29.0/kind-linux-amd64"
    chmod +x ./kind
    sudo mv ./kind /usr/local/bin/kind
    log_success "kind installed"
else
    log_info "kind already installed"
fi

# Create Kind cluster
log_info "Creating Kind cluster for Tekton testing..."
if kind get clusters | grep -q "tekton-test"; then
    log_warning "Kind cluster 'tekton-test' already exists, deleting it..."
    kind delete cluster --name tekton-test
fi

kind create cluster --name tekton-test --config "$CONFIG_DIR/kind-config.yaml"
kubectl cluster-info --context kind-tekton-test
log_success "Kind cluster created"

# Install Tekton Pipelines
log_info "Installing Tekton Pipelines..."
kubectl apply --filename https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml

# Wait for Tekton components to be ready
log_info "Waiting for Tekton Pipelines to be ready..."
kubectl wait --for=condition=Available --timeout=180s deployment/tekton-pipelines-controller -n tekton-pipelines
kubectl wait --for=condition=Available --timeout=180s deployment/tekton-pipelines-webhook -n tekton-pipelines
log_success "Tekton Pipelines installed and ready"

# Create test namespace
log_info "Creating test namespace..."
kubectl create namespace tekton-test
kubectl config set-context --current --namespace=tekton-test
log_success "Test namespace created"

# Install the pipeline under test
log_info "Installing verify-grouped-snapshot pipeline..."
kubectl apply -f "$INTEGRATION_TESTS_DIR/verify-grouped-snapshot.yaml" -n tekton-test

# Wait for pipeline to be available
log_info "Waiting for pipeline to be available..."
timeout=60
counter=0
while ! kubectl get pipeline verify-grouped-snapshot -n tekton-test > /dev/null 2>&1; do
    if [ $counter -ge $timeout ]; then
        log_error "Pipeline not ready after ${timeout}s"
        exit 1
    fi
    sleep 1
    counter=$((counter + 1))
done

log_success "Pipeline installed and ready"
log_success "Test environment setup complete!"

# Show cluster status
log_info "Cluster status:"
kubectl get all -n tekton-test
