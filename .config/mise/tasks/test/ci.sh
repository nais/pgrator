#!/usr/bin/env bash
# [MISE] description="Run e2e tests with chainsaw, with a kind cluster"
# [MISE] depends=["dev:setup-cluster"]
set -euo pipefail

tilt ci

echo "Remember to stop the cluster with 'mise run dev:stop-cluster'"
