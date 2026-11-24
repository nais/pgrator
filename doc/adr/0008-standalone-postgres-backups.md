---
status: proposed
---

# Orchestrate standalone Postgres backups with PostgresBackup

Continuous WAL archiving and nightly base backups in a database's Barman bucket
support point-in-time recovery (PITR) within a short, rolling window (2 to 30
days, default 2), configured under `spec.pitr.windowDays`. When a `Postgres` is
deleted, its Barman bucket is deleted with it.

Point-in-time recovery is distinct from a standalone backup:

| Property | Point-in-time recovery (`spec.pitr.windowDays`) | Standalone backup (`PostgresBackup`) |
| --- | --- | --- |
| **Purpose** | Restore to any second within a recent rolling window | Retain a self-contained snapshot over time, restore after deletion, or export |
| **Storage** | Database-owned Barman/WAL bucket | Shared DR bucket per team and environment (in the team's GCP project) |
| **Source deletion** | Deleted with the `Postgres` resource | Survives deletion of both the source `Postgres` and its Barman bucket |
| **Retention** | 2–30 days (default 2), managed by Barman | 1–730 days (up to 2 years), managed per backup by pgrator |

We build standalone backups first as an explicit `PostgresBackup` resource in
`nais.io/v1`. Once on-demand backups, DR bucket copying, and restore are proven
in production, periodic scheduling (`daily`, `weekly`, `monthly`) will be added
under `Postgres.spec.backup` to create `PostgresBackup` resources automatically.

## 1. Resource Model (`nais.io/v1`)

### `PostgresBackup`
A `PostgresBackup` represents one self-contained backup of a `Postgres`
database. It intentionally has **no** `ownerReference` to the source `Postgres`
so that it survives source database deletion.

- **`spec`:**
  - `postgres`: namespace-local source `Postgres` name (immutable).
  - `retentionDays`: number of days (1–730) to retain this backup before
    pgrator deletes it.
- **`status`:**
  - Phase and conditions (`Preparing` → `Running` → `Completed` / `Failed`).
  - Source configuration snapshot (`sourceUID`, `majorVersion`, `extensions`,
    `diskSize`) so that restore validation and bootstrap work after the source
    `Postgres` is gone.
  - CNPG `Backup` metadata (`backupID`, `serverName`, `startedAt`,
    `completedAt`, `beginWal`, `endWal`).
  - DR copy metadata (`location` with bucket and prefix, `sizeBytes`,
    `expiresAt`).

### Future: Periodic Scheduling on `Postgres.spec.backup`
Periodic backups belong to the lifecycle of a live `Postgres` rather than a
separate schedule CRD (if a `Postgres` is deleted, periodic backups for it must
stop). In a later iteration, `Postgres.spec.backup` will accept natural
intervals (`daily`, `weekly`, `monthly`) and `retentionDays`, creating
`PostgresBackup` resources on schedule.

## 2. Backup Execution and Copy Flow

1. **Trigger CNPG `Backup`:**
   For every new `PostgresBackup`, `PostgresBackupReconciler` creates a dedicated
   `postgresql.cnpg.io/v1` `Backup` (`method: plugin`, `barman-cloud`,
   `target: primary`) against the source CNPG `Cluster`, so the backup does not
   wait for `archive_timeout` to make the final WAL segment available. Always
   triggering a fresh `Backup` guarantees an exact snapshot time and a bounded
   WAL range (`beginWal`..`endWal`) archived to the source Barman bucket.
2. **Team DR bucket and Workload Identity:**
   Pgrator reconciles one DR `StorageBucket` per team namespace and environment
   (in the team's GCP project, without bucket lifecycle rules) and a dedicated
   `IAMServiceAccount` bound via GKE Workload Identity. No static keys are used.
3. **Vendor-agnostic copy via Kubernetes `Job`:**
   Once the CNPG `Backup` reaches `completed`, pgrator runs a Kubernetes `Job`
   in the team namespace with read access to the source Barman bucket and write
   access to the team DR bucket:
    - Copies `<serverName>/base/<backupID>/...` (including `backup.info`) and
      the complete WAL range from `beginWal` to `endWal`, with timeline history
      through the backup's timeline, into an isolated prefix in the DR bucket:
     `<postgresBackup-name>/<serverName>/...`.
   - Writes a manifest (`nais-backup.json`) recording checksums, total size,
     PostgreSQL major version, extensions, and disk size.
    - Verifies object sizes and checksums before reporting completion, reading
      the destination back when the storage API does not return a SHA-256 digest.
   - Uses a vendor-agnostic object-storage client rather than cloud-specific
     transfer services.
4. **Interaction with Barman retention:**
   The copy job starts immediately when the CNPG `Backup` completes—at least
   48 hours before the minimum 2-day Barman retention window can expire and a
   subsequent nightly backup triggers Barman cleanup. If source objects are
   missing before the copy finishes, the job fails and `PostgresBackup`
   transitions to `Failed`.
5. **Expiry and deletion:**
   Pgrator owns cleanup rather than GCS-specific bucket lifecycle rules. When
   `now > status.expiresAt` or when a `PostgresBackup` is deleted, a finalizer
   runs a cleanup job that removes the backup's prefix from the DR bucket
   before removing the Kubernetes resource.

## 3. Restore from a Standalone Backup

A new `Postgres` can be initialized from a completed `PostgresBackup` in the
same team and environment, even after the original `Postgres` and its Barman
bucket have been deleted:

1. `Postgres.spec.recovery` references the source `PostgresBackup`.
2. Pgrator validates that the new `Postgres` matches `majorVersion`, has at
   least the `diskSize`, and includes all `extensions` recorded on
   `PostgresBackup.status`.
3. Pgrator creates a temporary `barmanv1.ObjectStore` pointing at the backup's
   isolated prefix in the DR bucket and grants the new database's Google
   Service Account `roles/storage.objectViewer` on the DR bucket during
   bootstrap.
4. The CNPG `Cluster` bootstraps with `bootstrap.recovery` using
   `recoveryTarget.backupID` and `targetImmediate: true`, stopping recovery and
   opening the database as soon as the base backup and WAL up to `endWal` have
   been replayed.
5. Once the new database is up and has completed its first own base backup to
   its own Barman bucket, pgrator detaches the external cluster, removes the
   temporary `ObjectStore`, and revokes the temporary DR bucket IAM grant.

## 4. Export and User Interfaces

- **Export via API:** Authorized team members can download or stream a
  completed `PostgresBackup` (base backup, required WAL, and manifest) through
  the Nais API for off-platform use or tenant-managed archival. All exports are
  audit-logged.
- **Console and CLI:** List `PostgresBackup` resources (timestamp, size,
  status, expiry), trigger on-demand backups, and start restores to a new
  `Postgres` (with periodic schedule configuration added in the follow-up
  iteration).

## Considered Options

- **Keep PITR window under `spec.backup.retentionDays`.**
  Rejected: PITR and standalone backups are distinct mechanisms. Moving PITR to
  `spec.pitr.windowDays` reserves `spec.backup` for standalone backup
  scheduling.
- **Separate `PostgresBackupSchedule` CRD.**
  Rejected: a schedule is bound to the lifecycle of a live `Postgres` and
  should stop automatically if the database is deleted. Periodic scheduling will
  live under `Postgres.spec.backup` once the `PostgresBackup` building block is
  in place.
- **Cron expressions for periodic backups.**
  Rejected: physical base backups only make sense at coarse intervals (`daily`,
  `weekly`, `monthly`), which also lets the platform spread nightly start times
  across databases.
- **Reuse nightly CNPG backups instead of triggering a new `Backup`.**
  Rejected: triggering a dedicated CNPG `Backup` gives an exact on-demand
  snapshot and a self-contained `beginWal`..`endWal` range without waiting for
  the nightly schedule.
- **Google Cloud Storage Transfer Service for copying.**
  Rejected: couples the backup architecture to a GCP-specific service instead
  of a vendor-agnostic object-storage copy in a Kubernetes `Job`.
- **GCS `Custom-Time` bucket lifecycle rules for expiry.**
  Rejected: relies on provider-specific object metadata and can leave
  Kubernetes `PostgresBackup` resources and bucket contents out of sync on
  early deletion. Pgrator-managed expiry and finalizer cleanup keep Kubernetes
  and object storage consistent across providers.
- **Stream backup objects through the `pgrator` controller pod.**
  Rejected: running a `Job` in the team namespace avoids streaming multi-GiB
  backups through `nais-system` and keeps bucket IAM scoped to the team's
  Workload Identity.

## Consequences

- Each standalone backup is self-contained under its own prefix in the team's
  DR bucket and can be restored after the source database is deleted.
- Pgrator manages the full lifecycle of DR backups (creation, copy,
  verification, expiry, and deletion) without cloud-specific lifecycle rules.
- Delivery order:
  1. Move PITR manifest field to `spec.pitr.windowDays` (with backwards
     compatibility if needed) and add the `PostgresBackup` CRD (`pkg/api/v1`).
  2. `PostgresBackup` reconciler against CNPG `Backup`.
  3. Team DR bucket provisioning, copy/verification `Job`, and expiry/cleanup
     `Job`.
  4. Restore of a new `Postgres` from `PostgresBackup`.
  5. Nais API, Console, CLI, and backup export.
  6. Periodic scheduling via `Postgres.spec.backup` (`daily`, `weekly`,
     `monthly`).
