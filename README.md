# pgrator

Kubernetes operator for the [nais](https://nais.io) platform that manages **Postgres**, **Valkey**, and **OpenSearch** resources. It reconciles opinionated nais CRDs into the resources needed to run these services on GCP.

## Managed resources

| CRD               | API group    | Backend                                   | Creates                                                                                  |
|-------------------|--------------|-------------------------------------------|------------------------------------------------------------------------------------------|
| `Postgres`        | `nais.io/v1` | [CloudNativePG](https://cloudnative-pg.io) | Logical database; creates the default `PostgresBranch` |
| `PostgresBranch`  | `nais.io/v1` | [CloudNativePG](https://cloudnative-pg.io) | Independent CNPG `Cluster`, `Pooler`, network and optional WAL archive/backup resources |
| `PostgresBinding` | `nais.io/v1` | [CloudNativePG](https://cloudnative-pg.io) | Workload database roles, connection/certificate Secrets and NetworkPolicies |
| `PostgresAccess`  | `nais.io/v1` | [CloudNativePG](https://cloudnative-pg.io) | Time-limited personal role, credentials, relay mapping and database ingress policy |
| `Valkey`          | `nais.io/v1` | [Aiven](https://aiven.io)                  | Aiven Valkey instance + ServiceIntegration (metrics) |
| `OpenSearch`      | `nais.io/v1` | [Aiven](https://aiven.io)                  | Aiven OpenSearch instance + ServiceIntegration (metrics) |

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

`Postgres` is the logical database. Pgrator creates its default `main` `PostgresBranch`; each branch is an independent, writable CNPG cluster with its own data history. A recovery branch uses an immutable `bootstrap.recovery.sourceBranch` (local name) and UTC `targetTime` to restore from another branch's archive. Recovery does not merge data or switch the active branch.

If CNPG reports an unrecoverable recovery Cluster and a permanently failed `full-recovery` Job controlled by that Cluster, pgrator persists `RecoveryFailed=True` with reason `Unrecoverable`, the Cluster identity, original target, and CNPG phase/reason. CNPG can report `Initialized=True` before recovery finishes, so initialization alone does not prove success. Jobs are observed read-only; pgrator neither owns nor retries them. It also sets `Ready=False` and `reconcilePhase: Failed`. This terminal condition stops resource reconciliation without pruning the failed Cluster or its supporting resources, even if the source archive disappears. Deleting the failed Cluster does not retry recovery. To request another recovery, create a new branch with an explicit target; pgrator never adjusts the timestamp automatically. Ordinary readiness failures and later outages without recovery-job failure are not terminal recovery failures; branches with a recorded `RecoveryCompleted` milestone retain their successful status. Failed branches cannot be activated, but can be deleted subject to the normal branch lifecycle protections; source archives remain protected until dependent branches are deleted or detached.

Branch names are local to a Postgres: `Postgres.spec.activeBranch`, `status.activeBranch` and recovery `sourceBranch` use local names. Kubernetes `PostgresBranch.metadata.name` is a deterministic, hashed object name from `v1.PostgresBranchObjectName(postgres, branch)`; `spec.postgres` and `spec.branchName` must match and are immutable. `PostgresAccess.spec.postgresBranch` refers to this **object name**, not the local name. See [ADR 0007](doc/adr/0007-name-physical-postgres-resources-branches.md).

`spec.activeBranch` requests a selection; `status.activeBranch` records the branch pgrator has selected. Without an explicit request, `main` is selected initially and an existing selection is retained. For an explicit request, pgrator checks that the branch identity matches and neither it nor its CNPG cluster is terminating, and that the cluster is initialized and has a Ready condition. An unready request fails reconciliation instead of updating observed status. Bindings without an explicit branch follow the observed selection, not a pending request. **Do not treat that status gate as an atomic cutover guarantee:** credential publication and workload reconnection are asynchronous, and admission and reconciliation are not atomic with branch deletion. Verify dependent bindings and connections during activation.

`PostgresBinding` supplies workloads with a stable logical connection Secret for the selected branch. An Application or Naisjob can set `spec.uses.postgres[].branch` to an existing local branch name; Naiserator copies it to `PostgresBinding.spec.branch`. Omitting it follows the observed active branch. Explicit `branch: main` pins `main`, even after activation of another branch. Missing or terminating explicit branches wait without fallback, and stale connection Secrets/network access are withdrawn. Branch selection does not create a branch, change Secret/mount names or enable multiple branches of the same Postgres in one workload. Inactive branches may be deleted even while workloads or bindings reference them, provided another non-terminating branch remains. The CLI warns and lists the referring apps/jobs before confirmation, including with `--yes`; deleting a selected branch makes those connections unavailable without fallback. Create/delete/activate requests through the API are recorded in the activity log. Active/requested branches and recovery archives still needed for restore remain protected. Delete the whole Postgres to remove its last branch. Once another branch is active, deleting inactive `main` does not silently create a new empty `main`, even if the activation request is later omitted. Branches must be created and cleaned up separately; there is no branch TTL.

Rollout order for branch selection: publish the shared API types; install the updated pgrator `PostgresBinding` CRD/controller and liberator workload CRDs; deploy the branch-aware API; then deploy Naiserator using the new liberator dependency. For the deletion-policy change, make the warning-capable CLI available before removing reference guards in the API/controller. API clients should query `PostgresBranch.workloads` before deleting; older clients can delete without displaying the warning. Only then use `branch` in workload manifests. Older CRDs can prune the field and older Naiserator versions can ignore it, causing a workload to follow the active branch instead of the intended branch. No new Helm values or feature flags are required.

#### Workload private-key formats

Each `PostgresBinding` connection Secret contains `ca.crt` and, for every requested
credential (`admin`, `read`, `readwrite`), `<credential>.tls.crt`,
`<credential>.tls.key` (the original PEM private key), and `<credential>.tls.pk8`
(the **same key** encoded as unencrypted PKCS#8 DER for pgJDBC). PKCS#8 is a key
format, not a different key algorithm; EC certificates continue to use EC keys.
Both representations are published in the same validated snapshot and rotate
with the client certificate. Incomplete or invalid source material does not
replace the last complete snapshot for the same branch.

Naiserator owns the workload mount paths and exposes a role-prefixed `PGJDBCURL`
using the DER file. Existing PEM files and `PGSSLKEY` remain unchanged.

#### Personal access

`PostgresAccess` selects one branch independently of the active workload branch. It creates a short-lived password, a CNPG `DatabaseRole` (PostgreSQL role name is the authenticated email), an owned `RelayAccess` and token Secret, and a database-ingress policy. Its Ready condition requires the role applied at the current generation, a persisted token and the **owned** relay mapping's published endpoint; Ready does not establish SQL connectivity. The relay operator publishes the public endpoint after persisting its egress policy, and pgrator copies that endpoint into `PostgresAccess.status.relayEndpoint` for the API's owner-only connection response. Expiry removes access resources while CNPG's `ReclaimPolicy: Retain` preserves the PostgreSQL role and its objects. Existing roles created under older naming are not migrated or re-owned automatically. See [ADR 0005](doc/adr/0005-personal-postgres-access.md) for identity history and [ADR 0006](doc/adr/0006-relay-backed-personal-postgres-access.md) for relay transport.

The [generated CRD documentation](doc/README.md) describes public fields; `PostgresBranch` is currently an internal API kind, not a user-authored manifest.

`spec.accessLevel` supports `read`, `readwrite`, `readwritecreate` and `dba`.
The personal-only `dba` level grants direct membership in PostgreSQL's
`pg_monitor` and `pg_maintain` roles, without application data privileges,
database ownership or superuser privileges. It allows server-wide monitoring
and maintenance across databases in the selected PostgreSQL cluster, not just
the application schema. Monitoring can expose sensitive query text, and
maintenance can acquire locks that affect availability. `pg_maintain` requires
PostgreSQL 17 or newer. This level is not available to applications through
`PostgresBinding`; their `admin` credential is unchanged.

#### Why every branch has a `-personal-access` DatabaseRole

The PostgresBranch controller creates a CNPG `DatabaseRole` named
`<cluster-name>-personal-access` for **every branch**, even when nobody has
requested personal access. This is a branch-owned authentication group, not a
`PostgresAccess` or a personal user. Its SQL name is `nais_pa_<branch-hash>`;
it has `NOLOGIN`, no password and no database privileges.

When a person requests a `PostgresAccess`, that controller creates the user's
DatabaseRole and grants membership in:

- the branch's authentication group, allowing TLS/SCRAM password authentication
  through the Cluster's `pg_hba` rule; and
- the selected privilege group (`app_read`, `app_readwrite` or
  `app_readwritecreate`), or directly in `pg_monitor` and `pg_maintain` for
  `dba`, determining what the user may do in the database.

The authentication group must be unique per branch because recovery copies
SQL roles, memberships and passwords from the source. The restored cluster's
`pg_hba` rule requires its **own** group, so copied personal credentials do not
automatically grant password access to the destination. A new `PostgresAccess`
targeting that branch grants the required membership.

Unlike the privilege groups created by `postInitSQL`, this group is managed by
a separate DatabaseRole: bootstrap SQL runs only for `initdb`, not recovery.
Putting it only in bootstrap SQL would copy the source group during restore
without creating the destination group. The DatabaseRole covers new, existing
and recovered clusters and is controller-owned by the branch with
`ReclaimPolicy: Delete`; the user's access-owned DatabaseRole uses `Retain`.

An event such as `Updated DatabaseRole ...-personal-access` concerns this group,
not a personal access request. In versions that update children unconditionally,
it can also appear when the group configuration is unchanged.

Implementation entry points:
- [`createPersonalAccessGroupRole`](internal/controller/postgresbranch_controller.go)
  creates the branch-owned group.
- [`PersonalAccessRole` and `CreateCluster`](internal/resourcecreator/cnpg/cnpg.go)
  define its SQL name and authentication rule; `postInitSQL` defines the bootstrap
  privilege groups.
- [`CreateDatabaseRole`](internal/resourcecreator/access/access.go) defines the
  personal user's memberships.

## Dashboards

The [Postgres dashboards](dashboards/README.md) have component-owned Jsonnet sources,
generated Grafana resources and a separate, credential-free validation workflow.
Run `make -C dashboards install check`; operator tooling and cluster access are not
required. [Manual Git Sync setup](dashboards/MIGRATION.md) documents the central
connection to `main`. No provisioning Job or Helm release is needed.

## CI/CD

GitHub Actions runs the repository's mise checks and tests and publishes image and Helm chart artifacts for non-Dependabot branch pushes. Deployment via Fasit remains restricted to `main`. Changes confined to `dashboards/**`, its workflow and existing ignored Markdown paths skip this pipeline and use `Dashboard checks` instead.

Pull requests also run E2E tests in a [kind](https://kind.sigs.k8s.io/) cluster using [Chainsaw](https://github.com/kyverno/chainsaw), via the `mise run test:ci` task. The same dashboard-only exclusion applies; mixed operator/dashboard changes still run E2E.

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
