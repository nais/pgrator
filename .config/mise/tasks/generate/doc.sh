#!/usr/bin/env bash
#MISE description="Generate documentation for nais/doc"
set -euo pipefail

mkdir -p doc/output doc/output/openapi
go run cmd/docgen/docgen.go \
  --api-dir ./pkg/api/... \
  --output-dir doc/output \
  --template-dir doc/templates \
  --openapi-output doc/output/openapi
