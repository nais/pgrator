local g = import './g.libsonnet';
local q = import './postgres-fleet-queries.libsonnet';
local variables = import './postgres-fleet-variables.libsonnet';
local datasource = { type: 'prometheus', uid: '${datasource}' };
local legend = '{{k8s_cluster_name}} / {{team}} / {{postgres}} / {{branch}}';
local thresholds(steps) = { mode: 'absolute', steps: steps };
local unknown = [{ type: 'value', options: { '-1': { text: 'Unknown', color: 'gray' } } }];
local attention = thresholds([{ value: null, color: 'green' }, { value: 1, color: 'red' }]);
local backupThresholds = thresholds([
  { value: null, color: 'gray' },
  { value: 0, color: 'green' },
  { value: 129600, color: 'yellow' },
  { value: 172800, color: 'red' },
]);
local diskThresholds = thresholds([
  { value: null, color: 'gray' },
  { value: 0, color: 'green' },
  { value: 80, color: 'yellow' },
  { value: 90, color: 'red' },
]);
local target(expr, instant=false) = {
  refId: 'A',
  datasource: datasource,
  editorMode: 'code',
  expr: expr,
  legendFormat: legend,
  instant: instant,
  range: !instant,
  format: if instant then 'table' else 'time_series',
};
local panel(id, title, type, expr, x, y, w=12, h=7, unit='short', description='') = {
  id: id,
  title: title,
  type: type,
  description: description,
  datasource: datasource,
  gridPos: { x: x, y: y, w: w, h: h },
  targets: [target(expr, type == 'table' || type == 'stat')],
  fieldConfig: {
    defaults: { unit: unit, noValue: 'Unknown', mappings: unknown, color: { mode: 'palette-classic' } },
    overrides: [],
  },
};
local series(id, title, expr, x, y, unit='short', description='') =
  panel(id, title, 'timeseries', expr, x, y, unit=unit, description=description)
  + {
    fieldConfig+: { defaults+: { custom: { drawStyle: 'line', lineWidth: 1, fillOpacity: 5, spanNulls: false, showPoints: 'never' } } },
    options: { legend: { displayMode: 'table', placement: 'bottom', calcs: ['lastNotNull', 'max'] }, tooltip: { mode: 'multi', sort: 'desc' } },
  };
// Real rows delimit Grafana repeat scope. Keep inner section headings as text
// panels so every operational panel repeats, not only the first section.
local row(id, title, y) = { id: id, title: title, type: 'text', transparent: true, options: { mode: 'markdown', content: '' }, gridPos: { x: 0, y: y, w: 24, h: 1 } };
local stat(id, title, expr, x, w, risk=true) =
  panel(id, title, 'stat', expr, x, 0, w=w, h=3)
  + {
    targets: [target(expr) + { instant: true, range: false }],
    fieldConfig+: { defaults+: { color: { mode: if risk then 'thresholds' else 'fixed', fixedColor: 'blue' }, thresholds: attention, decimals: 0 } },
    options: { colorMode: 'value', graphMode: 'none', textMode: 'auto', reduceOptions: { calcs: ['lastNotNull'], fields: '', values: false } },
  };
local countRisk(expr) = 'count(' + expr + ') or (0 * count(' + q.branches + '))';
local table(id, title, expr, x, y, unit, description) =
  panel(id, title, 'table', expr, x, y, description=description)
  + {
    fieldConfig+: {
      overrides: [{ matcher: { id: 'byName', options: title }, properties: [{ id: 'unit', value: unit }] }],
    },
    options: { showHeader: true, cellHeight: 'sm' },
    transformations: [{ id: 'organize', options: {
      excludeByName: { Time: true, namespace: true },
      renameByName: { k8s_cluster_name: 'Environment', team: 'Team', postgres: 'Postgres', branch: 'Branch', Value: title },
    } }],
  };
local override(name, properties) = { matcher: { id: 'byName', options: name }, properties: properties };

local resourceColumns = ['CPU requested', 'CPU used', 'Memory requested', 'Memory used', 'PVC requested', 'PVC capacity', 'PVC used'];

g.dashboard.new('Nais Postgres – Fleet')
+ g.dashboard.withUid('nais-postgres-fleet')
+ g.dashboard.withDescription('Provider operations for Nais Postgres (CNPG): one, several or all tenant datasources, with separate repeated sections and branch-level CPU/memory/PVC resources with tenant totals. Observed primaries, replication, Barman backups, WAL and storage; not complete inventory, end-to-end availability or a restore guarantee.')
+ g.dashboard.withTags(['nais', 'postgres', 'cnpg', 'operations'])
+ g.dashboard.time.withFrom('now-6h')
+ g.dashboard.withTimezone('browser')
+ g.dashboard.withEditable(false)
+ g.dashboard.withVariables(variables)
+ {
  refresh: '5m',
  graphTooltip: 1,
}
+ g.dashboard.withPanels([
  { id: 102, title: '${datasource:text}', type: 'row', collapsed: false, repeat: 'datasource', panels: [], gridPos: { x: 0, y: 0, w: 24, h: 1 } },
] + std.map(function(p) p { gridPos+: { y: p.gridPos.y + 1 } }, std.sort([
  stat(2, 'Observed branches', 'count(' + q.branches + ')', 0, 4, risk=false),
  stat(3, 'Primary count ≠ 1', countRisk(q.noPrimary), 4, 5),
  stat(4, 'Replica absent / unknown', countRisk(q.noReplica), 9, 5),
  stat(5, 'Backup old / unknown', countRisk(q.backupNeedsAttention), 14, 5),
  stat(6, 'Scrape failures', countRisk(q.scrapeProblems), 19, 5),
  row(10, 'Availability and replication — observations, not an end-to-end probe', 15),
  series(11, 'Observed primary count', q.primaries, 0, 16, description='Zero includes missing SQL telemetry. Does not test SQL read/write, pooler, network or credentials.'),
  series(12, 'Streaming replicas at primary', q.streaming, 12, 16, description='Actual streaming connections. Desired replica count is not exported here; one connection does not prove the configured HA topology is satisfied.'),
  series(13, 'Replica replay lag', q.replicaLag, 0, 23, 's', 'Maximum observed replica lag per branch. Missing replicas produce missing data, not zero lag.'),
  series(14, 'Missing SQL telemetry / discovered targets', q.sqlTelemetryMissing, 12, 23, description='Targets without cnpg_collector_up. Healthy branches have no matching series in this panel; consult the fleet table for explicit counts.'),
  row(20, 'Backups and WAL — investigate unknowns; verify recovery with restore drills', 30),
  series(21, 'Age of latest available basebackup', q.backupAge, 0, 31, 's', 'Barman-plugin timestamp; -1 means no positive timestamp reported. Daily backups: >36h is a triage hint, not an agreed SLO.')
  + { fieldConfig+: { defaults+: { thresholds: backupThresholds } } },
  table(22, 'First recoverability point (reported)', q.recoveryPoint, 12, 31, 'dateTimeAsIso', 'Barman archive metadata, not a proof of WAL continuity. Missing/zero timestamps are omitted; do not infer a 30-day restore guarantee.'),
  series(23, 'WAL files waiting for archive', q.walPending, 0, 38, description='Maximum ready-to-archive files on any observed pod in a branch. Persistent or growing backlog warrants investigation.'),
  series(24, 'WAL archive failures', q.walFailures, 12, 38, description='Increase in failed archive attempts over $__rate_interval. Retries can occur; inspect backlog and successful progress together.'),
  series(25, 'Time since last successful WAL archive', q.walArchiveAge, 0, 45, 's', 'Informational only. Idle databases may not generate new WAL; age alone is not an archive-stalled alarm. Only positive archive timestamps are shown.'),
  table(26, 'Last failed backup (reported)', q.lastBackupFailure, 12, 45, 'dateTimeAsIso', 'Historical failures can remain after a successful backup. Compare with latest available backup; an empty table is not proof of working backups.'),
  row(30, 'Capacity — worst-pod observations', 52),
  panel(7,
        'Fleet — environment / team / Postgres / branch',
        'table',
        q.summary,
        0,
        3,
        w=24,
        h=12,
        description='Current observations, including replicas and inactive branches. Major version requires a positive CNPG version from every discovered pod with agreement across pods; otherwise Unknown. It is not the desired version from the parent Postgres resource. CPU is requested cores and observed CPU rate; memory is requested bytes and working set; PVC is requested size, actual filesystem capacity and bytes used. Each pgdata claim is counted once per branch. Resource footers sum this tenant and the selected filters, not all tenants. Missing/partial measurements make the affected branch cell and resource total Unknown, never zero or a silently partial footprint. Missing pod discovery remains outside this inventory. Excludes poolers, operators, backup objects, network and logging. Exactly one observed primary is expected; streaming replicas are actual connections, not desired HA count. Colour thresholds are triage hints, not alert policies.')
  + {
    options: { showHeader: true, cellHeight: 'sm' },
    transformations: [
      { id: 'groupingToMatrix', options: { rowField: 'resource', columnField: 'signal', valueField: 'Value', emptyValue: 'null' } },
      { id: 'organize', options: { renameByName: { 'resource\\signal': 'Environment / Team / Postgres / Branch' }, indexByName: {
        'resource\\signal': 0,
        'Major version': 1,
        'Observed primaries': 2,
        'Streaming replicas': 3,
        'Scrapes OK': 4,
        'Missing SQL telemetry': 5,
        'Backup age': 6,
        'Disk used': 7,
      } + { [resourceColumns[i]]: i + 8 for i in std.range(0, std.length(resourceColumns) - 1) } } },
      { id: 'sortBy', options: { sort: [{ field: 'Environment / Team / Postgres / Branch' }] } },
    ],
    fieldConfig+: {
      defaults+: { color: { mode: 'thresholds' }, custom: { cellOptions: { type: 'color-text' }, filterable: true, width: 85, minWidth: 70, wrapHeaderText: true } },
      overrides: [
        override('Environment / Team / Postgres / Branch', [{ id: 'custom.width', value: 260 }]),
        override('Major version', [
          { id: 'displayName', value: 'PG major' },
          { id: 'unit', value: 'none' },
          { id: 'decimals', value: 0 },
          { id: 'custom.width', value: 75 },
          { id: 'custom.cellOptions', value: { type: 'auto' } },
        ]),
        {
          matcher: { id: 'byNames', options: { mode: 'include', names: resourceColumns } },
          properties: [
            { id: 'custom.footer.reducers', value: ['sum'] },
            { id: 'custom.cellOptions', value: { type: 'auto' } },
            { id: 'unit', value: 'bytes' },
            { id: 'decimals', value: 2 },
            { id: 'custom.width', value: 95 },
            { id: 'mappings', value: [{ type: 'range', options: { to: -1, result: { text: 'Unknown', color: 'gray' } } }] },
          ],
        },
        { matcher: { id: 'byNames', options: { mode: 'include', names: ['CPU requested', 'CPU used'] } }, properties: [{ id: 'unit', value: 'cores' }] },
        override('Observed primaries', [
          { id: 'displayName', value: 'Primary' },
          { id: 'custom.width', value: 90 },
          { id: 'thresholds', value: thresholds([{ value: null, color: 'red' }, { value: 1, color: 'green' }, { value: 2, color: 'red' }]) },
          { id: 'decimals', value: 0 },
        ]),
        override('Streaming replicas', [
          { id: 'displayName', value: 'Replicas' },
          { id: 'custom.width', value: 90 },
          { id: 'thresholds', value: thresholds([{ value: null, color: 'gray' }, { value: 0, color: 'yellow' }, { value: 1, color: 'green' }]) },
          { id: 'decimals', value: 0 },
        ]),
        override('Scrapes OK', [
          { id: 'unit', value: 'percent' },
          { id: 'thresholds', value: thresholds([{ value: null, color: 'red' }, { value: 100, color: 'green' }]) },
        ]),
        override('Missing SQL telemetry', [{ id: 'displayName', value: 'SQL gaps' }, { id: 'thresholds', value: attention }, { id: 'decimals', value: 0 }]),
        override('Backup age', [{ id: 'unit', value: 's' }, { id: 'thresholds', value: backupThresholds }]),
        override('Disk used', [{ id: 'unit', value: 'percent' }, { id: 'thresholds', value: diskThresholds }]),
      ] + [
        override(name, [{ id: 'displayName', value: std.strReplace(name, 'requested', 'req.') }])
        for name in ['CPU requested', 'Memory requested', 'PVC requested']
      ],
    },
  },
  series(31, 'Data volume used', q.diskUsage, 0, 53, 'percent', 'Highest pgdata filesystem usage per branch, joined via the pod/PVC relationship. Uses actual filesystem capacity, not requested disk size.')
  + { fieldConfig+: { defaults+: { thresholds: diskThresholds } } },
  series(32, 'Least free data volume space', q.diskFree, 12, 53, 'bytes', 'Lowest available pgdata filesystem space per branch. Missing kubelet/PVC metrics are unknown, not zero usage.'),
  series(33, 'Postgres memory / limit', q.memory, 0, 60, 'percent', 'Highest container working set / positive memory limit per branch. No limit or missing metrics yields no observation; this does not diagnose memory pressure by itself.'),
  table(34, 'Postgres restarts in selected time range', q.restarts, 12, 60, 'short', 'Sum of container restarts for currently discovered database pods. Deleted/replaced pods are not a complete incident history.'),
  row(40, 'Control plane — environment scope only, unaffected by team/Postgres/branch filters', 67),
  series(41, 'Pgrator reconciliation errors / second', q.controllerErrors, 0, 68, 'ops', 'All Postgres controllers in the selected environments. Reconciliation errors are not an inventory of stalled database creation.')
  + { targets: [target(q.controllerErrors) + { legendFormat: '{{k8s_cluster_name}} / {{controller}}' }] },
  series(42, 'CNPG SQL metric collection errors', q.collectionErrors, 12, 68, description='Maximum last-collection-error flag per branch. Missing telemetry is separately visible in the fleet table.'),
], function(p) p.gridPos.y * 24 + p.gridPos.x)))
