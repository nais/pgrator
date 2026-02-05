#!/usr/bin/env bash
#MISE hide=true
set -euo pipefail

docker buildx create --name pgrator-builder --node pgrator-builder0
