#!/usr/bin/env bash
# [MISE] description="Run e2e tests with chainsaw (requires running mise run dev:tilt in a different terminal)"
set -euo pipefail

CLUSTER_NAME="pgrator"
CONTEXT_NAME="${DEV_CLUSTER_ENGINE}-${CLUSTER_NAME}"

tilt wait --for=condition=Ready "uiresource/pgrator" --timeout 2m
chainsaw test --kube-context "${CONTEXT_NAME}" --test-dir tests/e2e/ --config .chainsaw.yaml
