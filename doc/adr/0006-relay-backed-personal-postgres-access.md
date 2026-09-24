---
status: proposed
---

# Broker personal Postgres access through a relay-owned RelayAccess

ADR 0005 describes the current WireGuard/tunnel-operator design. This ADR
records the chosen direction for a relay-backed data path; it does not claim
that the API, CLI, pgrator or relay have been migrated. ADR 0005 remains the
current transport contract until that migration is implemented and verified.
The database-identity and pgrator-orchestration decisions in ADR 0005 still
apply.

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
state and enforce it for connections; pgrator is its producer. The CR needs
to identify an individual access, an exact permitted target derived from the
PostgresInstance, its expiry, and the material needed to verify that the
connecting client holds authorization for that access. The precise
credential/proof format remains to be designed. Database passwords and
plaintext relay bearer tokens must not be placed in the CR.

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

- **Client proof and delivery:** The POC's single shared bearer token is not
  suitable for production. Choose a per-access, high-entropy credential or
  another verifiable, access-bound proof; define how pgrator creates it,
  stores it without reconcile-time rotation, and exposes it through the
  existing owner-only API connection query. The relay must not read database
  password Secrets. Limit who may create or mutate RelayAccess CRs; otherwise
  an untrusted workload could grant itself a target.
- **Network isolation:** A shared relay Pod can reach the union of database
  targets allowed by the ingress policies. Unlike a per-access tunnel gateway,
  a Kubernetes NetworkPolicy cannot distinguish one user's connection from
  another inside the same Pod. Pgrator must grant only the relay workload
  access to the selected primary, with a restrictive relay egress policy, and
  the relay's per-connection target check becomes a security boundary. Review
  the blast radius of relay compromise and whether this shared-Pod model is
  acceptable before production rollout.
- **Readiness and lifetime:** Decide how pgrator proves the declared mapping
  and relay service are usable before marking PostgresAccess Ready. An
  on-demand GET needs no per-replica mapping acknowledgment. Reject new
  connections after expiry/deletion and close connections at expiry; define
  whether manual revocation must also terminate already-open connections and,
  if so, how replicas observe it. Bound Kubernetes GET latency and rate, and
  fail closed during API-server outages.
- **Contract migration:** The present PostgresAccess spec requires a CLI
  WireGuard public key, and its status/readiness and the API connection query
  expose Tunnel fields. Define a relay-backed variant or migration before
  removing those fields. Resolve the current TTL mismatch: the API accepts
  up to 8 hours while pgrator currently rejects access beyond one hour.
  Deliver CRD/RBAC and relay before pgrator creates RelayAccess, then switch
  API/CLI after the new status and credential path is available.

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
