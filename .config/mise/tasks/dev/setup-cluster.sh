#!/usr/bin/env bash
#MISE description="Create a kind cluster for local development"
set -euo pipefail

CLUSTER_NAME="pgrator"
CONTEXT_NAME="kind-${CLUSTER_NAME}"

if ctlptl get cluster "${CONTEXT_NAME}" 2>/dev/null; then
  echo "Kind cluster '${CLUSTER_NAME}' already exists"
  kubectl cluster-info --context "${CONTEXT_NAME}" >/dev/null 2>&1 || {
    echo "Cluster exists but is not reachable, recreating..."
    ctlptl
    ctlptl delete cluster "${CONTEXT_NAME}"
    ctlptl create cluster kind --name "${CONTEXT_NAME}" --registry=ctlptl-registry
  }
else
  echo "Creating kind cluster '${CLUSTER_NAME}'..."
  ctlptl create cluster kind --name "${CONTEXT_NAME}" --registry=ctlptl-registry
fi

echo "Cluster ready. Context: ${CONTEXT_NAME}"
