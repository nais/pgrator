#!/usr/bin/env bash
#MISE description="Generate documentation for nais/doc"
set -euo pipefail

mkdir -p doc/output/openapi
rm -f doc/output/openapi/*.json
go run cmd/docgen/docgen.go \
  --api-dir ./pkg/api/... \
  --output-dir doc/output \
  --template-dir doc/templates \
  --openapi-output doc/output/openapi
