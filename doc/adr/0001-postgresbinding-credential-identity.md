---
status: accepted
---

# Let the workload choose PostgresBinding credential identity

Naiserator owns the complete name of the client-certificate Secret it mounts into a workload. A PostgresBinding therefore carries `spec.secretName`, including the `-client-cert` suffix, and pgrator strips that suffix when naming the CloudNativePG DatabaseRole resource. CloudNativePG 1.30 then appends the suffix again when creating the Secret. This avoids making naiserator watch a PostgresBinding status merely to learn a name it must already put into its Deployment spec.

The credential identity is derived as `<postgres>-<workload>-<role>-client-cert`. It includes `role` because one workload can validly have separate admin and readwrite bindings to the same Postgres instance. Namespace does not need to be part of the name because DatabaseRole and Secret resources are namespace-scoped. Kubernetes resource names and Secret names are DNS subdomains with a maximum length of 253 characters, and CNPG imposes no stricter limit on DatabaseRole metadata names. The Secret name is limited to 253 characters; after removing the 12-character suffix, its DatabaseRole name is at most 241 characters. Postgres and workload names are each at most 63 characters, so their complete names fit without truncation or hashing.

Every binding, including `admin`, creates a DatabaseRole and receives its own CNPG-managed client-certificate Secret. For `admin`, the DatabaseRole configures the existing database-owner role `app`; it does not join either shared read or readwrite group role. Non-admin login roles are distinct: `<workload>-read` and `<workload>-readwrite`. Names longer than PostgreSQL's 63-byte identifier limit retain a readable workload prefix and role suffix with a stable hash between them.

Workload names are unique within a namespace across Applications and Naisjobs, so the workload name and binding role uniquely identify non-admin login roles. The admission webhook rejects duplicate admin bindings for the same Postgres instance. Child resources are created atomically and updated only while the binding remains their controller owner.

The CNPG Cluster name is derived as `pg-<postgres>` and normalized to a DNS-1035 label. A stable hash is appended when normalization changes the name to avoid collisions, or when shortening is needed to satisfy CNPG's 50-character limit. CNPG resources, pods, services, Poolers, selectors, and generated Secrets use that internal cluster name, while the public `Postgres` and `PostgresBinding` names remain unchanged.

The workload reads the server CA directly from CNPG's `<cnpg-cluster>-ca` Secret so CA rotation is propagated automatically. Since that Secret also contains `ca.key`, naiserator must project only the `ca.crt` key into the workload volume; pgrator does not create a binding-specific copy.

When CNPG implements configurable client-certificate Secret names, pgrator will pass `spec.secretName` directly to CNPG instead of deriving the DatabaseRole name from it. The Secret name remains the stable cross-controller contract.

## Considered Options

- Publish the generated Secret name in PostgresBinding status. Rejected because naiserator would need to watch and wait for a resource it created before it could construct the workload spec.
- Let pgrator derive all names from postgres and workload. Rejected because naiserator needs the name before reconciliation, and independently reimplementing shortening in two controllers creates a fragile cross-controller contract.
- Reuse the database-owner credential for admin bindings. Rejected because it makes admin credentials shared and makes their name controlled by the Postgres resource rather than the binding that consumes them.

## Consequences

- PostgresBinding carries the complete naiserator-selected client-certificate Secret name.
- The PostgresBinding admission webhook performs a namespace-local lookup to reject duplicate admin bindings.
- A PostgresBinding spec is immutable because changing any identity field would move ownership between credentials, roles, or workloads; replacement requires creating a new binding.
- The Postgres reconciler must no longer be the owner of the admin DatabaseRole or client certificate once that responsibility moves to PostgresBinding.
