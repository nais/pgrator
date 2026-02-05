#!/usr/bin/env bash
#MISE description="Build and push docker image for the manager for cross-platform support"
#MISE depends=["docker:_builder"]
#MISE depends_post=["docker:_cleanup_builder"]
set -euo pipefail

docker buildx build --builder pgrator-builder --push --platform=linux/arm64,linux/amd64 --tag "${PGRATOR_TTL_IMAGE}" .
