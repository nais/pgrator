# Git Sync ownership, access and migration

## Component-dashboard pattern (proposal)

This is a proposal for other component repositories, not a configured organisation-
wide integration. It separates three kinds of ownership:

| Owner | Owns | Does not receive |
| --- | --- | --- |
| Component repository | Jsonnet, pinned dependencies, generated dashboard resources, query semantics and validation | Grafana admin credentials, production datasource secrets, GitHub write credentials |
| Central observability configuration/admins | Allowed source repo/ref/path, repository identity, folder, permissions and Grafana-to-Git access | Automatic permission to edit component source through Grafana |
| Central datasource provisioning | Datasources, tenant credentials and query authorization | Secrets in dashboard JSON or component CI |

Use a separate read-only Git Sync repository and dedicated folder for each new
component, restricted to `dashboards/generated/`. Generated files retain stable,
component-prefixed UIDs. Polling reads only that path; workflows and webhooks are
disabled. Changes travel **source → generated JSON in Git → Grafana**, never back
into Git from Grafana. There is no second API/Grizzly/Helm dashboard publisher.
Provisioning requests belong in the established central infrastructure/config
review process, not in every component's CI. The exact central configuration
owner/path and automation still need agreement; no Terraform changes are included.

### Access model

- **Public repo:** anonymous read, as used successfully by the existing pilot.
  Grafana needs outbound access to GitHub, not inbound GitHub access through IAP.
  No PAT, App, GitHub token or datasource secret belongs in component CI.
  The Grafana UI wizard may still demand a token; do not create one to satisfy
  the UI. Use the approved central provisioning mechanism.
- **Private repo:** do not assume anonymous Git access works. Evaluate a centrally
  owned GitHub App, selected-repository installation and read-only contents plus
  required metadata permissions. Confirm Grafana's connection/API support,
  credential storage, rotation and actual permission requirements before creating
  an integration. Disable write/PR workflows and avoid personal tokens or an
  organisation-wide repository grant. This has not been tested or configured.
- **Grafana/API access:** normal contributor CI validates files only. It needs no
  Grafana login or kubecontext. Repository setup/cutover is an explicitly approved
  admin operation using existing authenticated access. IAP is not bypassed or
  weakened. Dashboard visibility and datasource/query authorization remain
  centrally governed; Git read access does not grant metric access.
- **Change authorization:** read-only sync prevents Grafana writing Git, not Git
  authors changing dashboard queries/links/content. A production source needs an
  agreed reviewed/protected ref and ownership/review policy. Both pilot repos'
  main protection was absent when inspected; selecting `main` alone is not a
  security boundary. Do not silently add branch protection or install an App.
  Do not make a path-filtered check globally required without handling non-dashboard
  PRs, which otherwise wait forever for a skipped check.

Per-component integration isolation limits accidental changes; folder scoping is
not a security sandbox for hostile dashboard authors with privileged datasource
access. Grafana's accepted format, permissions, URL policies and datasource
controls still apply.

## Existing Postgres pilot

The existing central repository identity is `nais-monitoring`, with folder
`nais-monitoring` and title `Nais monitoring`. It reads public `nais/dashboards`,
branch `postgres-fleet-git-sync`, path `dashboards/monitoring/`, polling every 60s,
read-only with no webhooks/workflows. Last verified source revision before this
migration is `dafce1a1b00417ea032e387aec5265058b6322e2`.

The two dashboards have UIDs `nais-postgres-fleet` and `nais-postgres-adoption`.
Their generated filenames stay `postgres-fleet.json` and `postgres-adoption.json`.
Keep both filenames relative to the sync root, UIDs, current repository identity
and folder during migration. Do not configure a second repository to adopt the
same UIDs or rename the existing manager/folder during the move.

## Cutover gates — existing Postgres source not switched yet

The separate Job smoke test against `monitoring.nais.io` has verified anonymous
Git sync from an isolated pgrator test branch, create/no-op/update behavior and
actual dashboard rendering. It did not move either Postgres dashboard. Central
access is available again, but source publication and the live cutover still
require explicit approval.

The local cleanup of `nais/dashboards` must **not** be published while Grafana
still follows that source, because Git Sync can treat removed files as deletions.
No automatic migration script or deploy target is provided.

### Isolated cross-repository qualification (Grafana 13.2.2)

Runtime testing with two local anonymous smart-HTTP Git repositories and the
complete dashboard definitions passed: forward URL/path change on the existing
`nais-monitoring`, rollback, repeated forward change, invalid-ref preservation
and recovery, and a subsequent polled edit. Repository/folder/dashboard identities,
creation times, ownership and complete definitions were preserved across the
source changes. Only the deliberate description edit changed dashboard content.
The loopback Git allowlist and insecure HTTP option were enabled only in the
isolated test process; they are not needed or proposed for public GitHub HTTPS.

Grafana rejects a URL change with no explicit secure-token operation, even when
both sources are anonymous. For this anonymous-to-anonymous migration, the tested
PUT includes `"secure": {"token": {"remove": true}}`. This is the API's explicit
clear operation, not a new token or a placeholder credential. Readback and sync
confirmed no stored Git-token reference. Before doing this centrally, verify that
the existing repository has no token and review the exact request. Never clear
credentials on an authenticated repository as an implicit migration side effect.
The reverse source change uses the same explicit anonymous-token clearing.

1. **Validate locally.** Install locked dependencies and run `make -C dashboards
   check`. Compare both generated resources byte-for-byte with the published
   baseline. Check that dashboard-only paths skip image/chart/deploy and E2E,
   while operator and mixed changes still run them. No production access required.
2. **Qualify in isolated Grafana 13.2.2.** Reproduce the current repository and
   resources, then test changing repo URL/ref/path on the *same* repository
   identity. Confirm whether the running API permits that operation, retains
   dashboard/folder identities, does not delete/recreate resources, and supports
   rollback to the old source. Test polling updates and unknown/error paths.
   Source-level support or a successful validation request is not sufficient.
   The isolated qualification above passes these gates. Actual GitHub source
   publication and central readback/rendering are still separate gates. If they
   fail, stop; never improvise Convert to local, deletion or a second owner.
3. **Publish the new source, separately approved.** Push/merge the pgrator files
   to the agreed reviewed ref, verifying the two generated files are reachable
   at that exact commit under `dashboards/generated/`. Do not push old-source
   cleanup yet. Changes to component workflow filters may run ordinary CI once;
   subsequent dashboard-only changes are excluded. No component deployment is
   needed to publish dashboard JSON.
4. **Read-only central preflight, once access returns.** Record the current
   repository config/ref, manager/folder/sourcePath, dashboard definitions and
   relevant resource identities. Confirm central version/capability matches the
   isolated test and that both destination resources exactly match. Review the
   proposed config diff and obtain approval before changing central configuration.
5. **Switch only the existing source.** If the isolated test supports it, change
   `nais-monitoring`'s URL to `https://github.com/nais/pgrator.git`, the approved
   ref and path `dashboards/generated/`, preserving identity/folder/read-only
   settings. Explicitly clear the absent token as described above; do not create
   credentials. Never delete/recreate the repository or folder. Wait for healthy
   sync at the exact destination commit, read back both definitions and verify
   actual rendering, datasources and filters without overlays. Sync alone is not
   a rendering test.
6. **Remove the old source, separately approved.** Only after step 5 succeeds,
   publish the prepared cleanup in `nais/dashboards`. Leave ingress, the legacy
   exporter dashboard and unrelated configuration untouched. Record the single
   active source in the central configuration and project card. Do not retain
   two editable sources or two publishers indefinitely.

Before old cleanup is published, rollback is the tested reverse source change on
the same repository identity, not resource deletion. After cleanup, a rollback
also needs restoration of the old source/ref before switching back. Keep the
baseline commit available. If sync fails, stop and inspect status rather than
adding a second publisher or deleting resources.
