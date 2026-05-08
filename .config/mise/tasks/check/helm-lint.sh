#!/usr/bin/env bash
#MISE description="Lint helm charts"
set -euo pipefail

helm lint --strict ./charts/pgrator
