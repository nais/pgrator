---
status: accepted
---

# Broker personal Postgres access through a relay-owned RelayAccess

ADR 0005 described the former WireGuard/tunnel-operator design. Pgrator now
implements this relay-backed transport contract directly; its PostgresAccess
API no longer accepts a WireGuard key and its status contains no Tunnel data.
The NAIS API and CLI have not yet migrated. ADR 0005's logical database and
pgrator-orchestration boundaries remain; its password-only choice for personal
SQL authentication is superseded by the client-certificate direction below.

An isolated proof of concept in `dev-nais-dev` carried PostgreSQL traffic from
a localhost listener over ordinary HTTP/3 `CONNECT`, through a Google UDP
LoadBalancer and a relay in `nais-system`, to a dedicated Postgres in `basseng`.
A query succeeded with PostgreSQL `sslmode=verify-full`; the server observed
the relay Pod as its client. Invalid bearer credentials and a different
requested target were rejected. This proves the path, **not** production
identity, authorization, revocation or availability. This is a TCP stream
relay, not MASQUE CONNECT-IP, CONNECT-UDP, a general VPN, or an open proxy.

## Decision and ownership

Keep the NAIS API a broker, not a database-target resolver or datapath control
plane. The API authorizes the authenticated person, records the reason for
access, creates a namespaced `PostgresAccess` with a server-authoritative
expiry, and later gives connection material only to that access's owner after
it is ready. It does not create `RelayAccess`, select a host/port, or send
mapping updates to relay replicas. The CLI provides the localhost listener
and carries the connection over the relay transport; no human needs a
kubeconfig or `pods/portforward`.

Pgrator remains the `PostgresAccess` orchestrator. It resolves the explicitly
selected `PostgresInstance` to the correct PostgreSQL RW target, reconciles
the personal DatabaseRole and credentials, and creates **one namespaced
`RelayAccess` per PostgresAccess** with the latter as Kubernetes controller
owner. Pgrator owns the desired mapping and its lifecycle, including expiry
and deletion; it also owns the database-ingress NetworkPolicy for that access.
The target comes from pgrator's trusted instance resolution, never an API or
client-supplied host/port. Deletion of PostgresAccess must remove its access
artifacts through ownership/garbage collection, while the retained PostgreSQL
role follows ADR 0005's identity lifecycle.

The `nais/relay` repository owns the `RelayAccess` CRD and the relay's
implementation of its contract. **Owning the CRD does not mean the relay
creates RelayAccess instances.** Relay replicas only consume the desired
state and enforce it for connections; pgrator is its producer. The CR identifies one access by namespace/name and has an immutable spec:
`target.serviceName` is the selected instance's CNPG `<cluster>-rw` Service,
`target.port` is 5432, `expiresAt` is the PostgresAccess expiry, and
`tokenSHA256` is the lowercase hexadecimal SHA-256 of the decoded raw 32-byte
bearer token. Pgrator generates that token once as canonical unpadded base64url
and stores it in a separate controller-owned Opaque Secret; the CNPG password
Secret remains separate. Neither database passwords nor plaintext relay bearer
tokens are placed in the CR or PostgresAccess status. Status exposes only the
RelayAccess and token Secret names. The relay GETs the mapping and validates
the raw token digest on each new connection.

For each new HTTP/3 `CONNECT`, the client supplies an access identifier and
proof of authorization. The relay performs a Kubernetes **GET** of that
`RelayAccess` in the indicated team namespace, checks the proof and expiry,
and connects only to the CR's permitted target. A missing resource, invalid
proof, expired access, unavailable API or mismatched target fails closed.
There is no gRPC push of mappings and no long-lived local mapping cache to
synchronize between replicas: each replica consults the same declarative
source for each *new* connection, not for each PostgreSQL message. The relay
must not accept an arbitrary host/port from the client even if the client
holds a valid credential. PostgreSQL TLS is not terminated by the relay.

## Boundaries to resolve before implementation

- **Client proof and delivery:** Pgrator's per-access bearer token is never
  logged or placed in status. The existing owner-only NAIS API connection
  query still needs to retrieve it by `status.tokenSecret` and deliver it only
  to the authorized person. The relay must not read database password Secrets.
  Limit who may create or mutate RelayAccess CRs; otherwise an untrusted
  workload could grant itself a target.
- **Network isolation:** A shared relay Pod can reach the union of database
  targets allowed by the ingress policies. Unlike a per-access tunnel gateway,
  a Kubernetes NetworkPolicy cannot distinguish one user's connection from
  another inside the same Pod. Pgrator must grant only the relay workload
  access to the selected primary, with a restrictive relay egress policy, and
  the relay's per-connection target check becomes a security boundary. Review
  the blast radius of relay compromise and whether this shared-Pod model is
  acceptable before production rollout.
- **SQL authentication blocker:** CNPG's `PodSelectorRef` only resolves Pods in
  the database Cluster's namespace; relay runs in `nais-system`. The attempted
  relay-specific SCRAM rule could not authenticate relay connections, so it has
  been removed rather than replaced with a broad password-authentication rule.
  The per-access database ingress NetworkPolicy and RelayAccess mapping remain
  declarative, but **SQL access through relay is not functional**. The user
  chose client certificates for personal SQL authentication, not a
  broader SCRAM HBA rule: CNPG `DatabaseRole.spec.clientCertificate.enabled`
  can issue a certificate and key signed by CNPG's client CA without pgrator
  reading or possessing the CA private key. Issuance and delivery are **not**
  implemented by this change.

  CNPG's DatabaseRole client-certificate configuration has no per-role TTL.
  A certificate for a durable, retained login role can therefore outlive one
  PostgresAccess. Deleting the generated Secret does not revoke certificate
  copies already delivered to clients, and PostgreSQL `validUntil` limits
  passwords rather than providing certificate revocation. Before enabling
  personal client-cert access, specify and verify per-access certificate
  validity/revocation and role-login semantics that prevent a past access's
  certificate from authenticating after that access ends. Neither pgrator
  status nor an API/CLI consumer may claim end-to-end connectivity until
  that design and its tests exist.
- **Readiness and lifetime:** Pgrator marks Ready only after the CNPG role
  reports Applied for its current generation and the relay mapping and token
  Secret have been persisted. This does **not** prove that the relay service or
  SQL data path is available. An on-demand GET needs no per-replica mapping
  acknowledgment. Reject new connections after expiry/deletion and close
  connections at expiry; define
  whether manual revocation must also terminate already-open connections and,
  if so, how replicas observe it. Bound Kubernetes GET latency and rate, and
  fail closed during API-server outages.
- **Contract migration:** Pgrator's v1 PostgresAccess spec no longer requires
  `clientWireGuardPublicKey`; `status.relayAccess` and `status.tokenSecret`
  replace Tunnel status. NAIS API and CLI still need to adopt these fields and
  remove Tunnel/WireGuard assumptions. Resolve the TTL mismatch: API accepts
  up to 8 hours while pgrator rejects access beyond one hour. Roll out the
  relay CRD and its GET RBAC before pgrator creates mappings; switch API/CLI
  only after their relay connection path is ready. Relay egress remains owned
  by the relay deployment, not by pgrator.

## Considered options

- Have the API create mappings or choose Postgres targets. Rejected: it would
  duplicate pgrator's physical-instance knowledge and orchestration.
- Have relay create RelayAccess from PostgresAccess. Rejected: it would need to
  duplicate pgrator's PostgresInstance/CNPG resolution or introduce another
  mapping producer. Relay owns the CRD and its enforcement, not its instances.
- Push mappings to relay replicas over gRPC. Deferred: it adds stream
  authentication, snapshot/resync, revision and replica-ack problems without
  demonstrated need at the expected personal-connection rate. Measure
  API-server GET load before considering a watched cache or push mechanism.
- Reuse the POC's global bearer token and fixed relay target. Rejected: it
  neither identifies an individual authorized access nor supports multiple
  targets and independent revocation.

## Consequences

- `nais/relay` must publish the CRD and operate the shared transport, public
  endpoint, certificate, health checks, logs and metrics; pgrator must have
  the RBAC and a non-test runtime consumer contract for RelayAccess.
- Pgrator's RelayAccess creation and the relay's GET are the producer/consumer
  pair for every new field: access identifier selects the mapping, trusted
  target determines the TCP dial, expiry rejects late connects, and proof
  rejects unauthorized clients. Verify each effect in integration tests.
- The API continues to broker identity, authorization, auditing and
  owner-only credential delivery; the CLI implements local forwarding, not
  Kubernetes resource creation. The POC test database, shared token and
  manually installed resources are not part of the deployable feature.
