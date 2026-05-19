#!/usr/bin/env bash
#MISE description="Run e2e tests with chainsaw (requires a running cluster)"
set -euo pipefail

chainsaw test --test-dir tests/e2e/ --config .chainsaw.yaml
