---
status: accepted
supersedes:
  - 0001-postgresbinding-credential-identity
---

# Model PostgresBinding as one workload use, with explicit credentials

A `uses.postgres` entry expresses one workload's use of one logical `Postgres`
database. Pgrator represents that relationship with exactly one
`PostgresBinding`. A binding is identified by its logical Postgres and consumer
workload, not by a database access role or a physical PostgresInstance.

`PostgresBinding.spec.credentials` is the immutable, exact collection of
credential types required by that workload use. A credential type is one of
`admin`, `read`, and `readwrite`; duplicates are invalid. Naiserator expands the
public `uses.postgres.role` shorthand before it creates the binding:

- an omitted role, or `admin`, produces `admin` and `readwrite` credentials;
- `read` produces only `read`;
- `readwrite` produces only `readwrite`.

Keeping the expanded collection in the binding means pgrator does not need to
infer desired workload mounts from a shorthand it did not receive, and lets the
binding validate the exact credential set it will publish.

Each credential type maps to a database login identity. `admin` is the durable
`app` database-owner login role. `read` and `readwrite` map to distinct
workload-scoped login roles and their corresponding group-role membership. Thus
credentials are distinct connection contracts, not necessarily distinct database
login roles: an admin credential deliberately reuses `app`. Consequently, only
one binding for a logical Postgres may contain `admin`; admission must enforce
that invariant. The binding controller creates or reconciles the CNPG
`DatabaseRole` and client certificate for every non-admin credential, while the
Postgres/active-instance model provides the `app` credential.

## Stable binding Secret

A binding owns exactly one stable, workload-facing Secret with the same name and
namespace as its immutable `PostgresBinding`. It is not supplied as an
instance-specific `spec.secretName`. Naiserator can therefore reference it while
constructing the workload, but cannot select a CNPG Secret or learn physical
instance names. The Secret is controller-owned by the binding and is the only
Secret pgrator writes for this contract.

CNPG remains the sole owner and writer of its cluster CA and
`*-client-cert` source Secrets. Pgrator may read those sources but must neither
modify them nor add owner references or pgrator ownership annotations to them.
The workload never receives `ca.key`.

The binding Secret is an atomic snapshot of one active PostgresInstance. It
contains, for every requested credential, the selected endpoint and database
user, the active instance CA certificate, the client leaf certificate, and its
private key. The Secret key names are a versioned cross-controller API:

```text
# configuration keys; valid environment-variable names
PGHOST, PGPORT, PGDATABASE, PGUSER, PGSSLMODE, PGSSLROOTCERT, PGSSLCERT, PGSSLKEY
READ_PGHOST, READ_PGPORT, ...
READWRITE_PGHOST, READWRITE_PGPORT, ...

# credential file keys; deliberately not valid environment-variable names
ca.crt
admin.tls.crt, admin.tls.key
read.tls.crt, read.tls.key
readwrite.tls.crt, readwrite.tls.key
```

The unprefixed configuration names describe the admin credential when present;
otherwise naiserator must use the relevant role prefix. Configuration path values
refer to deterministic paths projected from the same Secret, for example
`<mount-root>/<postgres>/readwrite/tls.crt`. Naiserator projects the credential
keys explicitly to those paths and may use `envFrom` for the configuration keys.
The dots in credential keys deliberately make them invalid environment-variable
names, so `envFrom` skips them. This is part of the contract, not an accidental
property: a new credential key must remain an invalid environment-variable name
and must be mounted explicitly. Using explicit `secretKeyRef` for configuration
keys remains compatible with this contract.

## Publication and reconciliation

Before creating or replacing a binding Secret, pgrator must read a complete
source snapshot for the active instance. The active instance must be ready; its
CA and every expected client-certificate Secret must exist; the PEM material must
parse; each private key must match its leaf certificate; and every leaf must
validate against the selected CA and identify its expected login role. In
particular, a CA rotation must not publish a new CA paired with old leaf
certificates, or vice versa.

If that validation fails after a Secret has been published, pgrator retains the
last known-good Secret and reports the binding not ready. On first provisioning,
it creates no workload-facing Secret until validation succeeds. This makes a
missing or invalid source fail closed without replacing a usable configuration
with an inconsistent one.

Pgrator must react to all dependencies without polling for certificate rotation:

- a Postgres active-instance selection change enqueues every binding referring to
  that logical Postgres;
- data-only changes to the active instance's CA or client-certificate Secrets
  enqueue affected bindings;
- DatabaseRole changes may be used as an additional signal, but status-only
  changes must not be filtered out if they are relied upon.

These are reference relationships, not ownership relationships. The controller
must implement an explicit mapped watch, normally backed by field indexes, rather
than the synchronizer's owner-annotation-only `AdditionalTypes` mechanism. A
CNPG source Secret remains CNPG-owned even while it triggers a binding reconcile.

On a valid active-instance switch or certificate rotation, pgrator replaces the
single binding Secret with the new complete snapshot. Stakater then performs the
workload rollout. Naiserator and workloads do not discover physical instances or
mount instance-specific CNPG Secrets.

## Considered Options

- Keep one PostgresBinding per access role. Rejected because one workload use can
  require several credentials, notably the default admin and readwrite pair. It
  exposes credential implementation details as separate database-use relations
  and makes instance activation synchronize several resources for one use.
- Keep a shorthand role in PostgresBinding and let pgrator expand it. Rejected
  because naiserator must know the full credential set to generate file mounts
  and configuration references before pgrator reconciles it.
- Give each credential a separate stable Secret while retaining one binding.
  Rejected because activation must switch all endpoint, CA, certificate, and key
  material for one workload use together.
- Mount CNPG source Secrets directly. Rejected by ADR 0002: source Secret names
  and contents are instance-specific, while the workload contract must remain
  stable through activation.
- Poll for CA or certificate rotation. Rejected because CNPG owns issuance and
  rotation. Source-resource events provide the prompt, authoritative trigger.

## Consequences

- `PostgresBinding.spec.role` and `spec.secretName` are replaced by the immutable
  `spec.credentials` collection. The binding name determines its stable Secret
  name.
- Naiserator creates one binding and one stable-Secret reference per
  `uses.postgres` entry, expands role shorthand itself, and never references a
  CNPG CA or client-certificate Secret.
- Pgrator creates or reconciles the required DatabaseRoles, reads CNPG-owned
  credential sources for the active instance, and writes only the stable binding
  Secret.
- The admission webhook validates credential sets and permits at most one
  admin-containing binding per logical Postgres.
- The binding controller and generic synchronizer need relationship-aware watches
  and predicates that admit source Secret data updates; current generation-only
  child filtering is insufficient.
- Existing per-role PostgresBindings and their direct-CNPG-Secret mounts require
  a deliberate greenfield transition. They cannot be silently repurposed.
