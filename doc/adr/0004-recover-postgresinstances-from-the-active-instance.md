---
status: accepted
---

# Recover PostgresInstances from the active instance

A point-in-time recovery creates a new, independent `PostgresInstance`; it never
modifies an existing instance or automatically changes `Postgres.spec.activeInstance`.
Naiserator creates the new instance, owned by its logical `Postgres`, with an
immutable bootstrap recovery description containing the source instance and UTC
target time. The description is provenance for the physical instance, not a
repeatable command: pgrator uses it to create that instance's CNPG `Cluster`
with `bootstrap.recovery`, and CNPG consumes it only while bootstrapping empty
storage.

The source instance is locked into the new instance at creation time rather than
being resolved from the logical Postgres during reconciliation. This prevents a
later active-instance change from changing an in-flight recovery. Pgrator
derives the source archive from the source instance and does not expose bucket
or CNPG configuration in its API. A recovery instance receives its own GSA,
PVCs, CNPG cluster, archive bucket, and future WAL/base backups. It receives
read access to the source archive only for the recovery; that access is owned by
the recovery instance and is removed with it. The source instance and its
archive remain independently owned and may be deleted under their normal
lifecycle after recovery and any later activation have completed.

Pgrator creates recovery infrastructure before it creates the CNPG cluster, and
waits for Config Connector to report the required GSA and IAM bindings ready.
This avoids starting CNPG recovery before Workload Identity and source-archive
access exist. Once the cluster is initialized and ready, the instance is an
ordinary writable instance. Naiserator performs any later activation explicitly
through the logical Postgres API.

## Considered Options

- Put a transient restore request on `Postgres`. Rejected because a logical
  resource's declarative spec is level-based and cannot safely mean "do this
  once, then forget it".
- Use a separate `PostgresRecovery` operation resource. Deferred: it is useful
  for queueing, cancellation, audit, and retries, but is not needed to model the
  durable physical instance or bootstrap it safely.
- Expose source bucket or CNPG recovery fields directly. Rejected because those
  are pgrator implementation details; naiserator selects a source instance and
  target time only.
