#!/usr/bin/env bash
#MISE description="Run go fmt against root code"
set -euo pipefail

go fmt ./...
