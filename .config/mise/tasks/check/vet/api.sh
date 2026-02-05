#!/usr/bin/env bash
#MISE description="Run go vet against pkg/api code"
#MISE dir="{{config_root}}/pkg/api"
set -euo pipefail

go vet ./...
