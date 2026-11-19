# Stable workload SQL identities

## Why unbinding must not drop a role

PgBouncer server connections can outlive an application rollout and its binding.
PostgreSQL authenticates those sessions with a role OID, not a role name that is
looked up on every query. Dropping and recreating `workload-readwrite` therefore
leaves pooled sessions using a deleted OID, even when the new role has the same
name and correct privileges. This produces `42501` after an A → B → A switch.

## Protocol

No new CRD or public input is introduced. Existing CNPG `DatabaseRole` resources
use the existing `databaseRoleReclaimPolicy: retain` setting:

1. Persist the binding's cleanup finalizer before activating retained SQL roles.
2. Keep the existing names, cluster references, memberships, certificates and
   connection Secret format for active bindings. An old Delete-policy role can
   be updated in place; its SQL role must not be dropped or recreated.
3. When a credential or binding is removed, withdraw obsolete connection
   Secrets and network access. Change owned workload roles to NOLOGIN, disable
   passwords, clear memberships and privileged attributes, and disable further
   client-certificate issuance. Set Retain even when retiring an old role that
   has never passed through the new normal reconcile path.
4. Wait for CNPG `status.applied=true` for the **current** generation. An old
   successful status does not prove revocation.
5. Delete the Retain CR, then let the binding finish deletion. The SQL identity
   remains disabled, without managed group grants. Rebinding activates that same
   identity and restores its memberships, including for previously pooled
   connections.

The shared database owner `app` must not be disabled by a workload binding.
Legacy binding-owned admin aliases are migrated to Retain without changing that
owner's SQL privileges. Foreign-owned resources are never changed.

If the physical cluster is confirmed absent, cleanup can finish without waiting
for a SQL operation on a nonexistent database. A timeout, permissions error, or a
cluster that is merely deleting does not count as absence. Already deleting
Delete-policy DatabaseRoles cannot be rescued reliably; inventory these before
rollout instead of forcing finalizer removal.

Disabled SQL role rows remain in the database; Kubernetes binding/role objects
and credential Secrets are still cleaned up. Any eventual deletion of dormant
SQL identities needs a separate policy that accounts for pooled sessions.

## Validation and rollout

- Controller tests cover active-role preservation, legacy policy migration,
  credential removal, generation-aware revocation, durable finalizer ordering,
  shared/foreign role isolation and cleanup after whole-database deletion.
- The kind E2E holds one PostgreSQL session open across two actual CNPG-driven
  unbind/rebind cycles. The OID stays the same, queries fail with `42501` while
  revoked, and the same session succeeds after rebind.
- There is no CRD upgrade, database bootstrap, table ownership migration or
  application restart required for active databases.
- Do not force-remove cleanup finalizers; SQL revocation must be confirmed.
- Old pooled sessions whose roles were dropped **before** the rollout remain
  stale and require a scoped reconnect. The policy cannot repair those OIDs.
- Roll out to dev and exercise the real application's A → B → A flow before
  expanding to other environments. Rolling back to the old operator restores
  its destructive role-deletion behavior.
