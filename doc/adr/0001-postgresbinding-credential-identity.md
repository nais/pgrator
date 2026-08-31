---
status: proposed
---

# Let the workload choose PostgresBinding credential identity

Naiserator owns the name of the client-certificate Secret it mounts into a workload. A PostgresBinding therefore carries an explicit, deterministic credential identity supplied by naiserator; pgrator uses that identity for the CloudNativePG DatabaseRole resource. CloudNativePG 1.30 derives its generated Secret as `<DatabaseRole.metadata.name>-client-cert`, so until CNPG supports configuring the Secret name, the DatabaseRole name is the contract naiserator uses when constructing the workload's volume reference. This avoids making naiserator watch a PostgresBinding status merely to learn a name it must already put into its Deployment spec.

The credential identity is derived from the full `(namespace, postgres, workload, role)` tuple. It includes `role` because one workload can validly have separate admin and readwrite bindings to the same Postgres instance. The generated DatabaseRole resource name must be no more than 51 DNS-label characters, leaving room for CNPG's `-client-cert` suffix within Kubernetes' 63-character Secret-name limit. Long names use readable, length-bounded prefixes plus a stable hash of the unshortened tuple; pgrator must not independently truncate postgres and workload names because that can collide.

Every binding, including `admin`, creates a DatabaseRole and receives its own CNPG-managed client-certificate Secret. For `admin`, the DatabaseRole configures the existing database-owner role `app`; it does not join either shared read or readwrite group role. At most one admin binding may target a given `(namespace, postgres)` pair, preventing multiple DatabaseRole resources from concurrently declaring the same PostgreSQL `app` role. This invariant is enforced when a PostgresBinding is admitted.

When CNPG implements configurable client-certificate Secret names, PostgresBinding will carry the Secret name naiserator wants to mount and pgrator will populate CNPG's corresponding field. The DatabaseRole name remains an internal pgrator resource identifier, while the Secret name becomes the stable cross-controller contract.

## Considered Options

- Publish the generated Secret name in PostgresBinding status. Rejected because naiserator would need to watch and wait for a resource it created before it could construct the workload spec.
- Let pgrator derive all names from postgres and workload. Rejected because naiserator needs the name before reconciliation, and independently reimplementing shortening in two controllers creates a fragile cross-controller contract.
- Reuse the database-owner credential for admin bindings. Rejected because it makes admin credentials shared and makes their name controlled by the Postgres resource rather than the binding that consumes them.

## Consequences

- PostgresBinding needs an explicit field for the naiserator-selected credential identity during the CNPG 1.30 transition, and later an explicit client-certificate Secret-name field.
- The PostgresBinding admission webhook needs a namespace-local lookup to enforce the one-admin-binding-per-Postgres invariant.
- The Postgres reconciler must no longer be the owner of the admin DatabaseRole or client certificate once that responsibility moves to PostgresBinding.
