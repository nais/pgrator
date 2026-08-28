# -*- mode: Python -*-

# Tiltfile for pgrator local development
#
# Prerequisites:
#   - kind cluster running (use: mise run dev:setup-cluster)
#   - kubectl context set to the kind cluster
#
# Usage:
#   tilt up           # start dev loop
#   tilt ci           # one-shot: deploy + test + exit

load("ext://helm_resource", "helm_resource", "helm_repo")
load("ext://namespace", "namespace_create")

DEV_CLUSTER_ENGINE = os.getenv("DEV_CLUSTER_ENGINE", "kind")
CONTEXT_NAME = DEV_CLUSTER_ENGINE + "-pgrator"

# ---------------------------------------------------------------------------
# Settings
# ---------------------------------------------------------------------------
allow_k8s_contexts(CONTEXT_NAME)
update_settings(k8s_upsert_timeout_secs=120)

# ---------------------------------------------------------------------------
# 1. Build operator image
# ---------------------------------------------------------------------------
docker_build(
    "pgrator",
    ".",
    dockerfile="Dockerfile",
)

# ---------------------------------------------------------------------------
# 2. External CRDs (Aiven, Google IAM, barman plugin)
#
# postgresql.cnpg.io CRDs are deliberately excluded: the CloudNativePG Helm
# chart installs and owns those, and Helm refuses to adopt resources created by
# kubectl (missing app.kubernetes.io/managed-by + meta.helm.sh annotations).
# The chart also ships CRDs this directory lacks, e.g. ClusterImageCatalog.
# The full directory is still used as-is by envtest in the controller tests.
# ---------------------------------------------------------------------------
local_resource(
    "external-crds",
    cmd="find internal/controller/testdata/external-crds -name '*.yaml' ! -name 'postgresql.cnpg.io_*' -exec kubectl apply --server-side -f {} ';'",
    deps=["internal/controller/testdata/external-crds"],
    labels=["setup"],
)

# ---------------------------------------------------------------------------
# 4. cert-manager
# ---------------------------------------------------------------------------
helm_repo("jetstack", "https://charts.jetstack.io", labels=["infra"])

helm_resource(
    "cert-manager",
    "jetstack/cert-manager",
    namespace="cert-manager",
    flags=[
        "--create-namespace",
        "--set=crds.enabled=true",
        "--timeout=120s",
    ],
    resource_deps=["jetstack"],
    labels=["infra"],
)

# ---------------------------------------------------------------------------
# 5. Prometheus Operator CRDs
# ---------------------------------------------------------------------------
helm_repo("prometheus-community", "https://prometheus-community.github.io/helm-charts", labels=["infra"])

helm_resource(
    "prometheus-crds",
    "prometheus-community/prometheus-operator-crds",
    # Pin the namespace explicitly: without it, Helm uses whatever namespace
    # happens to be ambient in the kubectl context. If that drifts between
    # `tilt up` runs (e.g. after `kubens`), Helm refuses to reconcile the
    # release because the CRDs' meta.helm.sh/release-namespace annotation
    # no longer matches, breaking restarts.
    namespace="default",
    pod_readiness="ignore",
    resource_deps=["prometheus-community"],
    labels=["infra"],
)

# ---------------------------------------------------------------------------
# 5b. CloudNativePG operator
#
# Without this only the CRDs exist, so pgrator's Cluster/Pooler/DatabaseRole
# objects would be created but never reconciled into actual Postgres pods.
# This chart owns all postgresql.cnpg.io CRDs (see the external-crds note).
# ---------------------------------------------------------------------------
helm_repo("cnpg", "https://cloudnative-pg.github.io/charts", labels=["infra"])

helm_resource(
    "cloudnative-pg",
    "cnpg/cloudnative-pg",
    # nais-system, not cnpg-system: the generated NetworkPolicy only allows
    # operator ingress from namespace nais-system, matching production.
    namespace="nais-system",
    flags=[
        "--create-namespace",
        "--timeout=300s",
    ],
    resource_deps=["cnpg"],
    labels=["infra"],
)

# ---------------------------------------------------------------------------
# 6. Prerequisite namespaces
# ---------------------------------------------------------------------------
namespace_create("serviceaccounts")
namespace_create("nais-cnpg-wal-storage")
namespace_create("cnpg-team", labels=["google-cloud-project: test-project"])
k8s_resource(
    objects=["serviceaccounts:namespace", "nais-cnpg-wal-storage:namespace", "cnpg-team:namespace"],
    new_name="namespaces",
    labels=["setup"],
)

# ---------------------------------------------------------------------------
# 7. ClusterRole
# ---------------------------------------------------------------------------
local_resource(
    "postgres-pod-clusterrole",
    cmd="kubectl create clusterrole postgres-pod-additional --verb=get,list --resource=pods --dry-run=client -o yaml | kubectl apply -f -",
    labels=["setup"],
)

# ---------------------------------------------------------------------------
# 7a. Node placement
#
# Postgres and pooler pods carry the GKE nodeSelector cloud.google.com/
# compute-class=n4-machines. Label the kind node so they can schedule.
# The dedicated=postgres toleration needs no matching taint.
# ---------------------------------------------------------------------------
local_resource(
    "node-placement-labels",
    cmd="kubectl label nodes --all cloud.google.com/compute-class=n4-machines --overwrite",
    labels=["setup"],
)

# ---------------------------------------------------------------------------
# 7b. ClusterImageCatalog
#
# pgrator references this catalog by name (cnpg.imageCatalogName) and resolves
# the image from spec.majorVersion. Without it the Cluster cannot pick an image.
# We use the "standard" images: unlike "minimal" they bundle pgaudit, which is
# required because pgaudit.log is always set in the generated parameters.
# ---------------------------------------------------------------------------
local_resource(
    "cluster-image-catalog",
    cmd="""kubectl apply --server-side -f - <<'EOF'
apiVersion: postgresql.cnpg.io/v1
kind: ClusterImageCatalog
metadata:
  name: cloudnative-image-catalog
spec:
  images:
    - major: 18
      image: ghcr.io/cloudnative-pg/postgresql:18-standard-trixie
    - major: 17
      image: ghcr.io/cloudnative-pg/postgresql:17-standard-trixie
    - major: 16
      image: ghcr.io/cloudnative-pg/postgresql:16-standard-trixie
EOF""",
    resource_deps=["cloudnative-pg"],
    labels=["setup"],
)

# ---------------------------------------------------------------------------
# 8. pgrator operator (Helm)
# ---------------------------------------------------------------------------
helm_resource(
    "pgrator",
    "./charts/pgrator",
    namespace="pgrator-system",
    flags=[
        "--create-namespace",
        "--set=development=true",
        "--set=controllerManager.container.image.repository=pgrator",
        "--set=controllerManager.container.image.tag=latest",
        "--set=google.projectId=test-project",
        "--set=aiven.project=test-project",
        "--set=aiven.projectVPCID=test-vpc",
        "--set=aiven.metricsDestinationEndpointID=test-endpoint",
        "--set=fasit.tenant.name=test-tenant",
        # No Config Connector in kind, so leave the WAL bucket prefix empty. That
        # switches off the whole Google path (bucket, IAM, Workload Identity,
        # barman-cloud) instead of failing on a missing project ID.
        "--set=cnpg.walBucketPrefix=",
        "--set=cnpg.walBucketNamespace=",
        # kind has no hyperdisk-balanced; empty means "use the cluster default"
        # (and is a known key in minimumDiskPerStorageClass).
        "--set=cnpg.storageClass=",
        # kube-apiserver ClusterIP in kind, so the CNPG instance manager is
        # allowed egress to the API server by the generated NetworkPolicy.
        "--set=apiServerIP=10.96.0.1/32",
    ],
    image_deps=["pgrator"],
    image_keys=[("controllerManager.container.image.repository", "controllerManager.container.image.tag")],
    resource_deps=["cert-manager", "prometheus-crds", "cloudnative-pg", "cluster-image-catalog", "node-placement-labels", "postgres-pod-clusterrole", "external-crds", "namespaces"],
    labels=["app"],
)

# ---------------------------------------------------------------------------
# 9. E2E tests (manual trigger)
# ---------------------------------------------------------------------------
running_ci = config. tilt_subcommand == "ci"
chainsaw_cmd = "chainsaw test --test-dir tests/e2e/ --config .chainsaw.yaml"
if running_ci:
    chainsaw_cmd += " --report-format XML --report-name chainsaw-report"
local_resource(
    "e2e-tests",
    cmd=chainsaw_cmd,
    deps=["tests/e2e", ".chainsaw.yaml"],
    resource_deps=["pgrator"],
    auto_init=running_ci,
    labels=["test"],
)
