#!/usr/bin/env bash
#MISE description="Generate CRD manifests and copy to Helm charts"
set -euo pipefail

controller-gen crd rbac:roleName=manager-role webhook paths="./pkg/api/..." output:crd:artifacts:config=config/crd/bases
cp config/crd/bases/*.yaml charts/pgrator/templates/crd/
for f in charts/pgrator/templates/crd/*.yaml; do
  yq e -i 'select(.kind=="CustomResourceDefinition") | .metadata.annotations."helm.sh/resource-policy"="keep"' "$f"
done
