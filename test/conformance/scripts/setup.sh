#!/usr/bin/env bash
# setup.sh - Deploy HUG into a Kind cluster and run Gateway API conformance tests.
#
# Prerequisites:
#   - kind, kubectl, docker, go
#   - A running Kind cluster (or use: task kind-create-conformance)
#
# Usage:
#   ./test/conformance/scripts/setup.sh [--create-cluster] [--run-tests]
#
# Environment variables:
#   CLUSTER_NAME       Kind cluster name (default: hug-conformance)
#   KUBECONFIG         Path to kubeconfig (auto-set for Kind)
#   HUG_HTTP_PORT      NodePort for HTTP (default: 31080)
#   HUG_HTTPS_PORT     NodePort for HTTPS (default: 31443)
#   HUG_GATEWAY_CLASS  GatewayClass name (default: haproxy)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

CLUSTER_NAME="${CLUSTER_NAME:-hug-conformance}"
CONFIG_DIR="${PROJECT_ROOT}/example/deploy"
KIND_CONFIG="${PROJECT_ROOT}/ci/kind/kind-config-conformance.yaml"
CONFORMANCE_DIR="${PROJECT_ROOT}/test/conformance"

# Images required by the Gateway API conformance test suite
CONFORMANCE_ECHO_IMAGE="gcr.io/k8s-staging-gateway-api/echo-basic:v20240412-v1.0.0-394-g40c666fd"
CONFORMANCE_COREDNS_IMAGE="registry.k8s.io/coredns/coredns:v1.12.2"

CREATE_CLUSTER=false
RUN_TESTS=false

for arg in "$@"; do
  case "$arg" in
    --create-cluster) CREATE_CLUSTER=true ;;
    --run-tests)      RUN_TESTS=true ;;
    --all)            CREATE_CLUSTER=true; RUN_TESTS=true ;;
    *)                echo "Unknown argument: $arg"; exit 1 ;;
  esac
done

log() { echo "==> $*"; }

# Step 1: Create Kind cluster (optional)
if [ "$CREATE_CLUSTER" = true ]; then
  log "Deleting existing Kind cluster '${CLUSTER_NAME}' (if any)..."
  kind delete cluster --name "${CLUSTER_NAME}" 2>/dev/null || true

  log "Creating Kind cluster '${CLUSTER_NAME}'..."
  kind create cluster \
    --name "${CLUSTER_NAME}" \
    --config "${KIND_CONFIG}" \
    --image "kindest/node:v1.32.0"

  log "Kind cluster created."
fi

# Ensure KUBECONFIG is set for the Kind cluster
export KUBECONFIG="${KUBECONFIG:-$(kind get kubeconfig-path --name="${CLUSTER_NAME}" 2>/dev/null || echo "${HOME}/.kube/config")}"

# Step 2: Build and load HUG image
log "Building HUG controller..."
(cd "${PROJECT_ROOT}/cmd/controller" && CGO_ENABLED=0 go build -buildvcs=true -o "${PROJECT_ROOT}/build/kubernetes-controller" .)

log "Building Docker image..."
docker build --network=host \
  -t haproxytech/haproxy-unified-gateway:latest \
  -f "${PROJECT_ROOT}/build/Dockerfile.dev" \
  "${PROJECT_ROOT}"
rm -f "${PROJECT_ROOT}/build/kubernetes-controller"

log "Pre-pulling conformance test images..."
docker pull "${CONFORMANCE_ECHO_IMAGE}"
docker pull "${CONFORMANCE_COREDNS_IMAGE}"

log "Loading images into Kind cluster..."
kind load docker-image haproxytech/haproxy-unified-gateway:latest --name="${CLUSTER_NAME}"
kind load docker-image "${CONFORMANCE_ECHO_IMAGE}" --name="${CLUSTER_NAME}"
kind load docker-image "${CONFORMANCE_COREDNS_IMAGE}" --name="${CLUSTER_NAME}"

# Step 3: Deploy HUG
log "Creating namespace..."
kubectl apply -f "${CONFIG_DIR}/hug/namespace.yaml"

log "Installing CRD RBAC..."
kubectl apply -f "${CONFIG_DIR}/crd-update/rbac.yaml"

log "Installing Gateway API CRDs..."
kubectl apply -f "${CONFIG_DIR}/crd-update/job-gwapi.yaml"
kubectl wait --for=condition=complete --timeout=120s \
  job/haproxy-unified-gateway-gwapi -n haproxy-unified-gateway

log "Installing HUG CRDs..."
kubectl apply -f "${CONFIG_DIR}/crd-update/job-crd.yaml"
kubectl wait --for=condition=complete --timeout=120s \
  job/haproxy-unified-gateway-crd -n haproxy-unified-gateway

log "Deploying controller RBAC..."
kubectl apply -f "${CONFIG_DIR}/hug/rbac.yaml"

log "Deploying HugConf..."
kubectl apply -f "${CONFIG_DIR}/hug/hugconf.yaml"

log "Deploying Global config..."
kubectl apply -f "${CONFIG_DIR}/hug/global.yaml"

log "Deploying controller..."
kubectl apply -f "${CONFIG_DIR}/hug-dev/controller.yaml"

log "Waiting for controller to be ready..."
kubectl wait --for=condition=ready --timeout=120s \
  pod -l run=haproxy-unified-gateway -n haproxy-unified-gateway

# Step 4: Create GatewayClass for conformance tests
log "Creating GatewayClass..."
kubectl apply -f "${CONFORMANCE_DIR}/manifests/gatewayclass.yaml"

log "HUG deployed successfully. Ready for conformance tests."

# Step 5: Run conformance tests (optional)
if [ "$RUN_TESTS" = true ]; then
  log "Running conformance tests..."
  cd "${PROJECT_ROOT}"
  go test ./test/conformance/ \
    -v \
    -timeout 60m \
    -count=1 \
    -run TestConformance
fi
