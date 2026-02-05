#!/usr/bin/env bash
#MISE description="Build docker image for the manager"
set -euo pipefail

docker buildx build --load --tag "${PGRATOR_IMAGE}" .
