#!/usr/bin/env bash
#MISE description="Run go vet against root code"
set -euo pipefail

go vet ./...
