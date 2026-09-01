---
status: accepted
---

# Let the workload choose PostgresBinding credential identity

Naiserator owns the complete name of the client-certificate Secret it mounts into a workload. A PostgresBinding therefore carries `spec.secretName`, including the `-client-cert` suffix, and pgrator strips that suffix when naming the CloudNativePG DatabaseRole resource. CloudNativePG 1.30 then appends the suffix again when creating the Secret. This avoids making naiserator watch a PostgresBinding status merely to learn a name it must already put into its Deployment spec.

The credential identity is derived as `<postgres>-<workload>-<role>-client-cert`. It includes `role` because one workload can validly have separate admin and readwrite bindings to the same Postgres instance. Namespace does not need to be part of the name because DatabaseRole and Secret resources are namespace-scoped. Kubernetes resource names and Secret names are DNS subdomains with a maximum length of 253 characters, and CNPG imposes no stricter limit on DatabaseRole metadata names. The Secret name is limited to 253 characters; after removing the 12-character suffix, its DatabaseRole name is at most 241 characters. Postgres and workload names are each at most 63 characters, so their complete names fit without truncation or hashing.

Every binding, including `admin`, creates a DatabaseRole and receives its own CNPG-managed client-certificate Secret. For `admin`, the DatabaseRole configures the existing database-owner role `app`; it does not join either shared read or readwrite group role. At most one admin binding may target a given `(namespace, postgres)` pair, preventing multiple DatabaseRole resources from concurrently declaring the same PostgreSQL `app` role. This invariant is enforced when a PostgresBinding is admitted.

When CNPG implements configurable client-certificate Secret names, pgrator will pass `spec.secretName` directly to CNPG instead of deriving the DatabaseRole name from it. The Secret name remains the stable cross-controller contract.

## Considered Options

- Publish the generated Secret name in PostgresBinding status. Rejected because naiserator would need to watch and wait for a resource it created before it could construct the workload spec.
- Let pgrator derive all names from postgres and workload. Rejected because naiserator needs the name before reconciliation, and independently reimplementing shortening in two controllers creates a fragile cross-controller contract.
- Reuse the database-owner credential for admin bindings. Rejected because it makes admin credentials shared and makes their name controlled by the Postgres resource rather than the binding that consumes them.

## Consequences

- PostgresBinding carries the complete naiserator-selected client-certificate Secret name.
- The PostgresBinding admission webhook needs a namespace-local lookup to enforce the one-admin-binding-per-Postgres invariant.
- The Postgres reconciler must no longer be the owner of the admin DatabaseRole or client certificate once that responsibility moves to PostgresBinding.
