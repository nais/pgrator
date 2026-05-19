# pgrator

Kubernetes operator for the [nais](https://nais.io) platform that manages **Postgres**, **Valkey**, and **OpenSearch** resources. It reconciles opinionated nais CRDs into the full set of cloud-provider resources needed to run these services on GCP.

## Managed resources

| CRD | API group | Backend | Creates |
|-----|-----------|---------|---------|
| `Postgres` | `data.nais.io/v1` | [Zalando postgres-operator](https://github.com/zalando/postgres-operator) or [CloudNativePG](https://cloudnative-pg.io) | Postgres cluster, NetworkPolicy, IAM resources, ServiceAccount, RoleBinding, PrometheusRule |
| `Valkey` | `nais.io/v1` | [Aiven](https://aiven.io) | Aiven Valkey instance + ServiceIntegration (metrics) |
| `OpenSearch` | `nais.io/v1` | [Aiven](https://aiven.io) | Aiven OpenSearch instance + ServiceIntegration (metrics) |

## Getting started

### Prerequisites

- [mise](https://mise.jdx.dev) — manages all tool versions and tasks
- Docker — for building images
- A Kubernetes cluster (for e2e tests)

### Setup

```sh
# Install all tools (Go, golangci-lint, controller-gen, etc.)
mise install

# Run all checks and tests
mise run all

# Run only unit/integration tests
mise run test

# Run only linting
mise run lint
```

### Available tasks

| Task | Description |
|------|-------------|
| `mise run all` | Run all checks, tests, and build |
| `mise run test` | Run tests (requires envtest binaries) |
| `mise run test-race` | Run tests with race detector |
| `mise run test-e2e` | Run Chainsaw e2e tests (requires a running cluster) |
| `mise run lint` | Run golangci-lint |
| `mise run vet` | Run go vet |
| `mise run check` | Run govulncheck |
| `mise run fmt` | Format code |
| `mise run fmt-check` | Check formatting |
| `mise run generate` | Generate CRDs, RBAC, and DeepCopy methods |
| `mise run generate-check` | Verify generated code is up to date |
| `mise run tidy-check` | Verify go.mod is tidy |
| `mise run build` | Build manager binary |
| `mise run helm-lint` | Lint Helm charts |
| `mise run docker` | Build Docker image |
| `mise run run` | Run controller locally |
| `mise run generate:doc` | Generate documentation for nais/doc |

### Git hooks

[Lefthook](https://github.com/evilmartians/lefthook) is configured for pre-commit (fmt, lint, vet, generate check) and pre-push (tests). Install hooks with:

```sh
lefthook install
```

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed information about project structure, conventions, and design patterns.

### Postgres engine selection

The `Postgres` CRD supports two backend engines:

- **Zalando** (default) — uses the [Zalando postgres-operator](https://github.com/zalando/postgres-operator) with Spilo
- **CNPG** — uses [CloudNativePG](https://cloudnative-pg.io) with barman-cloud backups

Engine selection is immutable after creation. The operator detects existing resources and prevents engine changes.

## CI/CD

CI uses the centralized [`nais/actions`](https://github.com/nais/actions) reusable workflow (`mise-build-deploy-fasit.yaml`), which runs all mise tasks in parallel, builds and pushes the Docker image and Helm chart, and deploys via Fasit.

E2E tests run separately in a [kind](https://kind.sigs.k8s.io/) cluster using [Chainsaw](https://github.com/kyverno/chainsaw).

## Contributing

### Development workflow

1. **Install tools** — `mise install` sets up Go, golangci-lint, controller-gen, helm, and all other dependencies at pinned versions.
2. **Make changes** — edit code, CRD types, or Helm chart.
3. **Regenerate** — if you changed types in `pkg/api/`, run `mise run generate` to update CRDs and DeepCopy methods.
4. **Test locally** — `mise run test` runs the full Ginkgo test suite with envtest (a real API server + etcd, no cluster needed).
5. **Commit** — lefthook pre-commit hooks run fmt, lint, vet, and generate-check automatically.
6. **Push** — pre-push hook runs tests. CI runs the full matrix in parallel.

### Adding a new golden test case

Golden tests are data-driven: each test case is a directory under `internal/controller/testdata/{resource}/{case-name}/`:

```
my-test-case/
├── object.yaml           # Input CRD spec to reconcile
├── prepared_data.yaml    # Optional: set engine, projectID, etc.
├── related_objects/      # Optional: pre-existing objects in cluster
├── contains/             # Assert actions contain at least these (use for partial checks)
│   └── cluster.yaml
└── consists_of/          # Assert actions match exactly these (use for full coverage)
    └── cluster.yaml
```

Each expected file in `contains/` or `consists_of/` specifies:
- `action`: `create`, `update`, `createOrUpdate`, `claim`, `recreate`
- `matcher`: `Equal` (exact match) or `Subset` (only specified fields must match)
- `object`: the expected Kubernetes resource

### Running tests

```sh
mise run test          # Integration tests (envtest — no cluster required)
mise run test-e2e      # E2E tests (requires kind cluster with operator deployed)
```

### Code generation

After modifying types in `pkg/api/` or RBAC markers (`+kubebuilder:rbac`):

```sh
mise run generate          # Regenerate CRDs + DeepCopy + copy to Helm chart
mise run generate-check    # Verify nothing is out of date (CI runs this)
```

## Some code generated with GitHub Copilot

This repository occasionally uses GitHub Copilot to generate code.
