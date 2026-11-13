// Adoption uses controller resource inventory, never SQL sessions or pod counts.
// Detail counts retain environments; overview totals report inventory coverage.
local env = 'k8s_cluster_name=~"${environment:regex}"';
local podKey = 'k8s_cluster_name, namespace, pod';
local pvcKey = 'k8s_cluster_name, namespace, persistentvolumeclaim';
local source(job) = 'max by (k8s_cluster_name) (up{job="' + job + '", ' + env + '})';
local pgrator = source('pgrator-metrics-service');
local recorder = source('cnrm-resource-stats-recorder-service');
local info(kind) = 'max by (k8s_cluster_name, exported_namespace, name) (pgrator_resource_info{resource_type="' + kind + '", ' + env + '})';
local sql = 'max by (k8s_cluster_name, exported_namespace, status) (configconnector_applied_resources_total{group_version_kind="SQLInstance.sql.cnrm.cloud.google.com", ' + env + '})';
// No zero fallback: an exporter can be up before its inventory is populated.
// -1 produces an explicit Unknown cell for a discovered but empty/failed source.
local reported(expr, health) = '(((' + expr + ') and on (k8s_cluster_name) (' + health + ' == 1)) or (-1 * (0 * ' + health + ' + 1)))';
local resources = reported('count by (k8s_cluster_name) (' + info('postgres.nais.io') + ')', pgrator);
local branches = reported('count by (k8s_cluster_name) (' + info('postgresbranch.nais.io') + ')', pgrator);
local sqlResources = reported('sum by (k8s_cluster_name) (' + sql + ')', recorder);
local teams(expr, health) = reported('count by (k8s_cluster_name) (group by (k8s_cluster_name, exported_namespace) (' + expr + ' > 0))', health);
local pgTeams = teams(info('postgres.nais.io'), pgrator);
local sqlTeams = teams(sql, recorder);
local bothTeams = reported(
  '(count by (k8s_cluster_name) (group by (k8s_cluster_name, exported_namespace) (' + info('postgres.nais.io') + ') and on (k8s_cluster_name, exported_namespace) group by (k8s_cluster_name, exported_namespace) (' + sql + ' > 0))) or (0 * ((' + resources + ' >= 0) and on (k8s_cluster_name) (' + sqlResources + ' >= 0)))',
  '(' + pgrator + ' * on (k8s_cluster_name) ' + recorder + ')'
);
local pods = 'group by (' + podKey + ') (up{job="nais-system/cloudnative-pg-postgres", postgres!="", ' + env + '})';
local podMetric(name, extra='') = 'max by (' + podKey + ') (' + name + '{container="postgres", ' + env + (if extra == '' then '' else ', ' + extra) + '})';
local fromPods(expr) = 'sum by (k8s_cluster_name) ((' + expr + ') * on (' + podKey + ') ' + pods + ')';
local volumes = 'group by (' + pvcKey + ') (max by (' + podKey + ', persistentvolumeclaim) (kube_pod_spec_volumes_persistentvolumeclaims_info{volume="pgdata", ' + env + '}) * on (' + podKey + ') group_left() ' + pods + ')';
local fromVolumes(name) = 'sum by (k8s_cluster_name) (max by (' + pvcKey + ') (' + name + '{' + env + '}) * on (' + pvcKey + ') ' + volumes + ')';
local signals = [
  { name: 'Nais Postgres', expr: resources },
  { name: 'Cloud SQL (all engines)', expr: sqlResources },
  { name: 'Nais Postgres teams', expr: pgTeams },
  { name: 'Cloud SQL teams', expr: sqlTeams },
  { name: 'Teams using both', expr: bothTeams },
  { name: 'Nais Postgres branches', expr: branches },
];
local allEnvironments(expr) = std.strReplace(expr, env, 'k8s_cluster_name=~".*"');
local expectedEnvironments = 'group by (k8s_cluster_name) ((' + pgrator + ') or (' + recorder + '))';
local zeroCount(expr) = '(count(' + expr + ') or vector(0))';
local observedTeams(expr, health) = 'group by (exported_namespace) ((' + expr + ' > 0) and on (k8s_cluster_name) (' + health + ' == 1))';
local uniquePgTeams = observedTeams(info('postgres.nais.io'), pgrator);
local uniqueSqlTeams = observedTeams(sql, recorder);
local globalProducts = [
  { name: 'Nais Postgres', resources: resources, teams: uniquePgTeams },
  { name: 'Cloud SQL (all engines)', resources: sqlResources, teams: uniqueSqlTeams },
];
local globalSignals(p) = [
  { name: 'Reported resources', expr: 'sum(' + p.resources + ' >= 0)' },
  { name: 'Unique teams (reported)', expr: 'count(' + p.teams + ')' },
  { name: 'Environments with inventory', expr: zeroCount(p.resources + ' >= 0') },
  { name: 'Observed environments', expr: zeroCount(expectedEnvironments) },
  { name: 'Tenants with inventory', expr: '(clamp_max(' + zeroCount(p.resources + ' >= 0') + ', 1))' },
  { name: 'Configured tenants', expr: 'vector(1)' },
];
{
  // Every overview expression ignores the detail filters. Team identity remains
  // inside the tenant query; summing these counts preserves tenant+namespace.
  overviewInventory: allEnvironments(std.join(' or ', [
    'label_replace(label_replace((' + s.expr + '), "product", "' + p.name + '", "", ""), "signal", "' + s.name + '", "", "")'
    for p in globalProducts
    for s in globalSignals(p)
  ])),
  overviewOverlap: allEnvironments('(count((' + uniquePgTeams + ') and on (exported_namespace) (' + uniqueSqlTeams + '))) or (0 * sum(' + resources + ' >= 0) * sum(' + sqlResources + ' >= 0))'),
  overviewResources: allEnvironments('sum(' + resources + ' >= 0)'),
  overviewCloudsql: allEnvironments('sum(' + sqlResources + ' >= 0)'),
  // Table includes unknown source observations; charts omit sentinels, leaving gaps.
  inventory: std.join(' or ', ['label_replace((' + s.expr + '), "signal", "' + s.name + '", "", "")' for s in signals]),
  resources: '(' + resources + ') >= 0',
  cloudsql: '(' + sqlResources + ') >= 0',
  teams: '(' + pgTeams + ') >= 0',
  cloudsqlTeams: '(' + sqlTeams + ') >= 0',
  branches: '(' + branches + ') >= 0',
  cpuRequested: fromPods(podMetric('kube_pod_container_resource_requests', 'resource="cpu"')),
  cpuUsed: fromPods('max by (' + podKey + ') (rate(container_cpu_usage_seconds_total{container="postgres", ' + env + '}[$__rate_interval]))'),
  memoryRequested: fromPods(podMetric('kube_pod_container_resource_requests', 'resource="memory"')),
  memoryUsed: fromPods(podMetric('container_memory_working_set_bytes')),
  storageRequested: fromVolumes('kube_persistentvolumeclaim_resource_requests_storage_bytes'),
  storageUsed: fromVolumes('kubelet_volume_stats_used_bytes'),
  pgratorSource: pgrator,
  cloudsqlSource: recorder,
}
