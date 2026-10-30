#!/usr/bin/env sh
set -eu

name=$1
level=$2
# GNU date in CI, BSD date on developer Macs. Requests may live at most one hour.
expiry=$(date -u -d '+30 minutes' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+30M +%Y-%m-%dT%H:%M:%SZ)
kubectl create -n "$NAMESPACE" -f - <<EOF
apiVersion: nais.io/v1
kind: PostgresAccess
metadata:
  name: $name
spec:
  postgresBranch: mydb-main-6bc4781b
  username: e2e-admin@example.com
  accessLevel: $level
  expiresAt: "$expiry"
EOF
