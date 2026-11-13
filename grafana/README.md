# Postgres dashboards

Component-owned sources for the provider dashboards on `monitoring.nais.io`:

| Dashboard | UID | Purpose |
| --- | --- | --- |
| [Nais Postgres – Fleet](https://monitoring.nais.io/d/nais-postgres-fleet/) | `nais-postgres-fleet` | Observed CNPG branches, operations and resource use |
| [Postgres – Adoption & Capacity](https://monitoring.nais.io/d/nais-postgres-adoption/) | `nais-postgres-adoption` | Reported Nais Postgres/Cloud SQL inventory, teams and trends |

These are separate from Console metrics and the legacy postgres_exporter dashboard.
The definitions were moved from `nais/dashboards` commit
`dafce1a1b00417ea032e387aec5265058b6322e2` without changing their contents or UIDs.
The central Git Sync connection is managed manually and reads `main` under
`dashboards/generated/`; see [setup and source changes](MIGRATION.md).
No provisioning Job, Helm release or component deployment is needed.

## Develop and validate

Requires `mise`, `make`, Git and network access to public tool/dependency releases.
No kubecontext, Grafana login or production credentials are needed:

```sh
make -C dashboards install
make -C dashboards fmt
make -C dashboards compile
make -C dashboards check
```

Run from the repository root. `install` restores Jsonnet dependencies from
`jsonnetfile.lock.json`; commit deliberate dependency/lockfile updates together.
Grafonnet is pinned to the same commit used before migration. No Grizzly dependency
or second publishing command is needed. `vendor/` is ignored.

`src/` is the source of truth. `src/main.libsonnet` wraps both dashboards as
`dashboard.grafana.app/v1` resources. Commit generated JSON under `generated/`
with its source; never edit generated resources or copies in Grafana by hand.
`check` checks formatting and compares a temporary compilation with `generated/`.
It does not fix files and catches missing/extra generated resources as well.

The Makefile runs pinned go-jsonnet 0.21.0 and jsonnet-bundler 0.6.0 with
`mise --no-config`: operator tooling, environment hooks and Go/Helm tasks are not
loaded. The separate `Dashboard checks` workflow does only installation and
read-only checks, with `contents: read` and no deployment credentials.

Changes confined to `dashboards/**`, `.github/workflows/dashboards.yml` and the
existing ignored Markdown paths do not start the ordinary build/deploy or E2E
workflows. Mixed operator/dashboard changes still run both workflows. The initial
change to those workflow filters is itself a normal workflow change; it is not
exempted. Existing local Lefthook operator checks are unchanged.

Generation checks do not prove PromQL semantics, datasource coverage or rendering.
When changing queries, verify missing/zero/partial data, deduplication and filter
isolation with synthetic data, then use an isolated Grafana preview. Live checks
require separately approved access and read-only datasource calls. Do not fetch
production credentials into this directory or commit runtime data, screenshots,
logs, browser profiles or authentication helpers.

## Fleet interpretation

The Tenants selector supports one/several/All provisioned Prometheus `*-metrics`
datasources, defaults to NAV and refreshes every five minutes. Each tenant repeats
the entire operational section; tenant values are never merged. Inner headings
are text panels because nested Grafana rows would terminate the repeat scope.

Environment/team/Postgres/branch filters apply to each section. Suggestions come
from the first tenant; All and custom exact values can cover names absent from
those suggestions. This is not a joined inventory dropdown. Control-plane panels
are environment-wide inside the tenant and ignore team/Postgres/branch filters.

The existing branch table stays below the summary stats. It includes:

- **PG major:** integer part of CNPG's `cnpg_collector_postgres_version` (18.6 →
  18), not `server_version_num` or a parent's desired version. Every discovered
  pod must report a positive version and all scrapes/pods must agree on major;
  missing, zero, partial or conflicting reports are Unknown.
- **CPU:** requested cores and observed CPU rate, summed across branch pods.
- **Memory:** requests and container working set in binary units.
- **PVC:** requested storage, actual kubelet filesystem capacity and total
  filesystem bytes used, not SQL table size. Each `pgdata` PVC counts once per
  branch, including replicas and inactive branches. Environment and namespace
  stay in joins; equal names in different scopes are not conflated.

Only the seven resource columns have sum footers, one per tenant and selected
filters. Version, health signals, percentages and backup ages are not summed.
There is no combined cross-tenant footprint or separate capacity table. Disk and
memory/limit graphs remain under Capacity; CPU limits are not table columns.

Resource cells require measurements for all discovered pods/PVCs in the branch.
Missing/partial measurements, incomplete pod/PVC mapping or ambiguous cross-branch
PVC ownership make the affected cell and footer Unknown, not a partial sum or
zero. Genuine measured zero stays zero. The query uses a finite negative `-1e30`
sentinel only in resource-table columns; negative results map to Unknown so the
sum footer propagates unknowns. Grafana skips nulls in sums and infinity bypasses
these mappings in 13.2.2, so neither is a replacement for this sentinel.

### Operational blind spots

- Inventory is currently discovered `up{postgres!=""}` targets, including failed
  scrapes, not a complete Postgres/branch registry. Databases before discovery and
  branches whose targets disappear are absent. Unknown is not healthy.
- Primary/streaming observations do not prove desired HA, active branch, SQL
  connectivity, credentials, networking or pooler health. A replica observation
  is not a desired-replica guarantee.
- Backup metadata comes from `barman_cloud_cloudnative_pg_io_*`, exported by the
  Barman plugin. Legacy CNPG backup timestamps may be zero and are not used.
  Backup age uses the newest positive timestamp across branch pods; missing/zero
  is Unknown, not 1970. First recoverability point does not prove uninterrupted
  WAL, latest recoverable point, retention or a successful restore.
- Backup age >36h and disk use at 80%/90% are triage hints, not SLO/RPO or new
  on-call alerts. Idle databases can have old last-archived-WAL timestamps.
- Restarts cover discovered pods. Poolers, operators, backup objects, network and
  logging are outside resource totals; this is not whole-service cost.

## Adoption interpretation

Tenant/environment detail sections show reported inventory and Nais resource
history. The overview intentionally ignores detail filters and uses all configured
sources/environments. Its source snapshot lives in `src/monitoring-sources.libsonnet`
and was validated against eight then-provisioned central datasources. It is not
a canonical tenant registry. Recheck provisioning before publishing changes:
new sources appear automatically in selectors, but enter combined totals only
after the explicit snapshot is updated and validated. Fleet has no snapshot
aggregation dependency.

- **Nais Postgres:** `pgrator_resource_info`, deduplicated by environment,
  namespace and resource name for `postgres.nais.io` and `postgresbranch.nais.io`
  separately. Branches/replicas/sessions are not logical Postgres resources.
- **Cloud SQL (all engines):** KCC `configconnector_applied_resources_total` for
  `SQLInstance.sql.cnrm.cloud.google.com`, summed across namespaces and reported
  Ready-condition reasons, with duplicate scrapes deduplicated. This is a
  periodically reset/rebuilt gauge, despite its name: never use `rate()`.
  Updating/failed resources remain counted. It is cached Kubernetes inventory,
  not authoritative GCP inventory, SQL databases or HA nodes, and exposes neither
  instance identity, engine nor configured CPU/memory/storage. Rebuilds can
  produce partial/missing counts.
- **Teams:** namespaces per environment in details; in the overview, deduplicated
  by tenant + namespace across environments before tenant counts are summed.
  A team can appear in both product counts; overlap is separate and includes
  use across different environments.
- **Unknown/coverage:** failed scrapes or empty/absent inventory are not zero,
  even if an exporter is up. Unknown table observations use `-1` and are omitted
  from chart sums; coverage remains visible. Known non-overlap between two
  populated inventories can be zero. If both sources vanish, even an environment
  row may disappear. Scrape success does not prove a complete/synced cache.

History is available Mimir history (default 30 days), not reconstructed inventory.
Reported count changes are not create/delete events, authoritative migrations or
an adoption share. Watcher event counters are not inventory.

Nais capacity/history uses CNPG postgres-container requests and observed CPU
rate/working set, plus deduplicated pgdata PVC requests/filesystem bytes used.
All discovered branches and replicas contribute; environment/namespace stay
isolated. Requests are not node allocation, limits or expanded filesystem capacity.

Cloud SQL capacity, PostgreSQL-only comparisons, money and savings are not invented
from these signals. They need engine-aware inventory, reliable Cloud SQL capacity/
usage and an explicit model covering CPU, memory, disk, backup, network and logging.
Coordinate with the separate cost-basis work and [nais/api#571](https://github.com/nais/api/issues/571).
An authoritative zero also requires an explicit synced/complete inventory signal.
