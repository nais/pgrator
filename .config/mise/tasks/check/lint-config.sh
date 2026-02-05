#!/usr/bin/env bash
#MISE description="Verify golangci-lint linter configuration"
set -euo pipefail

golangci-lint config verify
