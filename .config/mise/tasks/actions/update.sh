#!/usr/bin/env bash
#MISE description="Upgrade all github actions to latest version satisfying their version tag"
go tool ratchet update .github/workflows/*.yml
