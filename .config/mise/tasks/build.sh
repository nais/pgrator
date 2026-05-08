#!/usr/bin/env bash
#MISE description="Build manager binary"
#MISE depends=["fmt", "check", "generate"]
set -euo pipefail

go build -o bin/manager cmd/main.go
