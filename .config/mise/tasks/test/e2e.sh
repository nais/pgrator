#!/usr/bin/env bash
# [MISE] description="Run e2e tests with chainsaw (requires a running cluster)"
# [MISE] depends=["dev:setup-cluster"]
set -euo pipefail

chainsaw test --kube-context kind-pgrator --test-dir tests/e2e/ --config .chainsaw.yaml
