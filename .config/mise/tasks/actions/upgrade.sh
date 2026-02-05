#!/usr/bin/env bash
#MISE description="Upgrade all github actions to latest"
go tool ratchet upgrade .github/workflows/*.yml
