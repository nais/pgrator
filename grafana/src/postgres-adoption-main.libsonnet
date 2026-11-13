local g = import './g.libsonnet';
local q = import './postgres-adoption-queries.libsonnet';
local overview = import './postgres-overview.libsonnet';
local datasource = { type: 'prometheus', uid: '${tenant}' };
local target(ref, expr, legend, instant=false) = {
  refId: ref,
  datasource: datasource,
  expr: expr,
  legendFormat: legend,
  instant: instant,
  range: !instant,
  format: if instant then 'table' else 'time_series',
};
local panel(id, title, type, targets, x, y, w, h, unit='short', description='') = {
  id: id,
  title: title,
  type: type,
  description: description,
  datasource: datasource,
  gridPos: { x: x, y: y, w: w, h: h },
  targets: targets,
  fieldConfig: { defaults: { unit: unit, noValue: 'Unknown', color: { mode: 'palette-classic' } }, overrides: [] },
};
local series(id, title, targets, x, y, w=12, unit='short', description='') =
  panel(id, title, 'timeseries', targets, x, y, w, 7, unit, description)
  + {
    fieldConfig+: { defaults+: { custom: { drawStyle: 'line', lineInterpolation: 'stepAfter', lineWidth: 1, fillOpacity: 0, spanNulls: false, showPoints: 'never' } } },
    options: { legend: { displayMode: 'table', placement: 'bottom', calcs: ['lastNotNull'] }, tooltip: { mode: 'multi', sort: 'desc' } },
  };
local capacity = 'Nais Postgres database containers/data volumes only, across all discovered branches and replicas. Excludes poolers, operators, backup storage, network and logging. Missing Kubernetes/discovery data is not zero. Not Cloud SQL capacity or a billing estimate.';

local overviewDescription = 'Across the explicit ' + overview.scope + ' and every observed environment, independent of the detail selectors below. Reported counts only: compare inventory coverage with observed environments and configured tenants. Missing sources/inventory do not become zero; a partial sum is not complete inventory. Cloud SQL covers all engines/statuses. Teams are unique tenant + namespace across environments, not sums of dev/prod team counts. New/deleted datasource provisioning requires refreshing monitoring-sources.libsonnet. No migration percentage or event counts are inferred.';
local globalHistory(id, title, expr, x) =
  series(id, title, overview.targets(expr, false, 'tenant'), x, 7, description=overviewDescription + ' Historical sums use the reported observations at each timestamp; coverage can change. No data from any source leaves a gap, not an asserted zero.')
  + { datasource: overview.mixed, transformations: overview.history };
local globalPanels = [
  { id: 100, title: 'Overview — ' + overview.scope + ' · all environments', type: 'row', collapsed: false, panels: [], gridPos: { x: 0, y: 0, w: 24, h: 1 } },
  panel(101, 'Across tenants and environments — reported inventory', 'table', overview.targets(q.overviewInventory), 0, 1, 18, 6, description=overviewDescription)
  + {
    datasource: overview.mixed,
    transformations: overview.matrix + [{ id: 'organize', options: {
      indexByName: { Scope: 0, 'Reported resources': 1, 'Unique teams (reported)': 2, 'Environments with inventory': 3, 'Observed environments': 4, 'Tenants with inventory': 5, 'Configured tenants': 6 },
      renameByName: { 'Unique teams (reported)': 'Unique teams', 'Environments with inventory': 'Known envs', 'Observed environments': 'Observed envs', 'Tenants with inventory': 'Known tenants', 'Configured tenants': 'Sources' },
    } }],
    options: { showHeader: true, cellHeight: 'sm' },
    fieldConfig+: { defaults+: { unit: 'none', decimals: 0 }, overrides: [
      { matcher: { id: 'byName', options: 'Scope' }, properties: [{ id: 'custom.width', value: 220 }] },
    ] },
  },
  panel(102, 'Teams using both — reported', 'stat', overview.targets(q.overviewOverlap), 18, 1, 6, 6, description=overviewDescription + ' A team can use the two products in different environments. Partial inventories can miss teams using both.')
  + {
    datasource: overview.mixed,
    transformations: [
      { id: 'seriesToRows', options: {} },
      { id: 'filterFieldsByName', options: { include: { names: ['Value'] } } },
      { id: 'reduce', options: { reducers: ['sum'] } },
    ],
    fieldConfig+: { defaults+: { decimals: 0 } },
    options: { graphMode: 'none', colorMode: 'none', reduceOptions: { calcs: ['lastNotNull'], fields: '', values: false } },
  },
  globalHistory(103, 'Nais Postgres — reported total over time', q.overviewResources, 0),
  globalHistory(104, 'Cloud SQL (all engines) — reported total over time', q.overviewCloudsql, 12),
];

g.dashboard.new('Postgres – Adoption & Capacity')
+ g.dashboard.withUid('nais-postgres-adoption')
+ g.dashboard.withDescription('Overall reported adoption across configured tenant sources and all environments, followed by one repeated detail section per selected tenant. Controller-reported Nais Postgres resources versus Config Connector Cloud SQL instances (all engines; engine labels unavailable). Unknown is not zero. Capacity covers discovered Nais Postgres DB pods, branches and replicas, not Cloud SQL or total service cost. Historical coverage depends on metric availability; no retrospective inventory is reconstructed.')
+ g.dashboard.withTags(['nais', 'postgres', 'adoption', 'capacity'])
+ g.dashboard.time.withFrom('now-30d')
+ g.dashboard.withTimezone('browser')
+ g.dashboard.withEditable(false)
+ g.dashboard.withVariables([
  {
    name: 'tenant',
    label: 'Tenants',
    type: 'datasource',
    query: 'prometheus',
    regex: '/.*-metrics$/',
    multi: true,
    includeAll: true,
    refresh: 1,
    current: { text: 'All', value: '$__all' },
    options: [],
    description: 'All currently provisioned tenant metrics datasources, including platform/test tenants. Each selected tenant gets its own section; no cross-datasource totals are calculated.',
  },
  {
    name: 'environment',
    label: 'Environments',
    type: 'custom',
    query: 'prod,dev,non-prod,test,sandbox,ci',
    multi: true,
    includeAll: true,
    allValue: '.*',
    current: { text: 'All', value: '$__all' },
    options: [],
    description: 'All matches every environment, including future names. Named choices reflect the environments verified during implementation. Counts remain separated by environment.',
  },
])
+ {
  refresh: '5m',
  graphTooltip: 1,
}
+ g.dashboard.withPanels(globalPanels + std.map(function(p) p { gridPos+: { y: p.gridPos.y + 14 } }, [
  {
    id: 1,
    title: 'Tenant details — ${tenant:text}',
    type: 'row',
    collapsed: false,
    repeat: 'tenant',
    panels: [],
    gridPos: { x: 0, y: 0, w: 24, h: 1 },
  },
  panel(2,
        'Reported inventory by environment',
        'table',
        [target('A', q.inventory, '', true)],
        0,
        1,
        24,
        6,
        description='Nais Postgres: distinct logical postgres.nais.io resources reported by pgrator, not branches/pods. Cloud SQL: Config Connector SQLInstance resources, all engines and statuses, not SQL databases or HA nodes. Teams are namespaces within each environment; the same team in dev/prod appears in both rows. Unknown includes absent/empty inventories or failed sources; even a successful scrape cannot prove an empty inventory means zero. Config Connector resets/rebuilds its gauge periodically, so transient Unknown/partial counts can occur. These are reported observations, not an authoritative migration ledger.')
  + {
    options: { showHeader: true, cellHeight: 'sm' },
    transformations: [
      { id: 'groupingToMatrix', options: { rowField: 'k8s_cluster_name', columnField: 'signal', valueField: 'Value', emptyValue: 'null' } },
      { id: 'organize', options: {
        renameByName: { 'k8s_cluster_name\\signal': 'Environment' },
        indexByName: { 'k8s_cluster_name\\signal': 0, 'Nais Postgres': 1, 'Cloud SQL (all engines)': 2, 'Nais Postgres teams': 3, 'Cloud SQL teams': 4, 'Teams using both': 5, 'Nais Postgres branches': 6 },
      } },
    ],
    fieldConfig+: { defaults+: {
      decimals: 0,
      mappings: [{ type: 'value', options: { '-1': { text: 'Unknown', color: 'gray' } } }],
    } },
  },
  series(3, 'Reported resources over time', [
    target('A', q.resources, '{{k8s_cluster_name}} · Nais Postgres'),
    target('B', q.cloudsql, '{{k8s_cluster_name}} · Cloud SQL (all engines)'),
  ], 0, 7, description='Logical Nais Postgres resources versus KCC SQLInstance resources (all engines). Not branches/replicas, not event counters. Counts include non-ready resources reported by the controllers. Gaps are unknown; last legend value is only the latest observation in the selected range. Changes are net inventory changes, not proof of migration or counts of creates/deletes.'),
  series(4, 'Teams over time, per environment', [
    target('A', q.teams, '{{k8s_cluster_name}} · Nais Postgres'),
    target('B', q.cloudsqlTeams, '{{k8s_cluster_name}} · Cloud SQL (all engines)'),
  ], 12, 7, description='Distinct namespaces with reported resources in each environment. Teams using both appear in both series. No cross-environment unique-team total is implied. Unknown/no reported resources is not zero.'),
  series(5, 'Nais Postgres · CPU requested / used', [
    target('A', q.cpuRequested, '{{k8s_cluster_name}} · requested'),
    target('B', q.cpuUsed, '{{k8s_cluster_name}} · used'),
  ], 0, 14, 8, 'cores', capacity + ' CPU requested is Kubernetes container requests, not allocated node vCPU or a guarantee of CPU availability.'),
  series(6, 'Nais Postgres · memory requested / used', [
    target('A', q.memoryRequested, '{{k8s_cluster_name}} · requested'),
    target('B', q.memoryUsed, '{{k8s_cluster_name}} · working set'),
  ], 8, 14, 8, 'bytes', capacity + ' Usage is container working set, not a database cache-efficiency measure.'),
  series(7, 'Nais Postgres · data storage requested / used', [
    target('A', q.storageRequested, '{{k8s_cluster_name}} · PVC requested'),
    target('B', q.storageUsed, '{{k8s_cluster_name}} · filesystem used'),
  ], 16, 14, 8, 'bytes', capacity + ' Each pgdata PVC is counted once; filesystem usage includes WAL and overhead, not just SQL table data.'),
  series(8, 'Inventory source scrape status', [
    target('A', q.pgratorSource, '{{k8s_cluster_name}} · pgrator'),
    target('B', q.cloudsqlSource, '{{k8s_cluster_name}} · Config Connector recorder'),
  ], 0, 21, 24, description='1 = at least one endpoint successfully scraped; 0 = discovered but failed; a missing series is not healthy. Scrape success alone does not establish a complete, synced controller cache or valid inventory. This view does not use cumulative API watcher add/remove event counters as current resource counts.'),
]))
