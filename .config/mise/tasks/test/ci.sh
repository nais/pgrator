#!/usr/bin/env bash
# [MISE] description="Run e2e tests with chainsaw, with a kind cluster"
# [MISE] depends=["dev:setup-cluster"]
set -euo pipefail

CLUSTER_NAME="pgrator"
CONTEXT_NAME="${DEV_CLUSTER_ENGINE}-${CLUSTER_NAME}"

tilt ci --context="${CONTEXT_NAME}"

echo "Remember to stop the cluster with 'mise run dev:stop-cluster'"
