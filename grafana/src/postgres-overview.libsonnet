// Grafana transformations combine explicit tenant queries in the browser.
local sources = import './monitoring-sources.libsonnet';
{
  sources: sources,
  scope: '%d configured tenant sources' % std.length(sources),
  mixed: { type: 'datasource', uid: '-- Mixed --' },
  selected(expr, uid): '(' + expr + ') and on() (label_replace(label_replace(vector(1), "source", "' + uid + '", "", ""), "selected", "$1", "source", "(${datasource:regex})") and on(selected) label_replace(vector(1), "selected", "' + uid + '", "", ""))',
  targets(expr, instant=true, legend='{{product}}|{{signal}}'): [
    {
      refId: 'T%d' % i,
      datasource: { type: 'prometheus', uid: sources[i].uid },
      expr: if std.type(expr) == 'function' then expr(sources[i]) else expr,
      instant: instant,
      range: !instant,
      format: 'time_series',
      legendFormat: if legend == 'tenant' then sources[i].name else legend,
    }
    for i in std.range(0, std.length(sources) - 1)
  ],
  // Use only for scalar metric totals, never to merge raw team names. Team
  // deduplication happens inside each tenant before their counts are summed.
  matrix: [
    { id: 'seriesToRows', options: {} },
    { id: 'extractFields', options: { source: 'Metric', format: 'regexp', regExp: '/^(?<Product>[^|]+)\\|(?<Signal>.+)$/' } },
    { id: 'groupBy', options: { fields: {
      Product: { operation: 'groupby', aggregations: [] },
      Signal: { operation: 'groupby', aggregations: [] },
      Value: { operation: 'aggregate', aggregations: ['sum'] },
    } } },
    { id: 'groupingToMatrix', options: { rowField: 'Product', columnField: 'Signal', valueField: 'Value (sum)', emptyValue: 'null' } },
    { id: 'organize', options: { renameByName: { 'Product\\Signal': 'Scope' } } },
  ],
  history: [
    { id: 'joinByField', options: { byField: 'Time', mode: 'outer' } },
    { id: 'calculateField', options: { mode: 'reduceRow', reduce: { reducer: 'sum' }, alias: 'Reported resources', replaceFields: true } },
  ],
}
