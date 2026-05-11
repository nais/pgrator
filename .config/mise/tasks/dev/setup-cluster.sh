#!/usr/bin/env bash
#MISE description="Create a kind cluster for local development"
set -euo pipefail

CLUSTER_NAME="pgrator"

if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  echo "Kind cluster '${CLUSTER_NAME}' already exists"
  kubectl cluster-info --context "kind-${CLUSTER_NAME}" >/dev/null 2>&1 || {
    echo "Cluster exists but is not reachable, recreating..."
    kind delete cluster --name "${CLUSTER_NAME}"
    kind create cluster --name "${CLUSTER_NAME}" --wait 60s
  }
else
  echo "Creating kind cluster '${CLUSTER_NAME}'..."
  kind create cluster --name "${CLUSTER_NAME}" --wait 60s
fi

echo "Cluster ready. Context: kind-${CLUSTER_NAME}"
