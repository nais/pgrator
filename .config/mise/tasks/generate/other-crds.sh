#!/usr/bin/env bash
# [MISE] description="Update the non-config-connector CRDs in controller testdata. Downloads from configured repositories at given version."
set -euo pipefail

OUT_DIR="internal/controller/testdata/external-crds"

AIVEN_OPERATOR_VERSION="v0.39.0"
AIVEN_BASE_URL="https://raw.githubusercontent.com/aiven/aiven-operator/refs/tags/${AIVEN_OPERATOR_VERSION}/config/crd/bases/"
AIVEN_FILES=(
  aiven.io_opensearches.yaml
  aiven.io_serviceintegrations.yaml
  aiven.io_valkeys.yaml
)

CLOUDNATIVE_PG_VERSION="v1.30.0"
CLOUDNATIVE_PG_BASE_URL="https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/refs/tags/${CLOUDNATIVE_PG_VERSION}/config/crd/bases/"
CLOUDNATIVE_PG_FILES=(
  postgresql.cnpg.io_clusters.yaml
  postgresql.cnpg.io_databaseroles.yaml
  postgresql.cnpg.io_poolers.yaml
  postgresql.cnpg.io_scheduledbackups.yaml
)

BARMAN_PLUGIN_VERSION="v0.6.0"
BARMAN_PLUGIN_BASE_URL="https://raw.githubusercontent.com/cloudnative-pg/plugin-barman-cloud/refs/tags/${BARMAN_PLUGIN_VERSION}/config/crd/bases/"
BARMAN_PLUGIN_FILES=(
  barmancloud.cnpg.io_objectstores.yaml
)

FQDN_NETWORK_POLICY_VERSION="0.3"
FQDN_NETWORK_POLICY_BASE_URL="https://raw.githubusercontent.com/GoogleCloudPlatform/gke-fqdnnetworkpolicies-golang/refs/tags/${FQDN_NETWORK_POLICY_VERSION}/config/crd/bases/"
FQDN_NETWORK_POLICY_FILES=(
  networking.gke.io_fqdnnetworkpolicies.yaml
)

function download_crd {
  local base_url
  local filename

  base_url=${1}
  filename=${2}

  url="${base_url}/${filename}"
  out="${OUT_DIR}/${filename}"

  echo "Downloading ${url} -> ${out}"
  curl --silent --location --output "${out}" "${url}"
}


for fname in "${AIVEN_FILES[@]}"; do
  download_crd "${AIVEN_BASE_URL}" "${fname}"
done

for fname in "${CLOUDNATIVE_PG_FILES[@]}"; do
  download_crd "${CLOUDNATIVE_PG_BASE_URL}" "${fname}"
done

for fname in "${BARMAN_PLUGIN_FILES[@]}"; do
  download_crd "${BARMAN_PLUGIN_BASE_URL}" "${fname}"
done

for fname in "${FQDN_NETWORK_POLICY_FILES[@]}"; do
  download_crd "${FQDN_NETWORK_POLICY_BASE_URL}" "${fname}"
done
