# AGENTS.md

## Project

pgrator is a Kubernetes operator managing Postgres (CloudNativePG), Valkey, and OpenSearch resources on the nais platform. Written in Go using controller-runtime.

## Build & Test

All commands use [mise](https://mise.jdx.dev):

```sh
mise run build          # Build binary
mise run test           # Test root + pkg/api modules (sets up envtest)
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
- `internal/golden/` — Golden-file parsing and comparison support
- `charts/pgrator/` — Helm chart

## Conventions

- Reconcilers return `[]action.Action` — never mutate K8s directly
- Golden tests: add a directory under `internal/controller/testdata/{resource}/{case}/` with `object.yaml` and `contains/` or `consists_of/` expected YAML
- All Go tests use the standard library `testing` package; do not add Ginkgo/Gomega
- Prefer table-driven tests with `t.Run` when cases share a behavioral contract; use focused tests when setup or behavior differs
- Test observable behavior rather than implementation branches
- Use `Subset` in golden tests to assert key fields without a full object match; use `Equal` when the complete object is the contract
- Exported fields in `PreparedData` structs use yaml tags for golden test support
- Run `mise run generate` after modifying CRD types or RBAC markers

## Common Pitfalls

- **RBAC in Helm chart**: When adding new resource types to a controller (e.g. PodMonitor), update `charts/pgrator/templates/rbac/role.yaml` — the E2E will fail with `is forbidden` errors otherwise
- **Golden test config**: defaults are configured by the `TestGolden*` functions in `internal/controller/suite_test.go`; a case-local `config.yaml` overlays them. Expectations must match the effective config
- **Two Go modules**: Root `/` and `pkg/api/` have separate `go.mod` files. Plain `go test ./...` only tests the current module; use `mise run test` for both
