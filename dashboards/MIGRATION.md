# Manual Git Sync setup

Both Postgres dashboards belong in this repository. Central administrators manage
the existing Git Sync connection manually; there is no provisioning Job, Helm
release, operator or component-CI access to Grafana.

## Connection

| Setting | Value |
| --- | --- |
| Grafana | `https://monitoring.nais.io` |
| Existing repository / folder identity | `nais-monitoring` |
| Display name | `Nais monitoring` |
| Public Git repository | `https://github.com/nais/pgrator.git` |
| Branch | `main` |
| Path | `dashboards/generated/` |
| Sync | Read-only, 60-second polling |
| Write workflows | Disabled / empty |
| Webhooks | Disabled |
| Git credentials | None; anonymous public read |

Use the existing connection, not a second connection targeting the same UIDs.
Keep `nais-postgres-fleet` and `nais-postgres-adoption`, their filenames and folder
unchanged so dashboard links and ownership survive source changes. Datasources,
query permissions and Grafana access remain centrally managed. Component CI only
validates the files; it needs no production credentials.

## Dashboard changes

Edit Jsonnet, compile and run `make -C dashboards check`, then commit sources and
generated resources together. After publication on `main`, Grafana polls the
files directly. No pgrator build/deploy is needed for dashboard-only changes.
Generation checks are not PromQL, datasource-coverage or rendering tests; see
[README.md](README.md) for validation and interpretation.

## Manual source changes and recovery

Before changing the connection, verify both generated definitions at the exact
destination commit. Preserve repository/folder/dashboard identities and read-only
settings. Change only the intended URL/ref/path, wait for successful sync at the
expected commit, then check definitions, rendering and filters. Never delete or
convert the dashboards to resolve a source error.

Git Sync can delete managed dashboards when files disappear from its source.
Do not delete the currently selected branch or files before a verified switch.
To roll back, first confirm an intact source, then change the existing connection
back. The original intact `nais/dashboards` baseline is
`dafce1a1b00417ea032e387aec5265058b6322e2`; its pilot branch tip was subsequently
cleaned and is not a rollback source. For anonymous URL changes, Grafana 13.2.2
requires explicit `secure.token.remove: true`; confirm no stored Git credentials
before using it. A branch-only change does not require a credential change.
