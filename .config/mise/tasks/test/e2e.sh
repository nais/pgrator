#!/usr/bin/env bash
# [MISE] description="Run e2e tests with chainsaw (requires running mise run dev:tilt in a different terminal)"
set -euo pipefail

chainsaw test --kube-context kind-pgrator --test-dir tests/e2e/ --config .chainsaw.yaml
