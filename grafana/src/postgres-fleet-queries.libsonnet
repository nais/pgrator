// All joins retain environment and namespace: identical database/pod names can
// exist in several environments. `up` includes failed scrapes, unlike SQL metrics.
local labels = 'k8s_cluster_name, namespace, team, postgres, branch';
local pods = labels + ', pod';
local podKey = 'k8s_cluster_name, namespace, pod';
local pvcKey = 'k8s_cluster_name, namespace, persistentvolumeclaim';
local selector = 'postgres!="", k8s_cluster_name=~"${environment:regex}", team=~"${team:regex}", postgres=~"${postgres:regex}", branch=~"${branch:regex}"';
local metric(name, extra='') = name + '{' + selector + (if extra == '' then '' else ', ' + extra) + '}';
local aggregate(op, by, expr) = '%s by (%s) (%s)' % [op, by, expr];
local podMetric(name, extra='') = aggregate('max', pods, metric(name, extra));
local targets = podMetric('up');
local inventory = aggregate('group', pods, targets);
local branches = aggregate('group', labels, targets);
local primary = '(' + podMetric('cnpg_pg_replication_in_recovery') + ' == bool 0) * on (' + pods + ') ' + podMetric('cnpg_collector_up');
local backup = aggregate('max', labels, metric('barman_cloud_cloudnative_pg_io_last_available_backup_timestamp'));
local recovery = aggregate('max', labels, metric('barman_cloud_cloudnative_pg_io_first_recoverability_point'));
local podVolumes = 'max by (' + podKey + ', persistentvolumeclaim) (kube_pod_spec_volumes_persistentvolumeclaims_info{volume="pgdata", k8s_cluster_name=~"${environment:regex}"})';
local volumeMetric(name) = 'max by (' + pvcKey + ') (' + name + '{k8s_cluster_name=~"${environment:regex}"})';
local volumeToBranch(expr, op='max') = aggregate(op,
                                                 labels,
                                                 '(' + expr + ') * on (' + pvcKey + ') group_right() ' + podVolumes
                                                 + ' * on (' + podKey + ') group_left(team, postgres, branch) ' + inventory);
local diskUsage = volumeToBranch('100 * ' + volumeMetric('kubelet_volume_stats_used_bytes') + ' / on (' + pvcKey + ') ' + volumeMetric('kubelet_volume_stats_capacity_bytes'));
local knownOrUnknown(expr) = '((' + expr + ') or (-1 * ' + branches + '))';
local primaries = '(' + aggregate('sum', labels, primary) + ' or (0 * ' + branches + '))';
local streaming = knownOrUnknown(aggregate('max', labels, podMetric('cnpg_pg_replication_streaming_replicas') + ' and on (' + pods + ') (' + primary + ' == 1)'));
local backupAge = knownOrUnknown('time() - (' + backup + ' > 0)');
local scrape = '100 * ' + aggregate('sum', labels, targets) + ' / ' + aggregate('count', labels, targets);
local telemetry = aggregate('sum', labels, inventory + ' unless on (' + pods + ') ' + podMetric('cnpg_collector_up')) + ' or (0 * ' + branches + ')';
local errors = aggregate('max', labels, metric('cnpg_collector_last_collection_error'));
// CNPG reports a numeric major.minor (e.g. 18.6), not server_version_num.
// Require every discovered pod to report the same positive major; never infer
// a branch version from its parent resource or choose a side during upgrades.
local versions = '(floor(' + metric('cnpg_collector_postgres_version') + ') > 0) and on (' + pods + ') ' + inventory;
local major = aggregate('max', labels, versions);
local majorVersion = knownOrUnknown(major
                                    + ' and on (' + labels + ') (' + major + ' == on (' + labels + ') ' + aggregate('min', labels, versions) + ')'
                                    + ' and on (' + labels + ') (' + aggregate('count', labels, aggregate('group', pods, versions))
                                    + ' == on (' + labels + ') ' + aggregate('count', labels, inventory) + ')');
// Footprint uses selected discovered DB pods, not the worst branch/pod.
local selectedPods = aggregate('group', podKey, inventory);
local selectedVolumes = aggregate('group', pvcKey, podVolumes + ' * on (' + podKey + ') group_left() ' + selectedPods);
local containerValue(name, extra='') = 'max by (' + podKey + ') (' + name + '{container="postgres", k8s_cluster_name=~"${environment:regex}"' + extra + '}) * on (' + podKey + ') ' + selectedPods;
local volumeValue(name) = volumeMetric(name) + ' * on (' + pvcKey + ') ' + selectedVolumes;
local samples(expr, objects) = '(count(' + expr + ') or (0 * count(' + objects + ')))';
local footprint = [
  {
    name: 'CPU (cores)',
    scale: 1,
    objects: selectedPods,
    requested: containerValue('kube_pod_container_resource_requests', ', resource="cpu"'),
    used: 'max by (' + podKey + ') (rate(container_cpu_usage_seconds_total{container="postgres", k8s_cluster_name=~"${environment:regex}"}[$__rate_interval])) * on (' + podKey + ') ' + selectedPods,
    capacity: '(' + containerValue('kube_pod_container_resource_limits', ', resource="cpu"') + ') > 0',
  },
  {
    name: 'Memory (GiB)',
    scale: 1073741824,
    objects: selectedPods,
    requested: containerValue('kube_pod_container_resource_requests', ', resource="memory"'),
    used: containerValue('container_memory_working_set_bytes'),
    capacity: '(' + containerValue('kube_pod_container_resource_limits', ', resource="memory"') + ') > 0',
  },
  {
    name: 'PVC (GiB)',
    scale: 1073741824,
    objects: selectedVolumes,
    requested: volumeValue('kube_persistentvolumeclaim_resource_requests_storage_bytes'),
    used: volumeValue('kubelet_volume_stats_used_bytes'),
    capacity: volumeValue('kubelet_volume_stats_capacity_bytes'),
  },
];
// Retain branch ownership while deduplicating each pgdata claim. Summing pod
// mappings directly would count a shared claim more than once in a branch.
local branchVolumes = aggregate('group',
                                pvcKey + ', team, postgres, branch',
                                podVolumes + ' * on (' + podKey + ') group_left(team, postgres, branch) ' + inventory);
local branchContainer(expr) = '(' + expr + ') * on (' + podKey + ') group_left(team, postgres, branch) ' + inventory;
local volumeDiscovery = aggregate('count', labels, aggregate('group',
                                                             pods,
                                                             podVolumes + ' * on (' + podKey + ') group_left(team, postgres, branch) ' + inventory))
                        + ' == on (' + labels + ') ' + aggregate('count', labels, inventory);
local branchVolume(name) = '(' + volumeMetric(name) + ' * on (' + pvcKey + ') group_right() ' + branchVolumes
                           + ') and on (' + labels + ') (' + volumeDiscovery + ')'
                           + ' and on (' + pvcKey + ') (' + aggregate('count', pvcKey, branchVolumes) + ' == 1)';
// A null is skipped by Grafana sum (even all-null columns sum to zero). Use a
// finite negative sentinel, larger in magnitude than any physical footprint,
// so missing measurements also make the footer negative and map to Unknown.
// Infinity is unsuitable: Grafana's table bypasses mappings for infinite values.
// Only fully measured branches contribute a non-negative value, including zero.
local branchSum(expr, objects, scale=1) = '((' + aggregate('sum', labels, expr)
                                          + ' and on (' + labels + ') (' + aggregate('count', labels, expr)
                                          + ' == on (' + labels + ') ' + aggregate('count', labels, objects) + ')) / ' + scale
                                          + ') or (-1e30 * ' + branches + ')';
local branchResources = [
  { name: 'CPU requested', expr: branchSum(branchContainer(footprint[0].requested), inventory) },
  { name: 'CPU used', expr: branchSum(branchContainer(footprint[0].used), inventory) },
  { name: 'Memory requested', expr: branchSum(branchContainer(footprint[1].requested), inventory) },
  { name: 'Memory used', expr: branchSum(branchContainer(footprint[1].used), inventory) },
  { name: 'PVC requested', expr: branchSum(branchVolume('kube_persistentvolumeclaim_resource_requests_storage_bytes'), branchVolumes) },
  { name: 'PVC capacity', expr: branchSum(branchVolume('kubelet_volume_stats_capacity_bytes'), branchVolumes) },
  { name: 'PVC used', expr: branchSum(branchVolume('kubelet_volume_stats_used_bytes'), branchVolumes) },
];
local capacitySignals(f) = [
  { name: 'Requested', expr: 'sum(' + f.requested + ') / ' + f.scale },
  { name: 'Used', expr: 'sum(' + f.used + ') / ' + f.scale },
  { name: 'Limit / capacity', expr: 'sum(' + f.capacity + ') / ' + f.scale },
  { name: 'Requested samples', expr: samples(f.requested, f.objects) },
  { name: 'Usage samples', expr: samples(f.used, f.objects) },
  { name: 'Limit / capacity samples', expr: samples(f.capacity, f.objects) },
  { name: 'Observed pods / PVCs', expr: 'count(' + f.objects + ')' },
];
local summary = [
  { name: 'Major version', expr: majorVersion },
  { name: 'Observed primaries', expr: primaries },
  { name: 'Streaming replicas', expr: streaming },
  { name: 'Scrapes OK', expr: scrape },
  { name: 'Missing SQL telemetry', expr: telemetry },
  { name: 'Backup age', expr: backupAge },
  { name: 'Disk used', expr: knownOrUnknown(diskUsage) },
];
{
  capacity: std.join(' or ', [
    'label_replace(label_replace((' + s.expr + '), "product", "' + f.name + '", "", ""), "signal", "' + s.name + '", "", "")'
    for f in footprint
    for s in capacitySignals(f)
  ]),
  branches: branches,
  majorVersion: majorVersion,
  primaries: primaries,
  streaming: streaming,
  backupAge: backupAge,
  scrape: scrape,
  diskUsage: diskUsage,
  summary: std.join(' or ', [
    'label_replace(label_join((' + item.expr + '), "resource", " / ", "k8s_cluster_name", "team", "postgres", "branch"), "signal", "' + item.name + '", "", "")'
    for item in summary + branchResources
  ]),
  noPrimary: primaries + ' != 1',
  noReplica: streaming + ' < 1',
  backupNeedsAttention: backupAge + ' < 0 or (' + backupAge + ') > 129600',
  scrapeProblems: scrape + ' < 100',
  sqlTelemetryMissing: telemetry + ' > 0',
  collectionErrors: errors,
  replicaLag: aggregate('max', labels, metric('cnpg_pg_replication_lag') + ' and on (' + pods + ') (' + podMetric('cnpg_pg_replication_in_recovery') + ' == 1)'),
  walPending: aggregate('max', labels, metric('cnpg_collector_pg_wal_archive_status', 'value="ready"')),
  walFailures: aggregate('sum', labels, 'increase(' + metric('cnpg_pg_stat_archiver_failed_count') + '[$__rate_interval])'),
  walArchiveAge: 'time() - (' + aggregate('max', labels, metric('cnpg_pg_stat_archiver_last_archived_time')) + ' > 0)',
  recoveryPoint: '(' + recovery + ' > 0) * 1000',
  lastBackupFailure: '(' + aggregate('max', labels, metric('barman_cloud_cloudnative_pg_io_last_failed_backup_timestamp')) + ' > 0) * 1000',
  diskFree: volumeToBranch(volumeMetric('kubelet_volume_stats_available_bytes'), op='min'),
  restarts: aggregate('sum',
                      labels,
                      'max by (' + podKey + ') (increase(kube_pod_container_status_restarts_total{container="postgres", k8s_cluster_name=~"${environment:regex}"}[$__range]))'
                      + ' * on (' + podKey + ') group_left(team, postgres, branch) ' + inventory),
  memory: aggregate('max',
                    labels,
                    '(max by (' + podKey + ') (container_memory_working_set_bytes{container="postgres", k8s_cluster_name=~"${environment:regex}"})'
                    + ' / on (' + podKey + ') max by (' + podKey + ') (kube_pod_container_resource_limits{container="postgres", resource="memory", k8s_cluster_name=~"${environment:regex}"} > 0))'
                    + ' * on (' + podKey + ') group_left(team, postgres, branch) ' + inventory + ' * 100'),
  controllerErrors: 'sum by (k8s_cluster_name, controller) (rate(controller_runtime_reconcile_errors_total{k8s_cluster_name=~"${environment:regex}", namespace="nais-system", controller=~"postgres.*[.]nais[.]io"}[$__rate_interval]))',
}
