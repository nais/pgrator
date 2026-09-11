---
status: accepted
---

# Model Postgres as a logical database with active instances

A `Postgres` is the logical database that an Application requests through `uses.postgres`. It is not itself one CloudNativePG cluster. Pgrator creates and manages one or more equal `PostgresInstance` resources below it. An instance is a concrete, independently running and writable CNPG cluster with its own data history, Pooler, PVCs, network policies, Workload Identity, bucket permissions, database roles, PKI, and client credentials. Instance names are generated and neutral; neither `main` nor a restore timestamp is part of an instance's identity. PITR is an operation that creates a new instance from a selected point in time, without changing the source instance.

The user never creates a `PostgresInstance` manifest directly. GUI, TUI, and API offer operations in terms of the logical database, for example, "create an instance of `nais-postgres` from 12:00 today". Pgrator owns the resulting instance resource and reports its recovery and readiness status. A user with personal access may select any instance, including an inactive one; all instances are writable subject to that user's role.

`Postgres.spec.activeInstance` selects the instance normal workloads use. On the first instance pgrator activates it automatically. If the field is omitted later, pgrator retains the currently active instance. A requested instance must belong to the same logical Postgres and be ready; a failed activation leaves the currently active instance unchanged. The active instance observed by pgrator is recorded in `Postgres.status.activeInstance`. An active instance cannot be deleted before another ready instance is activated.

Applications and naiserator continue to know only `uses.postgres: nais-postgres` and mount Secrets with stable, logical names. CNPG owns and rotates each instance's CA and client-certificate Secrets. Pgrator owns a separate stable workload Secret for each logical binding, synchronizing the complete connection contract for the active instance: endpoint, database user, CA, client certificate, and private key. The binding and Secret shape are defined by ADR 0003.

Pgrator publishes the stable Secret only from a complete, internally consistent snapshot of the active instance. It must verify that the active instance exists and is ready, that its CA and every requested client-certificate Secret exist and are parseable, that every private key matches its leaf certificate, and that every leaf validates against that CA and identifies the expected database login role. During a CA rotation, leaf certificates can be renewed incrementally. Pgrator must retain the last known-good stable Secret rather than publish a mixture of generations; at first provisioning it must wait to create the Secret until the complete snapshot exists.

Activation and certificate rotation are event-driven dependencies, not polling responsibilities. A Postgres change that selects a new active instance must enqueue its bindings. Changes to the active instance's CA or client-certificate Secrets must also enqueue affected bindings, including Secret data-only updates. These relations must be mapped without adding owner references or pgrator ownership annotations to CNPG-owned source resources. Stakater performs the resulting rolling update of application pods, so naiserator never needs to know which instance is active or which CNPG Secrets it uses.

## Considered Options

- Treat `Postgres` as one CNPG cluster and replace it in place for PITR. Rejected because recovery is destructive, makes validation difficult, and cannot preserve the previous data history for rollback.
- Use `main` and named restore branches. Rejected because instances are equal after creation: an activated PITR instance can replace and outlive the former active instance.
- Route the stable connection through a Service selector only. Rejected because a switch also changes instance-specific credentials and CA, not only the host.
- Let naiserator select or discover the active instance and mount its CNPG secret. Rejected because it leaks pgrator's physical-instance model across controllers and makes workload manifests depend on activation state.
- Share source-instance network policies, service accounts, or credentials with a restore instance. Rejected because each instance must own its access and security resources.

## Consequences

- Pgrator needs an internal `PostgresInstance` resource and controller, plus API operations that create and manage it.
- The current direct relationship from `PostgresBinding` to one CNPG cluster becomes an implementation detail of the active instance. The stable binding Secret remains the cross-controller contract with naiserator, but it is pgrator-owned rather than CNPG-owned.
- ADR 0001's direct-CNPG-Secret contract is superseded. ADR 0003 replaces its binding and credential-identity model.
- Pgrator needs explicit relationship watches (or equivalent indexed event mapping) for `Postgres`, CNPG CA Secrets, and CNPG client-certificate Secrets. A generic owner-only watch is insufficient: CNPG source Secrets must remain exclusively CNPG-owned.
- Activation and CNPG certificate rotation cause a controlled workload rollout through the existing Stakater mechanism only after pgrator has published a complete credential snapshot.
- A future cutover is activation of a verified instance, not deletion and replacement of the logical `Postgres` resource.
