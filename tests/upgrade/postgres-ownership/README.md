# Postgres ownership upgrade acceptance test

Run only in a dedicated test environment. This is a real CNPG/data-plane test,
not an envtest simulation. **It has not been run as part of implementation.**
It is deliberately separate from ordinary fresh-install Chainsaw tests: a fresh
install cannot establish continuity of a database created by the old controller.

## Preconditions

- Start with the previous, branch-owning pgrator, the compatible naiserator and
  Application CRD, CNPG and the normal certificate, network and (for the cloud
  test) Config Connector/Barman infrastructure.
- Use a dedicated test namespace with the usual team Google-project label for
  the WAL-enabled case. Do not use a production or shared application database.
- Record the explicit kubecontext and namespace. Check the actual old/new image
  digests and keep leader election enabled on the same `pgrator.nais.io` lease.
- Install the additive CRDs before the new controller image. The new checkpoint
  schema must be available; never remove the PostgresBranch CRD in this release.
- Exercise both an ordinary install and one with WAL archiving enabled.

## Baseline (old binary)

1. Apply `continuity.yaml` in the test namespace. It declares only a disposable
   `Postgres` and a normal `nais.io/v1alpha1 Application` with
   `spec.uses.postgres`. **Naiserator must generate** the Deployment, binding,
   credential references/mounts and workload network policies; pgrator generates
   the connection Secret and database-side access. Do not hand-create bindings,
   Secrets, Pods or network policies to make the test pass.
2. Verify Application synchronization completed and its generated binding is
   Ready, then follow the `ownership-probe` Deployment's logs. The app seeds a
   sentinel and starts two workers: one persistent psql connection reads it every
   second, while the other opens a **new connection and writes** every second.
   Both use only the NAIS-injected PG/TLS configuration. Wait for `BASELINE_READY`
   **and successful SQL output from both workers** before upgrading. Startup-only
   retries are allowed before this measurement window, not afterward.
3. Applications use `restartPolicy: Always`. The fixture therefore latches any
   worker exit in `/tmp/ownership-probe.failed`, stops both workers and logs
   `FAIL`, keeping the container alive for inspection. Kubernetes readiness alone
   is **not** a success signal (this SQL-only probe has no HTTP probes). Record
   the generated Pod UID, container start time and restart count; any Pod
   replacement, container restart, failure marker or SQL error fails the run.
   Never redeploy/restart the app to clear a failed measurement.
4. Confirm that the actual cluster is controlled by PostgresBranch, and record
   Postgres, branch, cluster, Pooler, Database, DatabaseRole, IAM, ObjectStore,
   ScheduledBackup, WAL bucket, PVC, Service and relevant Secret UIDs/specs.
   Include network selectors, PGHOST, role memberships and the two owner
   annotations. Store Secret **hashes**, not plaintext credentials, in test output.
5. Record database and pooler Pod UIDs, restart counts, CNPG system ID, healthy
   state and successful backup/recovery-window evidence. Use the actual resource
   identities; do not reconstruct names in the test.

## Upgrade and interruption

1. Upgrade to the new controller while the probe remains running. Do not redeploy
   the probe, database or binding. Do not issue SQL retries that hide failures.
2. Observe `Postgres.status.ownership`: the legacy branch/resource UIDs must be
   checkpointed before adoption. During `Adopting`, only controller references
   and the two ownership annotations may change on the existing children.
3. In a separate run, interrupt/restart **only pgrator** during partial adoption.
   The next leader must resume from the stored UIDs without recreating resources.
   Unit/envtest tests cover interrupted status writes and partial action replay;
   this run additionally validates the real controllers and garbage collector.
4. Wait for `ownership.phase: Managed`, the original cluster UID in the journal,
   directly published `status.clusterName`/configuration/readiness, and absence
   of the retired main branch. The branch CRD must still exist.

## Acceptance criteria

- No SQL failure from either worker, no `FAIL` or failure marker, unchanged app
  Pod UID and container start time with zero restarts. The sentinel stays readable
  and the write ledger continues growing. Check timestamped SQL output for gaps
  or hangs as well as errors; Running/Ready is not sufficient.
- Cluster, database/pooler Pods, PVCs, Services and credential Secret identities
  are unchanged. No database or pooler restart caused by migration. Compare
  functional specs/labels after adoption, ignoring only server bookkeeping and
  the explicitly transferred controller/ownership annotations.
- Only formerly **direct** branch children change controller to Postgres. CNPG
  descendants and binding/access-owned resources keep their original owners.
- Connection Secrets retain data, hostnames, roles and certificates. Existing
  legacy personal accesses work through their remaining lifetime; new direct
  access also works, and expiry/revoke still completes.
- WAL bucket/IAM/ObjectStore identities and archive path are unchanged. WAL
  continues archiving; require a successful new backup and a recovery rehearsal
  from the preserved archive in another disposable database/test environment.
- Repeat with an Application using explicit `branch: main` alongside the primary
  omitted-branch fixture. A Naisjob with `spec.uses.postgres` should also complete
  successfully. Let naiserator generate their resources in every case; no user
  manifest changes should be required during the ownership transfer.
- Finally delete **only the disposable Postgres** and verify staged cleanup,
  including asynchronous finalizers. Do not confuse this destructive test with
  branch detach: detach must never delete adopted resources or backups.

## Failure and rollback rules

- A missing/replaced checkpointed resource, foreign owner, non-main branch or
  recovery bootstrap must stop that database's reconciliation, never recreate
  its cluster or guess a replacement target. Test these cases separately.
- Before any adoption, the previous release can be restored normally. **After
  ownership transfer starts, do not roll back to the branch-owning binary.** It
  can recreate a branch and compete for/prune resources. Halt pgrator to preserve
  the running database and deploy a fixed, adoption-aware binary instead. Rehearse
  this stop/resume procedure; there is no automatic reverse migration.
- Do not delete the ownership journal to clear an error: that destroys the UID
  safety guard. Investigate the pinned resource first.
- Checkpointed resource loss currently fails closed even for repairable children;
  repair/recovery is explicit rather than silently recreating a potentially
  different database/archive. Disabling WAL creation does not prune old archives.

Rollout to other environments is gated on these results, not just green Go tests.
