# pgrator — Architecture

## Project purpose

`pgrator` is a **Kubernetes operator** for the [nais](https://nais.io) platform. It watches four custom resources:

| CRD               | API group    | What it creates                                                                               |
|-------------------|--------------|-----------------------------------------------------------------------------------------------|
| `Postgres`        | `nais.io/v1` | CNPG `Cluster`, `Pooler`, NetworkPolicy, and optional WAL archive and backup resources         |
| `PostgresBinding` | `nais.io/v1` | CNPG `DatabaseRole`, connection and certificate Secrets, and NetworkPolicies                   |
| `Valkey`          | `nais.io/v1` | Aiven `Valkey` CR + `ServiceIntegration`                                                       |
| `OpenSearch`      | `nais.io/v1` | Aiven `OpenSearch` CR + `ServiceIntegration`                                                   |

The operator translates simple, opinionated nais user specs into the full set of cloud-provider resources needed to run those services inside a Kubernetes cluster on GCP.

---

## Detected stack

| Layer              | Details                                                                               | Evidence                                                    |
|--------------------|---------------------------------------------------------------------------------------|-------------------------------------------------------------|
| Language           | Go 1.26                                                                               | `go.mod:3`, `Dockerfile:2`                                  |
| Operator framework | `sigs.k8s.io/controller-runtime` v0.23.3                                              | `go.mod:28`                                                 |
| Kubernetes API     | `k8s.io/api` v0.35.3                                                                  | `go.mod:23-26`                                              |
| Postgres backend   | `github.com/cloudnative-pg/cloudnative-pg` + `plugin-barman-cloud`                    | `go.mod`                                 |
| Aiven backend      | Internal thirdparty types (`internal/thirdparty/aiven/v1alpha1`)                      | types hand-written; no direct module dep                    |
| Google IAM backend | Internal thirdparty types (`internal/thirdparty/google/v1beta1`)                      | `internal/thirdparty/google/v1beta1/*.go`                   |
| Monitoring         | `prometheus-operator/pkg/apis/monitoring` v0.90.1                                     | `go.mod:18`                                                 |
| Config             | `github.com/sethvargo/go-envconfig` v1.3.0                                            | `go.mod:19`, `internal/config/config.go`                    |
| Logging            | `go.uber.org/zap` via `controller-runtime/log/zap`                                    | `cmd/main.go:58`                                            |
| Testing            | Ginkgo v2 + Gomega + `controller-runtime/envtest`                                     | `go.mod:6,17`, `internal/controller/suite_test.go`          |
| Linting            | golangci-lint 2.10.1                                                                  | `.config/mise/config.toml:7`, `.golangci.yml`               |
| Code generation    | `controller-gen` (`sigs.k8s.io/controller-tools` v0.20.1)                             | `go.mod:29`, `.config/mise/config.toml`                     |
| Task runner        | **mise**                                                                              | `.config/mise/config.toml`                                  |
| Container          | Docker / Chainguard static base image                                                 | `Dockerfile`                                                |
| Packaging          | Helm chart                                                                            | `charts/pgrator/`                                           |
| CI/CD              | GitHub Actions                                                                        | `.github/workflows/main.yml`                                |
| E2E testing        | [Chainsaw](https://github.com/kyverno/chainsaw) + kind                                | `.github/workflows/e2e.yml`, `tests/e2e/`                   |
| Git hooks          | [Lefthook](https://github.com/evilmartians/lefthook)                                  | `lefthook.yml`                                              |
| Deployment         | nais fasit (`nais/fasit-deploy`)                                                      | `.github/workflows/main.yml:249`                            |
| Image registry     | Google Artifact Registry (`europe-north1-docker.pkg.dev/nais-io/nais/images/pgrator`) | `charts/pgrator/values.yaml:12`                             |

---

## Project structure

```
pgrator/
├── cmd/
│   ├── main.go            # Operator entry point; wires controllers + webhooks
│   └── docgen/docgen.go   # CLI tool: generates nais/doc reference markdown
├── pkg/api/               # Separate Go module (github.com/nais/pgrator/pkg/api)
│   ├── v1/                # nais.io/v1 CRD types: Postgres, Valkey, OpenSearch (+ webhooks)
│   ├── annotation.go      # Shared annotation constants
│   ├── object.go          # NaisObject interface
│   └── status.go          # BaseStatus (shared by all CRDs)
├── internal/
│   ├── config/            # Env-var based config struct
│   ├── controller/        # Resource-specific reconcilers (Postgres, PostgresBinding, Valkey, OpenSearch)
│   │   └── testdata/      # Golden test data (per-resource test cases)
│   ├── resourcecreator/   # Factories: builds child K8s/Aiven/GCP/CNPG objects
│   ├── synchronizer/      # Generic reconcile loop + action system
│   │   ├── synchronizer.go   # Core Reconcile() implementation
│   │   ├── reconciler/       # Reconciler interface (Prepare/Update/Delete)
│   │   ├── action/           # Action types: Create, Update, Delete, Claim, Unclaim, Recreate, NoOp
│   │   ├── events/           # Kubernetes event recorder wrapper
│   │   ├── ownership/        # Owner annotation management
│   │   └── relatedobjectsmap/# Lookup map for related K8s objects
│   ├── golden/            # Golden-file test harness
│   ├── metrics/           # Custom Prometheus metrics (reconcile counts, errors, durations)
│   ├── namegen/           # Stable short-name generator (hash-based)
│   └── thirdparty/        # Hand-written Go types for external CRDs
│       ├── aiven/v1alpha1/   # Aiven Valkey, ServiceIntegration, OpenSearch
│       └── google/            # Google IAM and Storage Config Connector types
├── config/
│   ├── crd/bases/         # Generated CRD YAML (committed, copied to Helm)
│   └── webhook/           # Webhook manifests
├── charts/pgrator/        # Helm chart for production deployment
├── tests/e2e/             # Chainsaw e2e test cases
├── doc/                   # Documentation templates + generated output (gitignored output/)
├── example/               # Example CRD manifests
├── .config/mise/          # mise configuration and tasks
│   ├── config.toml        # Tool versions + all task definitions
│   └── tasks/             # Specialized file tasks (docker, actions, generate:doc)
└── .github/workflows/     # CI: build-deploy (reusable), e2e (chainsaw)
```

---

## Conventions

### Formatting and linting
- **Formatter**: `gofmt` + `goimports` (enforced via `golangci-lint` formatters section).
- **Linter**: `golangci-lint` v2 with an explicit allow-list: `copyloopvar`, `dupl`, `errcheck`, `ginkgolinter`, `goconst`, `gocyclo`, `govet`, `ineffassign`, `lll`, `misspell`, `nakedret`, `prealloc`, `revive`, `staticcheck`, `unconvert`, `unparam`, `unused`.
- Config: `.golangci.yml`. Run separately for root module and `pkg/api` (two Go modules).
- CI runs `fmt-check` and `generate-check` to verify nothing is out of date.

### Type checking
- No separate type-checker beyond the Go compiler. `govet` + `staticcheck` cover static analysis.
- Interface compliance is asserted at compile time: `var _ reconciler.Reconciler[...] = &XxxReconciler{}` in every controller.

### Testing
- **Framework**: Ginkgo v2 (BDD) + Gomega matchers.
- **Environment**: `controller-runtime/envtest` spins up a real API server + etcd for integration tests.
- **Golden-file tests**: `internal/golden/` — each test case is a directory under `internal/controller/testdata/{resource}/{case}/` containing `object.yaml`, optional `prepared_data.yaml`, and expected action YAML files. Tests assert that `reconciler.Update()` produces exactly (or at least) the expected set of actions.
- Test files follow the standard Go `_test.go` convention, co-located with the package under test.
- Each package with tests has a `suite_test.go` that calls `RunSpecs`.

### Code generation
- `controller-gen` generates `zz_generated.deepcopy.go` (object deepcopy) and CRD YAML manifests.
- Generated files are committed and checked in CI (`ci:generate`).
- CRD YAMLs are auto-copied into `charts/pgrator/templates/crd/` with a `helm.sh/resource-policy: keep` annotation.

### Naming patterns
- Controllers follow the `{Resource}Reconciler` naming (e.g., `PostgresReconciler`).
- Each reconciler implements the generic `reconciler.Reconciler[T, P]` interface.
- Resource creator functions live in `internal/resourcecreator/` and are named `Create{Thing}` / `Minimal{Thing}`.
- Action constructors are verb functions: `action.Create`, `action.Update`, `action.DeleteIfExists`, `action.Claim`, `action.Unclaim`, `action.Recreate`, `action.NoOp`.

---

## Linting and testing commands

### Single "do everything" commands
```sh
# Run all checks (vet + lint)
mise run check

# Run all tests (requires setup-envtest; sets KUBEBUILDER_ASSETS automatically)
mise run test
```

### Individual commands

| Purpose                | Command                        | Source                                     |
|------------------------|--------------------------------|--------------------------------------------|
| Build binary           | `mise run build`               | `.config/mise/tasks/build.sh`              |
| Format code            | `mise run fmt`                 | `.config/mise/tasks/fmt/_default`          |
| Lint (root)            | `mise run check:lint:root`     | `.config/mise/tasks/check/lint/root.sh`    |
| Lint (api)             | `mise run check:lint:api`      | `.config/mise/tasks/check/lint/api.sh`     |
| Lint with autofix      | `mise run check:lint -- --fix` | `.config/mise/tasks/check/lint/_default`   |
| Helm lint              | `mise run check:lint:helm`     | `.config/mise/tasks/check/lint/helm.sh`    |
| Verify lint config     | `mise run check:lint:config`   | `.config/mise/tasks/check/lint/config.sh`  |
| Generate deepcopy      | `mise run generate:objects`    | `.config/mise/tasks/generate/objects.sh`   |
| Generate CRD manifests | `mise run generate:manifests`  | `.config/mise/tasks/generate/manifests.sh` |
| Generate docs          | `mise run generate:doc`        | `.config/mise/tasks/generate/doc.sh`       |
| Run locally            | `mise run run`                 | `.config/mise/tasks/run.sh`                |
| Build Docker image     | `mise run docker:build`        | `.config/mise/tasks/docker/build.sh`       |
| CI: check fmt          | `mise run ci:fmt`              | `.config/mise/tasks/ci/fmt.sh`             |
| CI: check generate     | `mise run ci:generate`         | `.config/mise/tasks/ci/generate.sh`        |

> **Note**: `mise run test` calls `setup-envtest` automatically to resolve `KUBEBUILDER_ASSETS`.
> The K8s version for envtest is derived from the `controller-runtime` version in `go.mod` at tool-install time.

---

## Key dependencies and their roles

| Dependency                                              | Role                                                      |
|---------------------------------------------------------|-----------------------------------------------------------|
| `sigs.k8s.io/controller-runtime`                        | Manager, reconcile loop, envtest, client, webhooks        |
| `k8s.io/api`, `k8s.io/apimachinery`, `k8s.io/client-go` | Kubernetes types and client                               |
| `github.com/cloudnative-pg/cloudnative-pg`              | `Cluster` CRD types used as reconcile target              |
| `prometheus-operator/pkg/apis/monitoring`               | `PrometheusRule`, `ServiceMonitor` CRD types              |
| `github.com/sethvargo/go-envconfig`                     | Struct-tag-based env-var configuration                    |
| `github.com/onsi/ginkgo/v2` + `gomega`                  | BDD test framework                                        |
| `sigs.k8s.io/controller-tools` (controller-gen)         | CRD manifest + deepcopy code generation                   |
| `github.com/imdario/mergo`                              | Struct merging (used in resource creators)                |
| `go.uber.org/zap`                                       | Structured logging (via controller-runtime adapter)       |
| `github.com/sethvargo/ratchet`                          | GitHub Actions pin-hashing tool (CI workflow maintenance) |

---

## Entry points

| Binary / command                 | Source                 | Description                                               |
|----------------------------------|------------------------|-----------------------------------------------------------|
| `manager` (container entrypoint) | `cmd/main.go`          | Kubernetes operator; runs indefinitely                    |
| `go run cmd/docgen/docgen.go`    | `cmd/docgen/docgen.go` | One-shot doc generator; outputs markdown to `doc/output/` |

---

## Configuration (runtime env vars)

All configuration is read at startup by `internal/config/config.go` via `sethvargo/go-envconfig`.

| Env var                                 | Required | Description                                           |
|-----------------------------------------|----------|-------------------------------------------------------|
| `AIVEN_PROJECT`                         | **yes**  | Aiven project name                                    |
| `AIVEN_PROJECT_VPC_ID`                  | **yes**  | Aiven VPC ID                                          |
| `AIVEN_METRICS_DESTINATION_ENDPOINT_ID` | **yes**  | Aiven metrics endpoint                                |
| `TENANT_NAME`                           | **yes**  | Tenant/cluster name                                   |
| `GOOGLE_PROJECT_ID`                     | no       | GCP project for IAM resources                         |
| `METRICS_CERT_PATH`                     | no       | Dir containing `tls.crt`/`tls.key` for metrics server |
| `DRY_RUN`                               | no       | Enable dry-run mode on the K8s client                 |
| `LEADER_ELECTION_ENABLED`               | no       | Enable leader election                                |
| `RESYNC_IAM_PERMISSIONS`                | no       | Allow recreating IAMPolicyMember resources            |

Helm chart exposes most of these via `charts/pgrator/values.yaml`.

---

## Do and don't patterns

### Do

| Pattern                                                                                                                                                                    | Evidence                                                                                  |
|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------|
| **Generic reconciler via `Synchronizer[T, P]`**: all three controllers plug into the same generic synchronizer; resource-specific logic is in `Prepare`/`Update`/`Delete`. | `internal/synchronizer/synchronizer.go`, `internal/synchronizer/reconciler/reconciler.go` |
| **Action objects**: all mutations are expressed as `action.Action` values — none executed inline in reconcilers.                                                           | `internal/synchronizer/action/action.go`, `internal/controller/postgres_controller.go`    |
| **Compile-time interface checks**: `var _ reconciler.Reconciler[...] = &XxxReconciler{}` in every controller file.                                                         | `internal/controller/postgres_controller.go:57`, `valkey_controller.go:35`                |
| **Wrapped errors with context**: `fmt.Errorf("...: %w", err)` throughout.                                                                                                  | `internal/synchronizer/synchronizer.go`  |
| **Structured logging via `logr`/`zap`**: no `fmt.Print*` in production paths; uses `ctrl.Log` / `logf.FromContext(ctx)`.                                                   | `cmd/main.go:58`, `internal/synchronizer/synchronizer.go:93`                              |
| **Golden-file tests**: test cases are data-driven YAML directories; adding a test means adding a directory.                                                                | `internal/golden/golden.go`, `internal/controller/testdata/`                              |
| **Ownership via annotations** (not OwnerReferences for cross-namespace resources): `<name>/owner` annotations track multi-owner shared resources.                          | `internal/synchronizer/ownership/ownership.go`                                            |

### Don't

| Anti-pattern                                                                                                                    | Evidence of avoidance                                  |
|---------------------------------------------------------------------------------------------------------------------------------|--------------------------------------------------------|
| **No global mutable state**: config is passed explicitly; `init()` is only used for scheme registration.                        | `cmd/main.go`, `internal/config/config.go`             |
| **No swallowed errors**: `errcheck` linter is enabled; every function that can fail returns an error.                           | `.golangci.yml:9`                                      |
| **No direct inline K8s mutations in reconcilers**: reconcilers only return `[]action.Action`; the synchronizer executes them.   | `internal/synchronizer/reconciler/reconciler.go:40-43` |
| **No ad-hoc deletion without ownership check**: cleanup goes through `action.Unclaim` which only deletes when no owners remain. | `internal/synchronizer/synchronizer.go:383-388`        |

---

## Open questions

1. **`pkg/api` is a separate Go module** (`github.com/nais/pgrator/pkg/api`), replaced via `replace` directive. The reason is not documented — it may allow other repos to import just the CRD types. Confirm before adding new shared code.
2. **Aiven and Google thirdparty types are hand-written** (not generated from upstream CRDs). When upstream CRDs change, these need manual updates. The process for keeping them in sync is not documented.
3. **`ENVTEST_K8S_VERSION`** is derived automatically at `mise` tool-install time from `go.mod` via a template expression in `.config/mise/config.toml`. Confirm this resolves correctly in all environments before running tests.
