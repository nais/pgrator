#!/usr/bin/env bash
#MISE description="Start Tilt for local development (creates kind cluster if needed)"
set -euo pipefail

mise run dev:setup-cluster
tilt up
