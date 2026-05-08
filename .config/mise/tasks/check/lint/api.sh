#!/usr/bin/env bash
#MISE description="Run golangci-lint linter against pkg/api code"
#MISE dir="{{config_root}}/pkg/api"
#USAGE flag "--fix" help="Automatically fix issues"
set -euo pipefail

golangci-lint run "$@"
