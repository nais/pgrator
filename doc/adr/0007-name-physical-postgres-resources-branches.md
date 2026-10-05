---
status: accepted
---

# Name independent Postgres databases PostgresBranch

The logical `Postgres` can own several independent, writable database histories. A
`PostgresBranch` is one concrete database, backed by its own CNPG Cluster,
storage, credentials and archive. It may be created from scratch or recovered
from another branch's archive at a specified time. `Postgres.status.activeBranch`
selects which branch normal workloads use; `PostgresAccess.spec.postgresBranch`
selects the exact branch a person may access. Recovery and changing the active
branch do not merge data or rewrite any other branch.

This renames the `PostgresInstance` resource and its reference fields to
`PostgresBranch`, `activeBranch`, `postgresBranch` and recovery `sourceBranch`.
The resource remains an independently managed physical database, not a CNPG
replica, Git-style mergeable branch, or a different kind of SQL credential.
Branches are peers: `main` is the default branch created with a Postgres, not
a special recovery or activation mode. Local branch names live in
`PostgresBranch.spec.branchName`; `spec.postgres` identifies the logical
Postgres. Both fields are immutable. The Kubernetes object name is derived
from the pair by `PostgresBranchObjectName` and must agree with the spec.
`Postgres.spec.activeBranch`, `status.activeBranch` and recovery `sourceBranch`
use local names; `PostgresAccess.spec.postgresBranch` uses the object name.

`spec.activeBranch` is a request and `status.activeBranch` is observed
selection. Explicit activation waits for the requested branch and its CNPG
cluster to be non-terminating and for the cluster to be initialized and Ready.
This is not an atomic guarantee against concurrent deletion or a promise that
binding updates wait for observed status; the binding reconciler also reads
the requested spec. ADRs 0002–0006 use the historical `PostgresInstance`
terminology; their lifecycle, ownership and security decisions remain in
force except where superseded by the current implementation (see the README).

The Kubernetes kind rename is not a storage migration. Existing
`PostgresInstance` objects and the old `activeInstance` selection must be
accounted for before rolling out the new controller. Do not delete the old CRD
or existing objects without checking their owners, finalizers and dependent
CNPG/PVC resources. Plan any migration against the actual cluster state.
