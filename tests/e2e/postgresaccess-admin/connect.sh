#!/usr/bin/env sh
set -eu

access=$1
sql=$2
cluster=pg-mydb-main-6bc4781b
primary=$(kubectl get cluster "$cluster" -n "$NAMESPACE" -o jsonpath='{.status.currentPrimary}')
password=$(kubectl get secret "$access-credentials" -n "$NAMESPACE" -o jsonpath='{.data.password}' | base64 -d)
test -n "$password"
# Connect as the personal login over TCP/TLS, exercising CNPG's SCRAM password
# and role memberships. No shared app credentials, certificate, or SET SESSION
# AUTHORIZATION is used. Loopback avoids depending on a relay deployment in kind.
kubectl exec -i -n "$NAMESPACE" "$primary" -c postgres -- \
  env PGPASSWORD="$password" PGUSER=e2e-admin@example.com PGDATABASE=app \
  PGHOST="$cluster-rw.$NAMESPACE.svc.cluster.local" PGHOSTADDR=127.0.0.1 \
  PGSSLMODE=verify-full PGSSLROOTCERT=/controller/certificates/server-ca.crt \
  PGCONNECT_TIMEOUT=10 psql -X -v ON_ERROR_STOP=1 < "$sql"
