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
Branches are peers: no reserved `main` name or special treatment of a recovery
branch is introduced. ADRs 0002–0006 use the historical `PostgresInstance`
terminology; their lifecycle, ownership and security decisions remain in
force except where this ADR explicitly changes the name.

The Kubernetes kind rename is not a storage migration. Existing
`PostgresInstance` objects and the old `activeInstance` selection must be
accounted for before rolling out the new controller. Do not delete the old CRD
or existing objects without checking their owners, finalizers and dependent
CNPG/PVC resources; see the session rollout notes for the current transition.
