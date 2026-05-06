#!/usr/bin/env bash
#MISE description="Run Go vulnerability check"
set -euo pipefail

go run golang.org/x/vuln/cmd/govulncheck@latest ./...
