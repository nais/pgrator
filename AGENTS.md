# AGENTS.md

## Project

pgrator is a Kubernetes operator managing Postgres (Zalando/CNPG), Valkey, and OpenSearch resources on the nais platform. Written in Go using controller-runtime.

## Build & Test

All commands use [mise](https://mise.jdx.dev):

```sh
mise run build          # Build binary
mise run test           # Run tests (needs setup-envtest)
mise run check:lint     # golangci-lint
mise run check:vet      # go vet
mise run fmt            # Format code
mise run generate       # Generate CRDs and DeepCopy
mise run check          # All checks (lint + vet)
```

## Code Generation

CRDs and DeepCopy methods are generated. After changing types in `pkg/api/`:

```sh
mise run generate
```

## Architecture

- `pkg/api/` — CRD types (separate Go module)
- `internal/controller/` — Reconcilers (Postgres, Valkey, OpenSearch)
- `internal/controller/resourcecreator/` — Builds child K8s objects
- `internal/synchronizer/` — Generic reconcile loop + action system
- `internal/golden/` — Golden-file test harness
- `charts/pgrator/` — Helm chart

## Conventions

- Reconcilers return `[]action.Action` — never mutate K8s directly
- Golden tests: add a directory under `internal/controller/testdata/{resource}/{case}/` with `object.yaml` and `contains/` or `consists_of/` expected YAML
- Use `Subset` matcher in golden tests to assert key fields without full object match
- Exported fields in `PreparedData` structs use yaml tags for golden test support
- Run `mise run generate` after modifying CRD types or RBAC markers

## Common Pitfalls

- **RBAC in Helm chart**: When adding new resource types to a controller (e.g. PodMonitor), update `charts/pgrator/templates/rbac/role.yaml` — the E2E will fail with `is forbidden` errors otherwise
- **Golden test config**: The controller test suite in `suite_test.go` has config flags (e.g. `PrometheusRulesDisabled`) that gate which actions are produced. Golden test expectations must align with these flags
- **Two Go modules**: Root `/` and `pkg/api/` have separate `go.mod` files — dependency updates and linting run independently for each
