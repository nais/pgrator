#!/usr/bin/env bash
#MISE description="Run tests"
#MISE depends=["generate"]
set -euo pipefail

KUBEBUILDER_ASSETS="$(setup-envtest use "${ENVTEST_K8S_VERSION}" -p path)"
export KUBEBUILDER_ASSETS

go test ./...
(
  cd pkg/api
  go test ./...
)
