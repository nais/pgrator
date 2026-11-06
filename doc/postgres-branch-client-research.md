# Avvikling av PostgresBranch: API, Console og CLI

Forskningsnote, 2026-11-06. Read-only undersøkelse av lokale primærkilder. Ingen applikasjonskode er endret og ingen tester er kjørt. Premiss fra oppdraget: bare `main` er aktiv; Postgres skal eie ressurser direkte og eksisterende main-CNPG-clustere skal adopteres uten endring for apper. Dette er **ikke** en verifisering av produksjonsinventaret.

## Kilder og avgrensning

Filreferanser nedenfor er relative til disse repoene:

| Prefiks | Lokal kilde | HEAD ved undersøkelsen |
| --- | --- | --- |
| `api/` | `/Users/Frode.Sundby/ws/api` | `da54f782ecf723c7cbaa8e0cfd9e024de3329601` |
| `console/` | `/Users/Frode.Sundby/ws/console-frontend` | `bcd1b1ffef8726a898fb9822b500322819cfc97f` |
| `cli/` | `/Users/Frode.Sundby/ws/cli` | `b9a1cf1b6f1d0e5985a7c4661cd3a44a6936b271` |

Rot-`AGENTS.md` er lest i alle tre repoene; glob fant ingen underordnede AGENTS.md. Særlig relevante føringer: API-generering etter schemaendringer (`api/AGENTS.md:35–42`), Houdini/cache og automatisk schema (`console/AGENTS.md:40–42,181–209`), og API-før-CLI samt branch-uavhengig navnecompletion (`cli/AGENTS.md:69–74`).

**Navneavklaring:** dagens Kubernetes API er `nais.io/v1`, resource **`postgres`**, kind `Postgres`; watcher bruker dette eksplisitt (`api/internal/persistence/postgres/dataloader.go:77–83`). `postgres.nais.io` er også dagens **CRD-navn** i enum-oppslaget (`available_values.go:15–26`). Formuleringen «postgres.nais.io/Postgres» bør derfor ikke alene tolkes som bestilling av en ny API-gruppe. Dersom planen faktisk innebærer ny gruppe, kommer separat GVR-/CRD-/RBAC- og objektidentitetsmigrering i tillegg. Denne noten anbefaler ikke en slik ekstra migrering.

## Konklusjon

Fjerning av Kubernetes-typen PostgresBranch kan gjøres før fjerning av GraphQL-typen, men **ikke med dagens API uendret**. Dagens API får metadata fra Postgres, men helse, konfigurasjon, workloadens branch og personlige tilganger krever en fysisk PostgresBranch. Console og allerede distribuerte CLI-versjoner spør fortsatt etter disse feltene. En kortlevd **virtuell main-branch som GraphQL-kompatibilitetsfasade**, basert på Postgres, er derfor et realistisk mellomtrinn. Nye klienter bør lese helse/konfigurasjon direkte på Postgres. Dette er en anbefaling, ikke implementert støtte i kildene.

De viktigste utrullingsrisikoene er:

1. Tapt readiness/access ved at main-branch forsvinner før API kan lese Postgres-status.
2. Nye CNPG-navn eller bortfall av `branch="main"`-metrics som ødelegger tilgangsoppslag, logger og grafer selv om databasen fortsatt kjører.
3. Branch-mutations fra gammel CLI/åpen Console-fane som fortsatt kan opprette eller aktivere branches.
4. Duplikater/stale cache under dual-read, eller feilaktig fallback ved autentiserings-/nettverksfeil.
5. Tap av historiske aktivitetstyper og non-null-relasjoner dersom GraphQL-schema ryddes samtidig med CRD-en.

## 1. API- og GraphQL-kontrakter

### Leseoverflaten i dag

Primærschema: `api/internal/graph/schema/postgres.graphqls`.

| Kontrakt | Kilde | Konsekvens |
| --- | --- | --- |
| `Team.postgreses` inkluderer Postgres uten branches, med filter/paginering | schema:14–27; `queries.go:236–251` | God stabil inngang for listing/completion. Må dedupliseres om to Postgres-kilder støttes. |
| `TeamEnvironment.postgres(name)` | schema:30–40; `queries.go:553–563` | Henter **direkte** fra K8s, ikke samme cache som team-listen. |
| Postgres metadata: version, extensions, HA, resources, labels | schema:184–229; `models.go:468–496` | Logisk Postgres-ID kan holdes stabil når eierskap endres. |
| `desiredActiveBranch`, nullable `activeBranch`, non-null `branch(name)` og `branches` | schema:202–226; `postgres.resolvers.go:102–127` | Å bare returnere `activeBranch: null` bryter ikke schema, men fjerner helse fra klientene. `branch` kan ikke erstattes med null. |
| Branch: `id,name,createdAt,earliestRestoreTime,configuration,state,workloads,issues,labels` | schema:120–168 | Status/konfigurasjon finnes i dag ikke direkte på GraphQL Postgres. |
| Branch state `AVAILABLE/PROGRESSING/DEGRADED` | schema:327–335; `models.go:451–528` | Readiness krever reconciled phase + observed generation og tolker CNPG/RecoveryFailed conditions. Må videreføres fra ny Postgres-status. |
| `WorkloadPostgres.branch!`, `.postgres!`, `.role`, `.envPrefix`; `PostgresWorkload.branch!` | schema:231–249 | Branch kan ikke slettes fra backendoppslaget uten at app-/jobbvisningene feiler. Legg til direkte målkontrakt før de gamle feltene fjernes. |
| `PostgresAccess.postgresBranch!` | schema:831–857; resolvers:138–139 | Også et eksisterende personlig access-objekt må kunne leses etter branch-adopsjon. |
| `TeamInventoryCounts.postgresBranches.total` | schema:822–829; `queries.go:259–261` | Teller fysiske branches, ikke Postgres. Må få ny kontrakt eller kompatibel avledet telling. |

`queries.go` og `models.go` i tabellen ligger under `api/internal/persistence/postgres/`.

Branch-oppslag i dag er kun branch-watcher: `queries.go:511–538`; branch-lister er kun samme watcher (`254–261,540–550`). Postgres hentes separat. Workloadvalg er eksplisitt `uses.postgres[].branch`, ellers **observert** active branch, ellers main; ønsket aktivering påvirker ikke valget (`queries.go:148–185,210–234,755–775`). Manglende branch gir feil, ikke stille filtrering (`integration_tests/postgres_branch_selection.lua:96`).

**Kompatibilitetsanbefaling:** bevar at både utelatt branch og eksplisitt `main` løser til samme adopterte database. Ikke remapp non-main til main i stillhet. «Kun main er aktiv» beviser ikke at det ikke finnes inaktive branches, pending aktivering, bindinger eller eksplisitte workloadreferanser til dem; gjør dette til et inventar-/migreringskrav.

### Identitet og status

Opaque ID-er har egne typer: `PBR` for branch (team/environment/Postgres/branch), `PG` for Postgres og `PA` for access (`api/internal/persistence/postgres/node.go:12–51`). Branch-ID er avledet av logisk navn, ikke branch-UID (`models.go:435–449`). En virtuell main kan derfor bevare `PBR`-ID samtidig som `PG`-ID bevares. Bytt ikke eksisterende branch-node til `__typename: Postgres` med samme ID; det er en annen GraphQL-kontrakt og cachetype.

Anbefalt additiv GraphQL-overflate: `Postgres.state`, `createdAt`, `configuration`, eventuelt `earliestRestoreTime` og `issues`, og en direkte Postgres-relasjon på access/workloadforbindelser der det mangler. Dette er foreslåtte felter; de finnes ikke alle i dagens schema. Nullable-felter må fortsatt representere «ennå ikke observert» fremfor å dikte opp defaults eller erklære adopsjon som frisk før reconciler har bekreftet den (`models.go:459–464,499–528`; schema:129–130).

### Mutations og skrivebaner

Schema har `createPostgres`, `updatePostgres`, `deletePostgres`, `createPostgresBranch`, `activatePostgresBranch`, `deletePostgresBranch` og `createPostgresAccess` (`postgres.graphqls:686–704`). Alle resolverne utfører domeneautorisering før operasjonen (`api/internal/graph/postgres.resolvers.go:44–91`). Bevar dette også ved deprecated mutations.

- Create/update skriver dagens `nais.io/v1 Postgres` (`queries.go:806–872,875–939`). Metadata og ressurskontrakten kan beholdes. Update konverterer til typet CR og tilbake; ved tillegg av migrerings-/identitetsfelter må Go-typen oppgraderes eller skrivebanen bevare ukjente felter. Branch-activate har allerede eksplisitt unstructured DeepCopy for å bevare nyere felter (`branch_operations.go:280–286`), mens vanlig update gjør typet roundtrip (`queries.go:893–924`).
- Branch-create bygger et fysisk `PostgresBranch`, med recovery fra kildebranch og eksplisitt target time; idempotens og recovery-vindu kontrolleres (`branch_operations.go:143–243`). Branch-activate skriver `spec.activeBranch` asynkront (`246–299`). Delete sletter branchobjektet og beskytter aktiv/ønsket/siste branch (`queries.go:40–128`).
- Disse branch-mutations må stoppes server-side **før** migreringen. Deprecated schema alene hindrer ikke gamle klienter. Returner tydelig «branching er avviklet»-feil uten K8s-write; ikke oversett branch-delete til whole-Postgres-delete. Eventuell `activate(main)`-no-op må være eksplisitt avtalt, autorisert og testet.
- Whole delete betyr bare `deletionRequested: true`, med asynkron finalizeropprydding, UID-precondition og fresh reads av app/jobb/binding-referanser (`delete_postgres.go:17–89`). Den hardkoder finalizer `postgres.nais.io` (`92–94`). Ny eiermodell må beholde eller bevisst migrere dette; klienten får ikke love fullført sletting.

### Personlig tilgang er også branch-avhengig

Input krever branch (`postgres.graphqls:715–730`). Validering krever tilgjengelig fysisk branch (`models.go:336–379`); `GetReadyPostgresBranch` verifiserer i tillegg CNPG `Ready=True` (`queries.go:468–508`). Cluster-navnet beregnes fra branch-navnet, ikke fra `status.clusterName` (`485`). Tidligste restore time bruker samme navneavledning og Barman ObjectStore, med gammel Cluster-fallback (`branch_operations.go:38–89`).

Access er `nais.io/v1 PostgresAccess`, har `spec.postgresBranch` og deterministisk navn basert på bruker + branchobjektnavn (`queries.go:565–568,595–610,697–737`). Existing access kan gjenbrukes og oppgraderes ved revoke/recreate (`619–649`). En flytting til `spec.postgres` må håndteres sammen med access-controller og dual-read for eksisterende objekter; det er ikke bare en GraphQL-rename. Bevar brukeridentitet, tilgangsnivå, TTL, TLS/relay-status og immutable access-semantikk. Ikke la ny navneavledning opprette to samtidige personlige roller/accesses for samme bruker/database.

Credentials leses separat på demand og kun av eier, ikke fra branch-watcher-cache (`models.go:595–608`; `queries.go:313–357`). Status/connection bruker branch-watcheren som klientfabrikk selv for access- og andre GVR-er (`queries.go:360–365,481,595,813`; `delete_postgres.go:96–101`). Disse skjulte avhengighetene må fjernes **før watcher fjernes**: deaktivert branch-watcher er ikke en generell dynamic-client-fabrikk.

### Issues, søk og gRPC

- Issue-checker leser bare branches og lagrer kritisk `PostgresBranchIssue` med fysisk branchobjektnavn (`api/internal/issue/checker/postgres.go:12–35`). GraphQL resolver slår issue tilbake opp via dette navnet (`api/internal/graph/issues.resolvers.go:112–117`). Ny helse må gi Postgres-issues, med en plan for eksisterende DB-lagrede issues og kompatibilitetsoppslag. Ellers blir databasefeil borte, eller non-null issue-relasjoner feiler.
- Søk bruker allerede logisk Postgres-watcher (`api/internal/cmd/api/http.go:260`; `integration_tests/postgres_search.lua:8,40,60`). Hold det branch-uavhengig.
- **Ikke bare GraphQL:** gRPC database-listing leser bare branch-watcheren (`api/internal/grpc/grpcdatabase/server.go:16–39`). Main presenteres med logisk Postgres-navn; non-main som `name/branch`; database er `app`, type `NAIS_POSTGRES` (`80–91`). Listing vil miste alle adopterte Postgres med uendret server. Bytt til samlet Postgres-lesemodell og bevar main-navn/type/paginering.

## 2. Watch, cache og abonnement

API starter separate watchere for Postgres og PostgresBranch (`api/internal/kubernetes/watchers/watchers.go:43–44,69–70,101–102`; `dataloader.go:61–85`) og injiserer dem i HTTP-context (`api/internal/cmd/api/http.go:384`).

Watch-data er informer/lister, ikke en egen persistent branch-cache: `api/internal/kubernetes/watcher/watcher.go:152–209`. Konvertering skjer også ved lister-read. Add/update/delete har hooks (`cluster_watcher.go:104–135`), men søk fant ingen Postgres-spesifikke abonnenter på slike hooks. Nye informere registreres ved oppstart; manglende registrering hoppes over og `HasSynced` er per miljø (`watcher.go:67–109`; `cluster_watcher.go:38–76`). Dual-read må derfor håndtere deploy/restart etter at CRD og RBAC finnes, samt sync før gammel kilde regnes som tom.

GraphQL-schema har log-/workloadLog-abonnement, men ikke Postgres-statusabonnement (`api/internal/graph/schema/log.graphqls:1–7`, `podlog.graphqls:1–9`; søk etter alle `type Subscription`/`extend type Subscription`). Console transport har SSE-plugin (`console/src/client.ts:1–9`); dette betyr ikke at Postgres-status push-oppdateres.

Console bruker Houdini `CacheAndNetwork` som default (`console/houdini.config.js:5–22`). Branch-siden poller hvert 10. sekund i synlig fane, stopper ved unmount, og refresher `NetworkOnly` etter mutation (`.../(single)/branches/+page.svelte:40–57,108–140,154–179`; `console/src/lib/domain/postgres/branchOperation.ts:14–38`). Oversikt poller også 10 sekunder, men bare når avledet status er progressing (`.../(single)/+page.svelte:104–123`). Nye direkte statusfelter må fortsatt gi korrekt progresjons-/feilvisning og ikke miste polling. Mutations må returnere relevante node-ID-er for normalisert cache; paginerte lister trenger egne refresh-/listestrategier (`console/AGENTS.md:200–203`).

**Anbefalt dual-read-regel:** én intern Postgres-lesemodell per `(environment,namespace,name)`. Velg ny/direkte status etter eksplisitt adopsjons-/readinesssignal, med gammel main-status som overgangskilde der signal mangler. Ikke foretrekk tom/ny status bare fordi objektet finnes. Ikke fallback fra forbidden/timeouts/malformed data til «ingen database» eller feil gammel skrivekilde. Samlet listing, branch-fasade, issues og gRPC må bruke samme dedupliserings-/kildevalg. Fresh reads og deletion guards skal fortsatt være fail-closed.

## 3. Console: endringsflate uten full UI-ombygging

Følgende ruteprefiks forkortes som `single/`: `console/src/routes/team/[team]/[env]/postgres/[postgres]/(single)/`.

- Teamliste leser allerede `Team.postgreses`, men helse leses via `activeBranch.state` og ønsket aktivering (`console/src/routes/team/[team]/postgres/query.gql:16–49`). Bytt til direkte Postgres-state uten å regressere visning av Postgres uten status/branch.
- Oversikt leser branch-konfigurasjon, branch-state og branch for hver workload (`single/query.gql:16–45`). Ny hovedside må ha samme informasjon direkte og ikke kreve branch.
- Branch-route har paginert branchliste og branch-usage (`single/branches/query.gql:7–35`). **Branch-creation er allerede fjernet fra Console**, men activate/delete er fortsatt registrert; tester bekrefter dette (`console/src/lib/domain/postgres/branchesPage.test.ts:65–106`; komponentens mutations:71–91). Fjern derfor ikke bare en «create»-knapp og anta at UI er branch-fri.
- Layout har egen Branches-tab og viderefører søkeparametre mellom Insights/Queries (`single/+layout.svelte:18–58`). `issueResource.ts:38–41` og issue-tester peker til `/branches`. Behold redirect/kompatibel leseside for gamle lenker ved utrulling; ikke la gamle aktivitet-/issue-lenker bli 404 over natten.
- Insights-query leser activeBranch og paginerte branches (`single/insights/query.gql:12–24`). Queries-load velger URL `?branch=`, ellers aktiv, ellers første branch (`single/queries/+page.ts:26–31`) før metrics-fetch (`40–54`). Avtal hvordan `?branch=main` normaliseres; avvis/forklar non-main-lenker fremfor å vise feil database.
- Observabilitet er en egen kompatibilitetskontrakt: PromQL inkluderer `namespace,team,postgres,branch` (`console/src/lib/domain/postgres/insights.ts:30–40`; `queryInsights.ts:20–29`). Fjernes branch-label samtidig, blir grafer/query statistics tomme. Behold `branch="main"` midlertidig eller rull ut nye selectors med historikk-/dedupliseringsstrategi før label fjernes.
- Logger velger aktiv/første branch (`single/logs/+page.svelte:12–23`) og matcher pod-prefix `pg-${postgres}-${branch}-`, med branch-spesifikk regex (`65–68`; `console/src/lib/utils/lokiLogQuery.ts:61,82,96`). Regex forventer åtte hextegn + instansnummer; testene utelukker nabobranch/pooler (`lokiLogQuery.test.ts:233–279`). **Adopsjon uten rename** lar dette fortsatt fungere; nye cluster-navn må ikke antas å følge gammel branch-prefix. Eksponer helst faktisk fysisk cluster/pod-identitet i en bevisst API-kontrakt fremfor å rekonstruere den i UI.
- App/jobb persistencevisning har eksplisitt branch-kontrakt, også to brukergrensesnittoppføringer til samme logiske database (`console/src/lib/domain/persistence/Persistence.test.ts:15–45`). Oppdater query/fragment og visning sammen.
- Inventar er ikke én ting: admin-teamquery bruker branchcount (`console/src/routes/admin/teams/query.gql:57`); `TeamInventory.test.ts:6–34` mocker branchcount; `teamRoles.gql:3–6` bruker allerede Postgres totalCount. Kartlegg og samordne disse før gammel inventory-field fjernes.
- Historiske branch-eventtyper inngår i felles aktivitetsoverflate (`console/src/routes/homeActivity.gql:225–245`, `src/lib/domain/activity/activityLogFragment.test.ts:33–35,80–98`, `activityTextComponents.ts:131–134`). Behold schema-/presentasjonsstøtte for gamle events selv når nye events aldri produseres.

Schema er API-generert og skal ikke håndredigeres (`console/AGENTS.md:40–42`). Oppdater API-schema-distribusjon og regenerer Houdini i klientendringen. Gamle åpne faner har gamle dokumenter; et nytt deploy er ikke tilstrekkelig grunn til straks å fjerne schemafeltene.

## 4. CLI: kommandoer, completion og output

- Registrerte kommandoer: `postgres list/get/create/update/delete/branch/psql/proxy` (`cli/internal/postgres/command/postgres.go:14–30`). Branch har `list/create/activate/delete`, fortsatt fulle skrivebaner (`command/branch.go:19–35,78–145,157–180,200–233`). Server-side stop er nødvendig også om ny CLI skjuler dem.
- `GetTeamPostgreses` leser/paginerer allerede Postgres uavhengig av branches, men output har `active_branch` (`cli/internal/postgres/list.go:12–18,34–70,74–85`). Completion og miljøvalg baseres på denne listen; ikke gå tilbake til branchlisting for å finne Postgres-navn (`cli/AGENTS.md:69–74`; `command/environment.go:78–129`).
- Get leser `desiredActiveBranch`, `activeBranch`, `branches(first:1000)` (`cli/internal/postgres/get.go:11–22`). Human/JSON-output har `activeBranch`, `requestedBranch`, `branches` og readiness fra BranchStatus (`command/get.go:15–27,70–100`; `command/readiness.go:8–44`). Ny klient kan lese direkte Postgres-status; bestem om JSON-feltene skal leve videre en periode for scripts. Ikke endre `Ready` til å bety SQL-connectivity; dagens hjelpetekst skiller tydelig (`get.go:53–54`).
- Branch-status og branch-mutations er separate genererte operasjoner (`cli/internal/postgres/branch.go:24–51,60–98,101–158`); sourcebranch-completion kommer derfra (`command/flag/flag.go:86–129`). Fjern/deprecate kode, help og completion samlet; behold eventuelt branch-list som read-only main-alias i overgangsperioden.
- `psql/proxy` har `--branch`, default aktiv (`command/flag/flag.go:18–36`). Uten flagget gjøres først `GetActivePostgresBranch` (`command/access.go:309–318`; `cli/internal/postgres/access.go:43–56`); **null aktiv branch stopper forbindelsen** med «specify --branch». Virtuell aktiv main eller oppgradert klient er nødvendig for ikke å miste vanlig tilgang etter adopsjon.
- Access polling er vanlig GraphQL polling, ikke subscription: status hvert sekund, 60 sekunders samlet setup-timeout, credentials først ved READY (`access.go:72–106,109–149`). Behold dette og eier-/TTL-/TLS/relay-kontrakten. Proxy JSON/env har egen scriptkontrakt med TLS-servernavn, loopback address og stdout-EOF (`command/access.go:109–127`); trenger normalt ingen endring ved ren adopsjon og skal regresjonstestes.
- CLI schema er hentet fra API og klienten er genqlient (`cli/AGENTS.md:49–53`). Deploy additivt API først, oppdater schema/query og generer klient etterpå. Støtte for gamle installerte binærer krever tidsavgrenset API-kompatibilitet, ikke at alle brukere oppdaterer samtidig.
- CLI activity-test dekker historiske branchtypenavn (`cli/internal/activity/activity_test.go:12–14`); ikke slett lesestøtte bare fordi branchkommandoene forsvinner.

## 5. Anbefalt utrullingsrekkefølge og porter

1. **Inventar og kontrakt før migrering.** Bekreft faktisk observed og desired branch = main/empty; finn inaktive branches, explicit non-main uses/bindings/accesses. Registrer fysiske main-cluster-identiteter, status/config og appenes effektive forbindelsesmateriale. Bare-aktiv-main er en forutsetning, ikke tillatelse til å slette inaktive data. Avklar om eksisterende K8s gruppe/navn beholdes.
2. **Stopp branch-skriving i API**, med tydelig deprecated/unsupported policy og uendrede autorisasjonskrav. Ta bort aktivering/sletting i ny Console/CLI, men behold schema for gamle dokumenter. Beskytt også mot andre writers utenfor disse repoene før operatoren slutter å støtte branches.
3. **Additiv CRD/status + RBAC, deretter API-lesestøtte.** Gi Postgres nødvendige observed felter; bevar gammel struktur under overgang. Dagens RBAC har Postgres get/list/watch/create/update/delete og branch get/list/watch/create/delete (`api/charts/console-backend-rbac/templates/rbac.yaml:130–179`, ekstra rolle:334–364). Ikke fjern gammel list/watch-tillatelse før gammel watcher er ute. Ny gruppe eller nye watched typer krever egne regler først. Verifiser informer discovery/sync per miljø og restart etter nye CRD-er.
4. **Deploy dual-read API og main-fasade før operator-adopsjon.** Dekk queries, readiness/access, issues, inventory, søk og gRPC; velg kilde konsekvent og dedupliser. Flytt klientfabrikkene bort fra branch-watcher. Eksponer direkte GraphQL-felter og deprecate gamle. Bevar ID-er, statussemantikk og gamle access-objekter.
5. **Rull operator-adopsjon gradvis.** Adopter eksisterende cluster og øvrige child-ressurser uten rename/recreate. Hold faktiske cluster-/service-/TLS-/secretidentiteter, data og observabilitetslabels stabile. Ikke deklarer ferdig bare fordi ownerReference er flyttet; kontroller status, tilgang og appforbindelser. Disse operatorinvariantene må verifiseres i pgrator-testene, ikke antas bevist av denne klientundersøkelsen.
6. **Flytt nye Console/CLI til direkte Postgres-kontrakt.** Regenerer schema/klientartefakter, endre readiness/UI, branch-parametre og gamle lenker. Behold main-kompatibilitet for gamle binærer/faner. Behold metric-label til alle lesere er tilpasset; hvis den fjernes, verifiser historiske tidsvinduer uten dobbelttelling.
7. **Fjern gammel CR/controller/watch etter dokumentert fullført adopsjon.** Ikke slett branch-CRD mens gamle ownerReferences/finalizers fortsatt kan føre til cascading delete eller mens API bruker watcher som klientfabrikk. Anbefalingen er en separat eksplisitt cleanup-port med inventarbevis og gRPC/access/issue-sjekker.
8. **Schemaopprydding sist.** Avvikle gamle GraphQL branch-readfelter/mutations først etter avtalt klientstøtteperiode eller versjonert kontrakt. Behold historiske activity-eventtyper dersom data fortsatt finnes. Fjern gammelt RBAC og fixtures til slutt. Et operator-rollback etter fysisk adopsjon må ha egen testet plan; API-dual-read alene gjør ikke eierskapsrollback trygg.

## 6. Tester som bør bevare/endre kontrakten

Eksisterende tester er kartleggingsgrunnlag, ikke kjørt bevis på migreringsstøtte.

| Område | Eksisterende ankere | Migreringsdekning som trengs |
| --- | --- | --- |
| API listing/workloads | `api/integration_tests/postgres_branch_operations.lua:7,27`; `postgres_workloads.lua:27,49,65`; `postgres_branch_selection.lua:24,49,96` | Gammel main + adoptert main gir samme PG/PBR-identitet, metadata og workloadbruk; mixed miljøer, ingen dubletter; empty og explicit main; non-main gir tydelig feil. |
| Helse/konfig/cache | `api/internal/persistence/postgres/configuration_test.go:80–106`; `models.go:499–528`; `integration_tests/postgres_branches.lua:78,208,239` | Ny status uten branch; statusoppdatering og informer add/update/delete; initial usynkronisert ny kilde; generationsgap; tilgjengelig/degradert/provisioning, nullable configuration og stabile parametere. |
| Tilgang | `api/integration_tests/create_postgres_access.lua:113,128,157,228,246,273,332,365,376`; admin/maintain-access-testene | Adoptert main uten branch gir access ved CNPG Ready; gammel access leses/gjenbrukes; ingen parallelle roller etter ny navneavledning; eier-only connection, TTL/revoke og TLS beholdes; false/unknown CNPG Ready er ikke AVAILABLE. |
| Branch-mutations | `api/integration_tests/postgres_branch_operations.lua:40–129`; `postgres_branch_delete.lua:6–27` | Gamle operasjoner validerer schema, men avviser uten writes etter cutover; main-delete kan aldri bli whole-delete; gammel klient kan ikke aktivere non-main. |
| Whole delete | `api/integration_tests/postgres_delete.lua:13–49`; `internal/persistence/postgres/delete_postgres.go:40–89` | Fresh-reference checks og finalizer virker på adoptert Postgres, UID-konflikt/failing list er fail-closed; asynkron acknowledgement beholdes. |
| Issues/gRPC/søk | `api/integration_tests/postgres_issues.lua:9,38`; `postgres_search.lua:8,40,60`; `api/internal/grpc/grpcdatabase/server_test.go` | Degraded Postgres gir én korrekt issue; gammel issue kan leses/ryddes; gRPC main blir fortsatt logisk navn + app + NAIS_POSTGRES, deterministisk paginering; søk inkluderer Postgres uten branch. |
| Console | `console/src/lib/domain/postgres/{branchesPage,overviewPage,PostgresTable,insightsPage,queryInsightsLoad,queryInsightsPage,branchOperation}.test.ts`; `src/lib/domain/issues/PostgresBranchIssue.test.ts:30–83` | SSR + klientsiden uten branchobjekt, additiv overgang, cache-id-stabilitet, polling cleanup, tydelige cutover-feil, gamle /branches-/querylenker, issue-presentasjon. |
| Observabilitet | `console/src/lib/domain/postgres/insights.test.ts:38–58`; `src/lib/utils/lokiLogQuery.test.ts:233–279` | Adopterte gamle podnavn og nye databaseidentiteter, main-filter, ingen nabodatabase/pooler, graf/query-stat-historikk før/etter labelendring uten dobbelttelling. |
| CLI | `cli/internal/postgres/{list,instance,access}_test.go`; `command/{get_readiness,get,environment,choices,access_completion,branch,inspection_commands,connection_env,proxy_output}_test.go` | Både gammel CLI mot overgangs-API og ny CLI mot gammel/adoptert database; main/default access, completion uten branch, stabile JSON/env-kontrakter og fjernet branch-write-UX. |

Kjør i implementeringsarbeidet: API `mise run generate:graphql` (og øvrig generering ved typet CR-/mocksendring), `mise run test` og sluttkontroll `mise run fmt ::: generate`; CLI oppdater schema, `mise run generate:graphql`, `mise run generate:docs`, test/check; Console Houdini generation/check, lint, Vitest og SSR-/klientregresjoner etter `console/AGENTS.md:618–626`. Bruk normale Go `testing`/cmp etter repoenes føringer.

## Metodenotat om Svelte

UI-avhengighetene er primært kartlagt via `.gql`, TypeScript og tester; bare fire routekomponenter ble lest begrenset for mutations, polling og log-prefix. Påkrevd svelte-file-editor-delegering ble forsøkt, men harness avviste nested subagent med depth limit 1. Derfor ble Svelte-skills lastet og official MCP `list-sections`, relevante `get-documentation` (routing/load/derived/lifecycle/stores) og autofixer brukt direkte, med prosjektets Svelte `5.57.3` (`console/package.json:56`). Layout fikk ingen funn; de øvrige fikk autofixer-funn om `$Postgres...` som stateful/non-store. Disse er ikke brukt som migreringsfunn: repoets dokumenterte Houdini-store-mønster krever nettopp `$store` (`console/AGENTS.md:191–196`). Ingen komponenter ble endret eller returnert som ny Svelte-kode.
