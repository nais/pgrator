#!/usr/bin/env bash
# [MISE] description="Update the external CRDs from config connector in controller testdata. Requires krew plugin eksporter installed, and that the current kubecontext points to a cluster with the CRDs installed."
set -euo pipefail

# Check that the eksporter krew plugin is available.
if ! command -v kubectl-eksporter &>/dev/null; then
  echo "ERROR: kubectl-eksporter not found on PATH." >&2
  echo "Install it via krew:" >&2
  echo "  kubectl krew install eksporter" >&2
  exit 1
fi

# CRDs to export from the current kubectl context.
# Add/remove entries here as managed CRDs change.
CRDS=(
  iampolicymembers.iam.cnrm.cloud.google.com    # GCP IAM policy members
  iamserviceaccounts.iam.cnrm.cloud.google.com  # GCP IAM service accounts
  storagebuckets.storage.cnrm.cloud.google.com  # GCP Storage Buckets
)

OUT_DIR="internal/controller/testdata/external-crds"

# Export each CRD and write it to the output directory.
for crd in "${CRDS[@]}"; do
  echo "Exporting ${crd}..."
  kubectl eksporter crd "${crd}" > "${OUT_DIR}/${crd}.yaml"
done

echo "Done. Config Connector CRDs written to ${OUT_DIR}/"
