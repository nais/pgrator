#!/usr/bin/env bash
#MISE description="Setup envtest binaries"
set -euo pipefail

setup-envtest use "${ENVTEST_K8S_VERSION}" -p path
