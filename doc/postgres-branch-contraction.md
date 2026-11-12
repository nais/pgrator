# PostgresBranch contraction: deploy independence before retiring the CRD

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
