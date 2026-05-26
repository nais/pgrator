#!/usr/bin/env bash
#MISE description="Install all dependencies (root, api module, and examples)"
set -euo pipefail

echo "📦 Installing Go dependencies..."
go mod download

echo "📦 Installing API module dependencies..."
(cd pkg/api && go mod download)

if [[ -d examples ]]; then
  echo "📦 Installing examples dependencies..."
  (cd examples && mise install 2>/dev/null || true)

  for service in examples/quotes-backend examples/quotes-frontend examples/quotes-analytics examples/quotes-loadgen; do
    if [[ -d "$service" ]] && [[ -f "$service/.mise.toml" ]]; then
      echo "  → $service"
      (cd "$service" && mise install 2>/dev/null || true)
    fi
  done
fi

echo "✅ All dependencies installed"
