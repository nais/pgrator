#!/usr/bin/env bash
#MISE description="Ensure generated artifacts are up to date"
#MISE depends=["generate"]
set -euo pipefail

if ! git diff --exit-code --name-only; then
	echo "The file(s) listed above are out-of-date. Please run \`mise run generate\` and commit the changes."
	exit 1
fi
