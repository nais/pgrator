#!/usr/bin/env bash
#MISE description="Start Tilt for local development (creates kind cluster if needed)"
set -euo pipefail

CLUSTER_NAME="pgrator"
CONTEXT_NAME="${DEV_CLUSTER_ENGINE}-${CLUSTER_NAME}"

mise run dev:setup-cluster
tilt up --legacy=true --context="${CONTEXT_NAME}"
