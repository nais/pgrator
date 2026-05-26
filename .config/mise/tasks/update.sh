#!/usr/bin/env bash
#MISE description="Update all dependencies (root, api module, and examples)"
set -euo pipefail

echo "📦 Updating Go dependencies (root)..."
go get -u ./...
go mod tidy

echo "📦 Updating Go dependencies (api module)..."
(cd pkg/api && go get -u ./... && go mod tidy)

if [[ -d examples ]]; then
  for service in examples/quotes-backend examples/quotes-frontend examples/quotes-analytics examples/quotes-loadgen; do
    if [[ -d "$service" ]] && [[ -f "$service/.mise.toml" ]]; then
      echo "📦 Updating $service..."
      (cd "$service" && mise run dependencies:update 2>/dev/null || mise run mod-tidy 2>/dev/null || true)
    fi
  done
fi

echo "✅ All dependencies updated"
