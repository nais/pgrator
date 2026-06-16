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
# 2. External CRDs (CNPG, Aiven, IAM, etc.)
# ---------------------------------------------------------------------------
local_resource(
    "external-crds",
    cmd="kubectl apply --server-side -f internal/controller/testdata/external-crds/",
    deps=["internal/controller/testdata/external-crds"],
    labels=["setup"],
)

# ---------------------------------------------------------------------------
# 3. Zalando CRD (generated from Go code)
# ---------------------------------------------------------------------------
local_resource(
    "zalando-crd",
    cmd="go run ./tests/e2e/setup/install-zalando-crd.go",
    deps=["tests/e2e/setup/install-zalando-crd.go", "go.mod", "go.sum"],
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
    pod_readiness="ignore",
    resource_deps=["prometheus-community"],
    labels=["infra"],
)

# ---------------------------------------------------------------------------
# 6. Prerequisite namespaces
# ---------------------------------------------------------------------------
namespace_create("serviceaccounts")
namespace_create("nais-cnpg-wal-storage")
k8s_resource(
    objects=["serviceaccounts:namespace", "nais-cnpg-wal-storage:namespace"],
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
# 8. pgrator operator (Helm)
# ---------------------------------------------------------------------------
helm_resource(
    "pgrator",
    "./charts/pgrator",
    namespace="pgrator-system",
    flags=[
        "--create-namespace",
        "--set=controllerManager.container.image.repository=pgrator",
        "--set=controllerManager.container.image.tag=latest",
        "--set=google.projectId=test-project",
        "--set=aiven.project=test-project",
        "--set=aiven.projectVPCID=test-vpc",
        "--set=aiven.metricsDestinationEndpointID=test-endpoint",
        "--set=fasit.tenant.name=test-tenant",
        "--set=walGsBucket=test-bucket",
        "--set=cnpg.backupBucket=test-cnpg-bucket",
    ],
    image_deps=["pgrator"],
    image_keys=[("controllerManager.container.image.repository", "controllerManager.container.image.tag")],
    resource_deps=["cert-manager", "prometheus-crds", "postgres-pod-clusterrole", "zalando-crd", "external-crds", "namespaces"],
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
