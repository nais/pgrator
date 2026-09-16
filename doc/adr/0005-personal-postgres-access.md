---
status: accepted
---

# Orchestrate personal Postgres access with a PostgresAccess resource

Personal access to a Postgres database crosses two independent boundaries:
network transport into a private tenant cluster, and PostgreSQL authentication
and authorization. A WireGuard tunnel provides the former but must not become a
database identity. A PostgreSQL role provides the latter but must not expose the
database outside the tenant cluster.

NAIS API authenticates the person through the existing NAIS login backed by
ZITADEL and authorizes an access request. API creates exactly one pgrator-owned
`PostgresAccess` resource. Its interface contains one explicitly selected ready
`PostgresInstance`, `read` or `readwrite` access, the authenticated NAIS user's
email address, an absolute expiry of at most one hour, and the CLI-generated
ephemeral WireGuard public key. The logical Postgres is derived from the
instance. API does not know CNPG Cluster or Service names, `DatabaseRole`
fields, password Secret format, or the tunnel-operator resource format. A
caller can never supply a host, port, database role, group role, or Secret name.

`PostgresAccess` is a deep orchestration module. Its controller resolves the
selected instance's CNPG target and manages these implementation resources:

- one controller-owned tunnel-operator `Tunnel` for the resolved TCP target;
- one controller-owned `kubernetes.io/basic-auth` Secret holding a newly
  generated random SCRAM password; and
- one per-user, per-instance CNPG `DatabaseRole` that is not owned by the
  access resource and therefore survives its deletion.

The DatabaseRole is a stable personal database identity. Its technical name is
derived from the local part of the NAIS user's email address and the physical
instance, normalized to a Kubernetes-compatible name and shortened with a
stable hash of the full email address and instance to PostgreSQL's 63-byte
identifier limit. It is visible as `current_user` in PostgreSQL and SQL audit
logs. This is intentional: the role can be correlated to the full email address
held in PostgresAccess and API audit data without putting the email domain in
PostgreSQL. The role has no superuser, createdb, createrole, replication, or
bypass-RLS privilege and does not issue a client certificate.

While a PostgresAccess is active, pgrator configures the stable role with the
access resource's password Secret, `login: true`, `validUntil` equal to the
access expiry, and only the authorized privileges. `read` grants membership in
the existing read group. `readwrite` grants membership in the existing
readwrite group and `CREATE` on the shared `public` schema. The latter is
deliberate: personal users may create durable objects in `public` and later
return as the same role to inspect or remove their own objects. `CREATE TEMP`
is therefore also available during an authorized readwrite access.

The role name and objects are durable; its password and effective privileges are
not. Euthanaisa has an expiry annotation only on PostgresAccess. At expiry it
only deletes that parent resource. Kubernetes garbage collection removes the
owned Tunnel and password Secret. The PostgresAccess finalizer first removes
the role's password Secret reference and active group/schema privileges, then
allows deletion to finish. It does not drop the role, terminate sessions, or
delete user-owned objects. This gives Euthanaisa one authoritative TTL without
making it perform database actions.

There is at most one active PostgresAccess for a `{NAIS username,
PostgresInstance}` pair. A new access replaces the previous one. Pgrator rotates
the stable role to the new password Secret and manages the old access as stale,
so a delayed finalizer from it must not remove the newer credential or
privileges. Existing connections from the old access may continue until their
tunnel or client connection closes, but cannot open new connections after
password rotation. This is deliberately not a hard session cutoff.

PostgresAccess reports ready only when CNPG reports the DatabaseRole applied and
the tunnel-operator reports Tunnel ready. API can retrieve the generated
credential only from pgrator's controller-owned Secret. The CLI receives it
only once PostgresAccess is ready, opens a local TCP listener, and sends that
connection over WireGuard. PostgreSQL TLS remains inside the tunnel and uses
`sslmode=verify-full`; the gateway must not become a TLS termination point.

Direct PostgreSQL OAuth with ZITADEL is deferred. PostgreSQL 18's OAuth support
requires an installed server-side validator and compatible clients, neither of
which belongs in this first access path. ZITADEL remains the identity provider
for NAIS API authorization; it is not directly trusted by PostgreSQL in this
decision.

## Considered Options

- Use CNPG client certificates for personal users. Rejected because CNPG issues
  and renews these on global, normally 90-day settings and has no CRL support.
  That does not match short-lived credentials or precise revocation.
- Let API create `Tunnel`, Secret, and DatabaseRole directly. Rejected because
  API would need to learn CNPG's credential contract, physical target discovery,
  role lifecycle, and cross-operator readiness. PostgresAccess hides these
  implementation details behind one resource-oriented interface.
- Use one expiring DatabaseRole per access. Rejected because a user who creates
  durable objects cannot later return as the same database identity to manage
  them, and role deletion can fail while those objects exist.
- Make the tunnel-operator manage roles and credentials. Rejected because it
  would couple a generic byte transport to PostgreSQL identity and privileges.
- Let Euthanaisa terminate PostgreSQL sessions before deletion. Rejected because
  Euthanaisa only deletes expired Kubernetes resources and the product does not
  require hard session cutoff.
- Authenticate directly with ZITADEL OIDC tokens. Deferred pending validation of
  CNPG operand support, a maintained PostgreSQL OAuth validator, token-to-role
  mapping, revocation behavior, and CLI/GUI client compatibility.

## Consequences

- Personal access is limited to explicitly selected ready instances, including
  inactive instances where API policy permits it; it never follows a later
  active-instance switch.
- API owns authorization, PostgresAccess creation, credential delivery, and
  audit correlation. Pgrator owns target derivation and orchestration. CNPG
  owns database-role reconciliation. Tunnel-operator owns WireGuard transport.
- A personal role is visible as PostgreSQL `current_user`, giving SQL audit a
  stable, person-correlatable identity rather than a gateway IP or shared account.
- Person-owned objects in `public` deliberately outlive individual access
  resources. Their owner role does too. Lifecycle cleanup of those objects is a
  separate product and operational concern.
- Pgrator must make replacement and finalization generation-safe: an old access
  must never clear the password or privileges installed by a newer access.
- The API and CLI wait for PostgresAccess ready and need no knowledge of its
  CNPG or tunnel implementation resources.
- Connection support begins with `psql`; GUI support requires validation that it
  can use the local tunnel while verifying the internal PostgreSQL server name.
