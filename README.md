# pgrator

Kubernetes operator for the [nais](https://nais.io) platform that manages **Postgres**, **Valkey**, and **OpenSearch** resources. It reconciles opinionated nais CRDs into the full set of cloud-provider resources needed to run these services on GCP.

## Managed resources

| CRD               | API group    | Backend                                   | Creates                                                                                  |
|-------------------|--------------|-------------------------------------------|------------------------------------------------------------------------------------------|
| `Postgres`        | `nais.io/v1` | [CloudNativePG](https://cloudnative-pg.io) | CNPG `Cluster`, `Pooler`, NetworkPolicy, and optional WAL archive and backup resources    |
| `PostgresBinding` | `nais.io/v1` | [CloudNativePG](https://cloudnative-pg.io) | CNPG `DatabaseRole`, connection and certificate Secrets, and NetworkPolicies              |
| `Valkey`          | `nais.io/v1` | [Aiven](https://aiven.io)                  | Aiven Valkey instance + ServiceIntegration (metrics)                                     |
| `OpenSearch`      | `nais.io/v1` | [Aiven](https://aiven.io)                  | Aiven OpenSearch instance + ServiceIntegration (metrics)                                 |

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

# Run unit and integration tests in both Go modules
mise run test

# Run only linting
mise run check:lint
```

### Available tasks

Use `mise tasks` to get a list of available tasks, with descriptions

### Git hooks

[Lefthook](https://github.com/evilmartians/lefthook) is configured for pre-commit (fmt, lint, vet, generate check) and pre-push (tests). Install hooks with:

```sh
lefthook install
```

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed information about project structure, conventions, and design patterns.

### Postgres

The `Postgres` CRD (`nais.io/v1`) provisions a [CloudNativePG](https://cloudnative-pg.io) cluster. The reconciler is being rebuilt greenfield (cert-based auth, PostgreSQL 18); see the type in `pkg/api/v1/postgres_types.go`.

## CI/CD

GitHub Actions runs the repository's mise checks and tests and publishes image and Helm chart artifacts for non-Dependabot branch pushes. Deployment via Fasit remains restricted to `main`.

Pull requests also run E2E tests in a [kind](https://kind.sigs.k8s.io/) cluster using [Chainsaw](https://github.com/kyverno/chainsaw), via the `mise run test:ci` task.

## Contributing

### Development workflow

1. **Install tools** — `mise install` sets up Go, golangci-lint, controller-gen, helm, and all other dependencies at pinned versions.
2. **Make changes** — edit code, CRD types, or Helm chart.
3. **Regenerate** — if you changed types in `pkg/api/`, run `mise run generate` to update CRDs and DeepCopy methods.
4. **Test locally** — `mise run test` runs standard Go tests in both the root and `pkg/api` modules. Controller integration tests use envtest (a real API server + etcd, no cluster needed).
5. **Commit** — lefthook pre-commit hooks run fmt, lint, vet, and generate-check automatically.
6. **Push** — pre-push hook runs tests. CI runs the full matrix in parallel.

### Running operator in local cluster

- `mise run dev:setup-cluster` to start a local cluster.
- `mise run dev:tilt` to use [tilt](https://tilt.dev) to install dependencies into the cluster, and build and install the operator.
- `mise run test:e2e` to run E2E tests in the local cluster.
- `mise run dev:stop-cluster` to stop the cluster.

The cluster uses [kind](https://kind.sigs.k8s.io) by default, but can be changed to any cluster engine supported by [ctlptl](https://github.com/tilt-dev/ctlptl#current).
To select a different engine, create a [local mise config](https://mise.jdx.dev/configuration.html#mise-toml), overriding the env-variable `DEV_CLUSTER_ENGINE` with a ctlptl product name.


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
- `action`: the concrete action type, for example `create`, `createOrUpdate`, or `exclusiveCreateOrUpdate`
- `matcher`: `Equal` (exact match) or `Subset` (only specified fields must match)
- `object`: the expected Kubernetes resource

### Writing Go tests

Use the standard library `testing` package. Prefer table-driven tests with
`t.Run` when several cases exercise the same contract; use a focused `TestXxx`
function when setup or behavior differs materially. Test observable behavior,
not implementation branches. Golden fixtures are the preferred contract tests
for reconciler output.

### Running tests

```sh
mise run test          # Root + pkg/api Go tests; sets up envtest automatically
mise run test:e2e      # E2E tests (requires running mise run dev:tilt in a separate terminal)
mise run test:ci       # Starts a cluster, runs E2E tests
```

`go test ./...` can be used for a single module when `KUBEBUILDER_ASSETS` is
already configured. The mise task is the authoritative full test command because
Go does not traverse into the nested `pkg/api` module automatically.

### Code generation

After modifying types in `pkg/api/` or RBAC markers (`+kubebuilder:rbac`):

```sh
mise run generate          # Regenerate CRDs + DeepCopy + copy to Helm chart
mise run generate-check    # Verify nothing is out of date (CI runs this)
```

## Some code generated with GitHub Copilot

This repository occasionally uses GitHub Copilot to generate code.
