# Postgres point-in-time recovery

## Scope

PITR creates a new, independent Postgres from an existing source in the same team
and environment. The caller supplies a distinct new name and an explicit UTC
target time. It does not modify the source, activate a branch, or reconnect any
applications. Disaster recovery after source deletion and cross-environment
recovery are outside this first version.

Console has two entry points into the same create flow: **Restore to new
database** on a database, and recovery mode in **Create Postgres**. Both use
`createPostgres` with optional `recovery { sourcePostgres, targetTime }`.

## Existing implementation reused

- API `internal/persistence/postgres/recovery.go` retains the Barman ObjectStore
  `firstRecoverabilityPoint` lookup and the older CNPG Cluster fallback. It no
  longer needs a virtual branch to find the physical cluster.
- The CNPG resource creator retains the recovery bootstrap and separate source
  archive / destination WAL archive model.
- Recovery failure detection, completed-cluster UID checkpoints, waiting for
  IAM infrastructure, and waiting for an own backup before detaching source
  inputs follow the branch-era recovery protocol.
- The Console UTC parsing and earliest-minute rounding follow the previous
  branch creation form, without restoring branch APIs or automatic activation.

## Immutable origin, not a repeatable command

`Postgres.spec.recovery` may only be set at creation and cannot be changed or
removed. The public GraphQL input contains a source name and target time only.
The API resolves the source in the destination team/environment and pins the
source Postgres UID, physical server name, and Barman archive name internally.
Pgrator validates source ownership and the source's archive UID checkpoints
before using it. Source database name reuse cannot redirect recovery.

This changes the placement described in ADR 0004: Postgres now owns one physical
database directly, so an immutable initialization description belongs on the new
Postgres itself. It still must never mean "restore this existing database again".

The requested major version must match the source. Unspecified resource requests,
extensions and availability configuration inherit from the source; explicitly
requested disk size cannot be smaller and source extensions cannot be omitted.
Console initially always inherits this configuration during recovery.

The destination has its own ownership/UID journal, cluster, access group and WAL
archive. Recovered databases use a UID-bound personal authentication group, so
reusing a name cannot activate an old group restored from an ancestral backup;
existing ordinary databases retain their historical authentication identity.
Lost or replaced checkpointed resources are not recreated. A confirmed
failed CNPG recovery job produces a persisted terminal failure; source and target
time remain unchanged. Deleting a failed destination does not require the source
or recovery inputs to remain available.

A source cannot finish deletion while any surviving recovery still needs its
archive. A destination becomes independent only after it has a successful own
backup, its cluster UID is checkpointed as recovered, and the actual cluster no
longer references the source archive. Temporary source read permissions are
revoked after that detach; unlike data resources, these grants are not durable
entries in the destination's ownership inventory.

## Timestamp limits

Both API and Console reject targets before the known earliest restore time and
future targets. There is deliberately no promise that all WAL up to wall-clock
"now" is archived. A recent explicit PITR can therefore fail. A server-derived
latest recoverable timestamp and archive-continuity guarantees are deferred.

## Publication and rollout

No live rollout is part of this implementation.

1. Publish the changed `pgrator/pkg/api` module and pin that version in the API
   before merging the API change. During local coordinated development, an
   external Go workspace can use the local API module without committing a
   machine-local `replace` directive.
2. Roll out the new pgrator CRD **and running controller** before exposing PITR
   through the API. An older controller ignores recovery intent and must never
   receive these requests.
3. Roll out API, then Console. API create uses strict field validation to reject
   an old CRD rather than allow unknown recovery fields to be silently pruned.
4. Perform a real restore rehearsal with known before/after writes and verify
   the source is unchanged, the destination reflects the target time, new
   connections work, and the destination has its own successful backup.

Local unit, API integration, CRD admission, and renderer tests do not replace a
restore rehearsal against the actual Barman/GCS archive.

## Local Console schema

Until the new API is deployed, generate the Console schema from this feature's
local API contract. Houdini also pulls schema on Vite build startup, even with a
zero polling interval: running a bare build against the old proxy endpoint can
overwrite the new generated schema. Override `VITE_SCHEMA_ENDPOINT` per validation
command with a local endpoint serving the new contract; do not hand-edit the
generated schema or change the user's persistent environment configuration.
