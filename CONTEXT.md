# Managed persistence

Language for managed databases and their recovery lifecycle.

## Language

**Point-in-time recovery (PITR)**:
Creation of a new, independent Postgres database with the source database's data at an explicitly selected instant. The source remains unchanged and applications are not moved automatically.
_Avoid_: Branch creation, in-place restore

**Recovery source**:
The existing database whose archived history supplies a recovered database. A source's identity is distinct from its reusable name.

**Recovery target time**:
The requested instant in the source database's history, not the creation time of the recovered database.

**Earliest restore time**:
The earliest known instant recoverable from the currently retained backups and archived history. It is not necessarily the start time of the most recent nightly backup.
