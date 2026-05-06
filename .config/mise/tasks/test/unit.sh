#!/usr/bin/env bash
#MISE description="Run tests"
#MISE depends=["generate"]
set -euo pipefail

GITHUB_ACTIONS="${GITHUB_ACTIONS:-false}"
KUBEBUILDER_ASSETS="$(setup-envtest use "${ENVTEST_K8S_VERSION}" -p path)"
export KUBEBUILDER_ASSETS
declare -a output_flags
if [[ "${GITHUB_ACTIONS}" == "true" ]]; then
  output_flags+=("--github-output")
fi
go tool ginkgo run -r --keep-going --race --coverprofile=coverage.out "${output_flags[@]}"
