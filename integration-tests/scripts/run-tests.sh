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
TEST_DATA_DIR="$INTEGRATION_TESTS_DIR/test-data"

# Test results tracking
TESTS_PASSED=0
TESTS_FAILED=0

# Function to run a single test
run_test() {
    local test_name="$1"
    local snapshot_file="$2"
    local expected_result="$3"  # "success" or "failure"
    local pipelinerun_name="test-${test_name}"

    log_info "=== Running test: $test_name ==="

    # Read snapshot data
    if [ ! -f "$snapshot_file" ]; then
        log_error "Snapshot file not found: $snapshot_file"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi

    local snapshot_data
    snapshot_data=$(cat "$snapshot_file")

    # Create PipelineRun YAML with generateName
    cat << EOF > "/tmp/${pipelinerun_name}.yaml"
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  generateName: ${pipelinerun_name}-
  namespace: tekton-test
spec:
  pipelineRef:
    name: verify-grouped-snapshot
  params:
  - name: SNAPSHOT
    value: '${snapshot_data}'
EOF

    # Apply PipelineRun and capture the generated name
    local actual_pipelinerun_name
    if ! actual_pipelinerun_name=$(kubectl create -f "/tmp/${pipelinerun_name}.yaml" -n tekton-test -o jsonpath='{.metadata.name}'); then
        log_error "Failed to create PipelineRun for test: $test_name"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi

    log_info "Created PipelineRun: $actual_pipelinerun_name"

    # Wait for PipelineRun to complete
    log_info "Waiting for PipelineRun to complete..."
    if [ "$expected_result" = "success" ]; then
        if kubectl wait --for=condition=Succeeded=True --timeout=300s "pipelinerun/${actual_pipelinerun_name}" -n tekton-test; then
            log_success "Test '$test_name' passed (expected success, got success)"
            TESTS_PASSED=$((TESTS_PASSED + 1))
        else
            log_error "Test '$test_name' failed (expected success, got failure)"
            TESTS_FAILED=$((TESTS_FAILED + 1))
        fi
    else
        # Expected failure - wait for failure condition or timeout
        if kubectl wait --for=condition=Succeeded=False --timeout=300s "pipelinerun/${actual_pipelinerun_name}" -n tekton-test 2>/dev/null; then
            log_success "Test '$test_name' passed (expected failure, got failure)"
            TESTS_PASSED=$((TESTS_PASSED + 1))
        else
            # Check if it actually succeeded (which would be wrong)
            local pr_status
            pr_status=$(kubectl get pipelinerun "${actual_pipelinerun_name}" -n tekton-test -o jsonpath='{.status.conditions[0].status}' 2>/dev/null || echo "Unknown")
            if [ "$pr_status" = "True" ]; then
                log_error "Test '$test_name' failed (expected failure, got success)"
                TESTS_FAILED=$((TESTS_FAILED + 1))
            else
                log_success "Test '$test_name' passed (expected failure, got failure)"
                TESTS_PASSED=$((TESTS_PASSED + 1))
            fi
        fi
    fi

    # Show logs for debugging
    log_info "Pipeline logs for test '$test_name':"

    # Get the pod associated with this pipelinerun and show its logs
    local pod_name
    pod_name=$(kubectl get pipelinerun "${actual_pipelinerun_name}" -n tekton-test -o jsonpath='{.status.pipelineRunStatusFields.childReferences[0].name}' 2>/dev/null || echo "")

    if [ -n "$pod_name" ]; then
        log_info "Getting logs from pod: $pod_name"
        kubectl logs "$pod_name" -n tekton-test --all-containers=true || echo "Failed to get logs from pod"
    else
        log_warning "No pod found for pipelinerun ${actual_pipelinerun_name}, checking taskruns..."
        # Fallback: try to get logs from taskruns
        local taskruns
        taskruns=$(kubectl get taskrun -n tekton-test -l tekton.dev/pipelineRun="${actual_pipelinerun_name}" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")
        if [ -n "$taskruns" ]; then
            for tr in $taskruns; do
                log_info "Getting logs from taskrun: $tr"
                kubectl logs -l tekton.dev/taskRun="$tr" -n tekton-test --all-containers=true || echo "Failed to get logs from taskrun $tr"
            done
        else
            log_warning "No taskruns found for pipelinerun ${actual_pipelinerun_name}"
        fi
    fi

    # Cleanup
    rm -f "/tmp/${pipelinerun_name}.yaml"

    echo ""
}

# Main test execution
log_info "Starting Tekton pipeline tests..."

# Ensure we're in the right context
kubectl config set-context --current --namespace=tekton-test

# Test 1: Invalid snapshot (should fail)
run_test "invalid-snapshot" "$TEST_DATA_DIR/snapshot-invalid.json" "failure"

# Test 2: Valid snapshot (should pass)
run_test "valid-snapshot" "$TEST_DATA_DIR/snapshot-valid.json" "success"

# Test 3: Empty snapshot (should fail)
run_test "empty-snapshot" "$TEST_DATA_DIR/snapshot-empty.json" "failure"

# Test 4: Single component (should pass)
run_test "single-snapshot" "$TEST_DATA_DIR/snapshot-single.json" "success"

# Test summary
echo ""
log_info "=== Test Summary ==="
echo "Tests passed: $TESTS_PASSED"
echo "Tests failed: $TESTS_FAILED"
echo "Total tests: $((TESTS_PASSED + TESTS_FAILED))"

# Show all PipelineRuns
echo ""
log_info "All PipelineRuns in tekton-test namespace:"
kubectl get pipelinerun -n tekton-test -o custom-columns="NAME:.metadata.name,STATUS:.status.conditions[0].status,REASON:.status.conditions[0].reason"

# Exit with appropriate code
if [ $TESTS_FAILED -eq 0 ]; then
    log_success "All tests passed! 🎉"
    exit 0
else
    log_error "Some tests failed!"
    exit 1
fi
