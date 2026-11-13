# PostgresBranch contraction: deploy independence before retiring the CRD

The runtime-independent releases described below are now deployed. The next
contract-retirement changes and their explicit remaining internal cleanup are
documented in [postgres-branch-retirement.md](postgres-branch-retirement.md).
The older intermediate rollout notes below are historical, not instructions to
reintroduce the removed branch GraphQL fields or republish the branch CRD.

## Scope of this release

All environments have received the adoption-aware pgrator, and the dev upgrade
test has demonstrated stable physical resources and SQL continuity. This release
removes the **active runtime dependency** on the PostgresBranch Kubernetes API.
It does not delete the CRD, rename physical resources, reset the ownership journal
or introduce a replacement restore design.

### Pgrator

- The registered Postgres controller no longer watches, lists, reads, adopts or
  deletes PostgresBranch objects. Adoption/detach belongs to the previous release.
- Binding and access controllers no longer watch or read the retired resource.
  Omitted/main workload references and immutable legacy main access references
  resolve through Postgres and its observed physical identity.
- New databases provision directly. Previously observed databases without an
  ownership checkpoint, or with an unfinished Adopting journal, fail closed and
  must finish adoption with the previous version first. Existing cluster/resource
  UID guards and explicit staged Postgres deletion remain in effect.
- Physical names, inherited `branch=main` metric labels, role and certificate
  identities, connection endpoints, IAM and archive identities are unchanged.
- Some old Go types and renderer helpers remain private identity adapters. They
  do not require a live branch API and never create synthetic branch objects.
  The old controllers are not registered. Purging every historical symbol/type
  is separate from making the running binaries independent of the CRD.

### API

- No branch informer is registered or required for cache readiness. Loaders,
  inventory, gRPC and issue checks use logical Postgres resources only.
- Database state/configuration and physical log/backup targets come only from
  observed Postgres status. Missing physical status is Progressing; it never
  falls back to retired branch state or guesses an unobserved log target.
- Deprecated main reads, stable main node IDs, issue identities and immutable
  legacy main access references remain compatible through a virtual main view.
  Non-main lookups do not silently select main.
- New personal access resources always use `spec.postgres`. Existing immutable
  `spec.postgresBranch` main references remain readable/resolvable.

## Validation

- Pgrator has an isolated envtest API server which installs the normal CRDs
  **except PostgresBranch**. Actual manager/controller startup, cache watches and
  database provisioning complete without it. Fake-client tests additionally
  reject any branch reads/lists while exercising direct reconciliation/deletion.
- Existing resource/spec preservation, UID replacement guards, IAM fail-closed
  behavior, binding certificate rotation, access proof retention and expiry tests
  remain covered. Obsolete multi-branch/adoption-phase controller tests are
  replaced by direct-lifecycle and unfinished-migration rejection tests.
- API tests reject branch GET/LIST/WATCH while starting the normal watcher bundle
  and exercising inventory, main node resolution, legacy access target resolution,
  fresh access readiness and scoped logs. GraphQL integration tests use direct
  status and retain the main compatibility and isolation contracts.
- API integration tests require Docker; on this workstation the test command
  needs `DOCKER_HOST=unix:///Users/Frode.Sundby/.docker/run/docker.sock` so
  testcontainers uses the existing Docker Desktop context. This is a local test
  environment setting, not a repository/runtime configuration change.

Local validation completed: pgrator generation, both Go module test suites,
controller/synchronizer race tests, build and the complete `mise run check` pass.
API generation, full test suite (including Lua integrations/race), build, go vet,
staticcheck, deadcode and Helm lint pass with `GOWORK=off`. The complete API check
still reports pre-existing G115 findings in vulnerability/webhook code and known
Go/dependency vulnerabilities; those unrelated upgrades are not folded into this
contraction change. No live cluster was modified while implementing this release.

## Rollout and later CRD removal

### Implemented issue model (supersedes the intermediate notes below)

The API now produces real `PostgresIssue` nodes with `postgres: Postgres!`.
The checker reads Postgres status directly and writes `POSTGRES` issue/resource
types keyed by the logical database name. Postgres issue scopes, filters and
facets use that identity. Console queries and presenters use only the new type.
The intermediate `PostgresBranchIssue.postgres` field has been removed.

Issues are a periodically rebuilt snapshot: the existing checker deletes every
row and inserts new randomly identified rows each minute. A durable identity
migration is therefore unnecessary. Old `POSTGRES_BRANCH` rows are converted to
`PostgresIssue` on reads, preserving the row's current ID and resolving its
physical identity to Postgres within the same team/environment. The next checker
refresh replaces these rows with canonical logical identities without duplicates.
New POSTGRES filters and database scopes match the canonical stored keys, so
verify at least one successful new checker refresh before deploying Console.

The old `PostgresBranchIssue` schema type and deprecated enum values remain only
to validate old client documents during rollout; no new API result produces that
type. Older Console clients do not have type-specific selections for the new
issue, so deploy the updated Console promptly after API support is available.
After the branch-free Console is deployed everywhere, remove that legacy schema
alongside the remaining branch GraphQL facade. Historical activity stays intact.

Validation covers checker degradation/recovery/deletion, logical issue scopes,
facets/filters, Node lookup, conversion of old rows, scoped legacy parent lookup,
and refresh without duplicates. Both pending Copilot fixes remain in place.

**Latest decision, superseding the intermediate issue approach below:** retain
Postgres issue reporting but replace the domain/schema model with a real
`PostgresIssue` referencing `postgres: Postgres!`. Do not remove the feature and
do not treat adding `postgres` to `PostgresBranchIssue` as the final model. Current
local changes still implement that intermediate relation and must be adapted.
API support for the new GraphQL type necessarily precedes Console queries for it;
old schema compatibility is only for the client rollout. See the latest section
of the transition checkpoint before continuing implementation after compaction.

Console rollout is prioritized before breaking GraphQL contraction. The current
Console still selected `PostgresBranchIssue.branch`, so removing the branch model
first would invalidate its queries even when no such issues were returned. The
additive preparation is `PostgresBranchIssue.postgres: Postgres!` (with the old
branch relation retained/deprecated). Deploy that API capability, then deploy the
Console which uses direct Postgres fields everywhere. Only after that Console is
available everywhere should the legacy branch GraphQL fields/types be removed in
one coordinated cleanup. Historical activity data and issue identities need not
be renamed merely to stop using the persistence model.

The Console regression scans all current page/component GraphQL documents for
retired branch dependencies, including inline queries. It protects against finding
another hidden UI dependency only after a breaking schema rollout. Pgrator and
the Kubernetes CRD do not require the GraphQL facade; its remaining purpose is
client rollout compatibility.

1. Before deploying this pgrator, confirm adoption has completed for all existing
   databases in each target cluster: journals Managed, original cluster/resource
   UIDs consistent, no unfinished migrations and no remaining branch resources
   or owner references that need investigation. Do not remove a journal to clear
   an error.
2. Deploy the CRD-independent pgrator and API. Either can go first **while the
   branch CRD and old RBAC permissions remain available**. Keep the existing CRD
   template/permissions during this release so old processes are not broken
   before their replacements have started.
3. Validate status/configuration, inventory/issues, logs, workload connections,
   personal access/expiry and automatic WAL/backup operation after the rollout.
   An application redeploy is not required for this change.
4. Once every relevant binary is independent, a separate cleanup change can stop
   publishing the branch CRD/schema and remove obsolete permissions/types. Inspect
   all namespaces and owner references again, and explicitly authorize the final
   CRD deletion. The current Helm CRD has `helm.sh/resource-policy: keep`; simply
   removing its template is not proof that the live CRD was removed, and deleting
   it while still publishing it permits later recreation by Helm.

There is **no automatic CRD deletion** in this release. Do not roll back to the
old branch-owning binary after adoption or retire APIs while an older deployment
still depends on them. A successful old backup does not substitute for validating
new backup/recovery behavior; branchless user-facing restore remains separate work.
