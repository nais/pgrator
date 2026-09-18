---
status: accepted
---

# Orchestrate personal Postgres access with a PostgresAccess resource

Personal access crosses separate transport and database boundaries. A WireGuard
tunnel provides a private path, but it is not a PostgreSQL identity.
`tunnel-operator` is the only human database data plane: we do not use
`kubectl`, user kubeconfigs, Kubernetes RBAC, or `pods/portforward`.

NAIS API authenticates the person through the existing NAIS login backed by
ZITADEL and authorizes an access request. It creates exactly one pgrator-owned
`PostgresAccess` resource. Its interface contains one explicitly selected ready
`PostgresInstance`, `read`, `readwrite`, or `readwritecreate` access, the
authenticated NAIS user's email address, an absolute expiry of at most one hour,
and the CLI-generated ephemeral WireGuard public key. The logical Postgres is
derived from the instance. API never supplies a host, port, database role, group
role, Secret name, CNPG Cluster, or tunnel-operator implementation field.

PostgresAccess is the orchestration module. Its controller resolves the selected
instance's PostgreSQL RW target and manages:

- one controller-owned tunnel-operator `Tunnel` for that target;
- one controller-owned `kubernetes.io/basic-auth` Secret holding a random SCRAM
  password;
- one controller-owned `NetworkPolicy` permitting only that Tunnel gateway to
  reach the resolved CNPG primary on TCP/5432; and
- one controller-owned CNPG `DatabaseRole` per user and physical instance,
  configured with `ReclaimPolicy: Retain`.

The DatabaseRole is the stable personal database identity. The DatabaseRole
Kubernetes resource name uses the local part of the user's email and physical
instance, normalized to a Kubernetes-compatible name and shortened with a stable
hash of the full email and instance to PostgreSQL's 63-byte limit. The actual
PostgreSQL role name is the raw email address, used verbatim, and appears as
`current_user` in SQL audit logs. The full email therefore also remains in
PostgresAccess and API audit data. The role has no superuser, createdb, createrole, replication,
or bypass-RLS privilege and does not issue a client certificate.

While access is active, pgrator sets `login: true`, `validUntil`, the current
password Secret, and exactly one privilege group on the durable role:

- `read` grants the read group;
- `readwrite` grants the readwrite group; and
- `readwritecreate` grants a distinct group with readwrite privileges and
  `CREATE` in shared `public`. The explicit level allows durable personal
  objects without extending workload readwrite access. Users can later return
  as the same role and remove objects they own.

`readwritecreate` is initialized only for new PostgresInstances. Pgrator marks
a CNPG Cluster `postgres.nais.io/readwritecreate-capable: "true"` when it
creates a fresh initdb cluster (postInitSQL runs exactly once, at initdb), and
rejects `readwritecreate` PostgresAccesses targeting unmarked clusters with a
`False`/`UnsupportedAccessLevel` Ready condition. Pre-existing and recovered
instances stay ineligible until an explicit database-privilege migration exists.

The PostgreSQL role name, OID, and user-owned objects are durable; the
DatabaseRole CR is not. Euthanaisa expiry deletes PostgresAccess, and Kubernetes
garbage-collects its owned Tunnel, password Secret, database-ingress
NetworkPolicy, and DatabaseRole CR. CNPG's `ReclaimPolicy: Retain` keeps the
actual PostgreSQL role and the objects it owns. A later access for the same
canonical NAIS identity and physical instance recreates the same named
DatabaseRole and rotates its password and `validUntil`. Pgrator does not wait
for CNPG during finalization, terminate sessions, or remove user-owned objects.

There is at most one active PostgresAccess per `{NAIS email, PostgresInstance}`.
API serializes replacement and creates a new resource only after the old one is
fully deleted. Pgrator can therefore treat the active resource as the only writer
of mutable credential and privilege fields. Existing connections may run until
their tunnel or client connection ends; expiry is not a hard session cutoff.

PostgresAccess is ready only when CNPG has applied its DatabaseRole and
tunnel-operator reports its Tunnel ready. PostgreSQL TLS remains inside
WireGuard and uses `sslmode=verify-full`; the gateway must not terminate TLS.
The selected instance's NetworkPolicy permits only the stable tunnel-gateway
network identity to reach its PostgreSQL port. The gateway is not allowed broad
tenant-namespace access.

Direct PostgreSQL OAuth with ZITADEL is deferred. PostgreSQL 18 OAuth requires
an installed server validator and compatible clients; ZITADEL is used for API
authorization, not directly trusted by PostgreSQL in this design.

## Considered Options

- CNPG client certificates for personal users. Rejected: global, normally
  90-day lifecycle and no CRL do not fit short-lived personal credentials.
- API directly creates Tunnel, Secret, and DatabaseRole. Rejected: it leaks
  CNPG lifecycle and physical target discovery into API.
- Dropping the PostgreSQL role with access expiry. Rejected: it would create a
  new OID on later access and lose ownership of durable user-created objects.
  The DatabaseRole CR is instead garbage-collected with `ReclaimPolicy: Retain`.
- Tunnel-operator manages database identities. Rejected: it couples generic
  byte transport to PostgreSQL authorization.
- Kubernetes `pods/portforward` as human data plane. Rejected: the per-tunnel
  WireGuard transport is the data-plane contract.
- Euthanaisa terminates PostgreSQL sessions. Rejected: it only deletes expired
  Kubernetes resources and hard cutoff is not a v1 requirement.
- Direct ZITADEL OIDC tokens in PostgreSQL. Deferred pending validator, CNPG
  operand, mapping, revocation, and client-compatibility validation.

## Consequences

- Personal access always selects a ready physical instance and never follows a
  later active-instance switch.
- API owns authorization, PostgresAccess creation, credential delivery and audit
  correlation. Pgrator owns target derivation and orchestration. CNPG owns role
  reconciliation. Tunnel-operator owns WireGuard transport.
- A personal role provides a stable, person-correlatable `current_user` rather
  than a gateway IP or shared account.
- User-created objects deliberately outlive access resources; their lifecycle is
  a separate operational concern.
- Each tenant needs an operated tunnel-operator deployment, including
  LoadBalancer/forwarder exposure, port capacity, metrics, logs, alerts,
  upgrades, and cleanup.
- Delivery proceeds through #141 transport qualification, #143 credential and
  privilege lifecycle, #144 PostgresAccess orchestration, and #145 API/CLI
  integration.
