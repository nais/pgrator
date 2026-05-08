#!/usr/bin/env bash
#MISE description="Run golangci-lint linter against root code"
#USAGE flag "--fix" help="Automatically fix issues"
set -euo pipefail

golangci-lint run "$@"
