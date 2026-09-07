---
status: accepted
---

# Model Postgres as a logical database with active instances

A `Postgres` is the logical database that an Application requests through `uses.postgres`. It is not itself one CloudNativePG cluster. Pgrator creates and manages one or more equal `PostgresInstance` resources below it. An instance is a concrete, independently running and writable CNPG cluster with its own data history, Pooler, PVCs, network policies, Workload Identity, bucket permissions, database roles, PKI, and client credentials. Instance names are generated and neutral; neither `main` nor a restore timestamp is part of an instance's identity. PITR is an operation that creates a new instance from a selected point in time, without changing the source instance.

The user never creates a `PostgresInstance` manifest directly. GUI, TUI, and API offer operations in terms of the logical database, for example, "create an instance of `nais-postgres` from 12:00 today". Pgrator owns the resulting instance resource and reports its recovery and readiness status. A user with personal access may select any instance, including an inactive one; all instances are writable subject to that user's role.

`Postgres.spec.activeInstance` selects the instance normal workloads use. On the first instance pgrator activates it automatically. If the field is omitted later, pgrator retains the currently active instance. A requested instance must belong to the same logical Postgres and be ready; a failed activation leaves the currently active instance unchanged. The active instance observed by pgrator is recorded in `Postgres.status.activeInstance`. An active instance cannot be deleted before another ready instance is activated.

Applications and naiserator continue to know only `uses.postgres: nais-postgres` and mount secrets with stable, logical names. CNPG owns and rotates each instance's client-certificate secret. Pgrator owns a separate stable workload secret for each logical binding, synchronizing the complete connection contract for the active instance: endpoint, database user, CA, client certificate, and private key. On activation or credential rotation, pgrator updates that stable secret from one complete instance credential set. Stakater performs the resulting rolling update of application pods, so naiserator never needs to know which instance is active or which CNPG secrets it uses.

## Considered Options

- Treat `Postgres` as one CNPG cluster and replace it in place for PITR. Rejected because recovery is destructive, makes validation difficult, and cannot preserve the previous data history for rollback.
- Use `main` and named restore branches. Rejected because instances are equal after creation: an activated PITR instance can replace and outlive the former active instance.
- Route the stable connection through a Service selector only. Rejected because a switch also changes instance-specific credentials and CA, not only the host.
- Let naiserator select or discover the active instance and mount its CNPG secret. Rejected because it leaks pgrator's physical-instance model across controllers and makes workload manifests depend on activation state.
- Share source-instance network policies, service accounts, or credentials with a restore instance. Rejected because each instance must own its access and security resources.

## Consequences

- Pgrator needs an internal `PostgresInstance` resource and controller, plus API operations that create and manage it.
- The current direct relationship from `PostgresBinding` to one CNPG cluster becomes an implementation detail of the active instance. The stable binding secret remains the cross-controller contract with naiserator, but it is pgrator-owned rather than CNPG-owned.
- ADR 0001's decision that naiserator mounts a CNPG-managed client-certificate Secret, and reads the CA directly from the CNPG cluster CA Secret, is superseded for the multi-instance model. Its credential naming and role-identity decisions remain applicable until replaced deliberately.
- Activation and CNPG certificate rotation cause a controlled workload rollout through the existing Stakater mechanism.
- A future cutover is activation of a verified instance, not deletion and replacement of the logical `Postgres` resource.
