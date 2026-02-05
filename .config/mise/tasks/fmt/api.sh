#!/usr/bin/env bash
#MISE description="Run go fmt against pkg/api code"
#MISE dir="{{config_root}}/pkg/api"
set -euo pipefail

go fmt ./...
