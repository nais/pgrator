#!/usr/bin/env bash
#MISE description="Find dead/unreachable code"
set -euo pipefail

go run golang.org/x/tools/cmd/deadcode@latest -test ./...
