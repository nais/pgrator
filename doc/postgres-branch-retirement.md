# Postgres branch API retirement

**Work in progress:** these branches publish the contract-retirement changes for
review, not as the finished rollout candidate. Complete the internal renderer and
type cleanup below, including removal of `LegacyPostgresTypeMeta`, on the same
branches before treating them as ready to deploy. No extra intermediate release
or Console rollout is required. Final rollout order is API, then pgrator; live
CRD deletion remains a separately authorized operation.

## Preconditions and scope

The operator and API releases that no longer use the Kubernetes branch API are
deployed everywhere. The branch-free Console release is merged and rolling out.
Do not deploy the breaking GraphQL change until that Console is running everywhere.

This change retires the published Kubernetes and GraphQL branch contracts. It
does not delete any live CRD or resource, change physical identities, or remove
the durable ownership/UID journal. An unfinished adoption is still rejected,
including when the parent is deleting.

### Published contracts

- The pgrator chart no longer publishes `postgresbranches.nais.io` or grants
  permissions for it, including its status/finalizer subresources.
- Code generation must not recreate the retired production CRD. A regression
  checks both generated/chart paths and the rendered Helm resources.
- GraphQL no longer exposes PostgresBranch reads, virtual-main relations,
  branch inventory, branch access input, or PostgresBranchIssue. PostgresIssue
  remains, and historical branch activity entries and their scalar data remain.
- The legacy PBR node resolver is no longer registered. Postgres node IDs and
  direct database fields are unchanged.
- gRPC database inventory converts Postgres directly. Its protobuf contract and
  database identities (`name`, database `app`, environment/team/type) are unchanged.
- Console's generated schema and query regression cover the contracted schema.

### Internal cleanup still outstanding

Retiring these contracts is not the same as purging every branch-named helper.
The operator's direct physical renderer still uses the old branch-shaped Go
identity. The type no longer generates a CRD, but the historical controller
tests still need it and a test-only archived CRD under `testdata/legacy-crds`.
The normal production manifest directories do not contain this fixture. The
isolated direct-controller envtest explicitly verifies the branch CRD is absent.

The API also still has internal main-view/readiness helpers and legacy physical
reference decoding for immutable personal accesses. These must be disentangled
in a further internal-only refactor; do not describe them as already removed.
In particular, never drop support for existing immutable `spec.postgresBranch`
access references merely because the public branch graph has been removed.

Keep the renderer refactor separate from any changes to physical specs. Golden
and lifecycle tests must continue to preserve cluster/pooler/role/certificate,
network, IAM, bucket/archive names, metadata and owner UIDs. Historical `main`
name components and metric labels are durable physical identities, not evidence
that the branch Kubernetes API must remain.

## Live CRD deletion is a separate, explicitly authorized operation

1. Verify the branch-independent pgrator and API are deployed in every target
   environment, and verify completed adoption checkpoints for all databases.
2. Read all remaining PostgresBranch objects across all namespaces. Any remaining
   object, finalizer, non-main database, or unfinished journal requires investigation.
3. Check physical resources for stale PostgresBranch controller owner references.
   Never remove finalizers or overwrite ownership merely to make this check pass.
4. Deploy the chart that no longer publishes the retired CRD, so a later Helm
   upgrade cannot recreate it.
5. Obtain explicit authorization before deleting `postgresbranches.nais.io`.
   Deleting a CRD also deletes its remaining custom resources and is not harmless
   schema cleanup. The old chart's `helm.sh/resource-policy: keep` means removing
   its template does not by itself delete the installed CRD.
6. Validate database status, workload/personal access, WAL/backup operation, and
   operator cache readiness after retirement. Application redeployment is not
   part of this procedure. Do not roll back to a branch-owning binary.

No live cluster operation is performed by this code change.
