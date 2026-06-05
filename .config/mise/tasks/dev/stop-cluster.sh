#!/usr/bin/env bash
# [MISE] description="Stop the kind cluster started with dev:setup-cluster"
# [MISE] wait_for=["test:e2e", "test:ci"]
set -euo pipefail

CLUSTER_NAME="pgrator"
CONTEXT_NAME="${DEV_CLUSTER_ENGINE}-${CLUSTER_NAME}"

if ctlptl get cluster "${CONTEXT_NAME}" 2>/dev/null; then
  echo "${DEV_CLUSTER_ENGINE} cluster '${CLUSTER_NAME}' exists, stopping"
  ctlptl delete cluster "${CONTEXT_NAME}"
else
  echo "No ${DEV_CLUSTER_ENGINE} cluster '${CLUSTER_NAME}' exists"
fi

echo "Cluster '${CLUSTER_NAME}' stopped."
