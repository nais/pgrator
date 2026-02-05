#!/usr/bin/env bash
#MISE description="Run a controller from your host"
#MISE depends=["fmt", "check", "generate"]
set -euo pipefail

go run ./cmd/main.go
