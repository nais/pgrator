# Plan: avvikle PostgresBranch uten å flytte databaser

Status: kompatibilitetsfasen er under utrulling. Ingen fysisk adopsjon er aktivert.
Oppdatert: 2026-11-12. **Les [gjenopptakingsnotatet](postgres-branch-transition-checkpoint.md) først** for gjeldende commits, utrullingsstatus og neste steg. Fremdriftslisten nedenfor dokumenterer det opprinnelige checkpointet; senere beslutninger i gjenopptakingsnotatet har forrang (blant annet er GraphQL `Postgres.clusterName` fjernet).

## Anbefaling

Bruk en **expand → adopt → contract**-overgang:

1. Publiser additive typer og en API-versjon som leser både gammel og ny modell. Rull deretter ut branchløse klienter og en naiserator som ikke lenger skriver branch-valg.
2. Rull ut én migrasjonsbevisst pgrator-versjon som flytter direkte eierskap til eksisterende `Postgres`, uten å endre fysiske identiteter eller connection-kontrakten.
3. Slett branch-objekter først etter verifisert adopsjon. Fjern kompatibilitetskode og CRD i en separat opprydding.

**Ikke gi eksisterende CNPG-clustere nye navn. Ikke gjenopprett dem. Ikke krev redeploy av applikasjoner.** Behold gjerne dagens deterministiske fysiske navnealgoritme også for nye databaser i denne overgangen. Et historisk `main` i et internt ressursnavn er langt billigere enn to navnesystemer eller endpoint-migrasjon.

`postgres.nais.io` forstås her som pgrators eksisterende Postgres-reconciler/eierskap. Kubernetes-ressursen er fortsatt **`nais.io/v1`, kind `Postgres`**; dette forslaget endrer ikke API-gruppen. Det eldre `data.nais.io/v1`-opplegget er utenfor overgangen.

## Lokal fremdrift

- Additive `Postgres.status.clusterName` og `configuration`, felles konfigurasjonstype, genererte CRD-er/DeepCopy og en helper som bevarer den eksisterende main-identiteten er lagt til i pgrator. Ingen controller publiserer de nye feltene eller adopterer ressurser ennå.
- API har direkte `state`, `configuration`, `createdAt`, `clusterName`, `earliestRestoreTime` og `issues`, samt direkte Postgres-relasjoner på workload/access. Kildevalg skjer via **det lagrede statusfeltet** `clusterName`, med virtuell main for gammel GraphQL-lesing, workloads og gamle access-referanser. GraphQL clusterName kan også avledes for gammel modell; dette beregnede svaret aktiverer ikke ny eierskapsmodell.
- Inventory, gRPC og issue-checker bruker samme sammenslåtte branch-view. Issue-identiteten beholdes midlertidig som gammel main-identitet, slik at lagrede issues fortsatt kan løses gjennom fasaden uten en samtidig databasemigrering.
- Dynamic-client-fabrikker for vanlige Postgres-/access-/credential-/backup-/delete-operasjoner er flyttet til Postgres-watcheren. Selve branch-operasjonene og branch-watcherens levetid er foreløpig beholdt; brukerens forestående sperre for create/restore/activate håndteres ikke på nytt her.
- Under utviklingen ble API koblet til lokalt `pgrator/pkg/api` gjennom en midlertidig ekstern Go-workspace (`postgres-branch-transition.go.work` i OpenCodes tempmappe). Før API-branch pushes, pins dependency til den pushede pgrator-committen og generering/tester kjøres med `GOWORK=off`. Ingen lokale `replace`-stier skal inn i `go.mod`.
- Access-schema krever nøyaktig én immutable referanse: `postgres` eller deprecated `postgresBranch`. API-input kan utelate branch (default main), og ny API skriver fortsatt gammel K8s-referanse før ny-modell-markøren er observert. Eksisterende grant gjenbrukes med samme deterministiske navn ved begge former. Første expiry-svar er normalisert til samme sekundpresisjon som den lagrede referansen, så gjenbruk ikke gir et tilsynelatende endret utløpstidspunkt.
- Access-controlleren resolver begge former, inklusive legacy main etter detach, og krever konsistent fysisk navn og Postgres-controller-UID/kind/API-versjon i direkte modell. Nye relasjonswatches følger både Postgres- og legacy-referanser. Rolle-/relay-/nettverksbuildere bruker samme fysiske identitet, og expiry/revoke krever ikke at databasen eller branchen fortsatt finnes.
- CLI er flyttet til direkte Postgres-state for list/get og branchløse access-requests uten active-branch-oppslag. Create/restore/activate/delete for branch er ikke lenger tilgjengelig, og de tilhørende mutations er fjernet fra CLI-koden/generert klient. Read-only `branch list`, legacy JSON main-felter og `--branch main` beholdes som lokale kompatibilitetsaliaser; non-main-flagget avvises. Historiske branch-activity-typer beholdes. Arbeidsbranchen heter `feat/postgres-without-branches` i alle seks endrede repoer.
- GraphQL access-inputs branch-felt er nå nullable/optional, med main-default i servervalideringen. CLI sin eksisterende schema-eksport mister input-defaults; reell optionalitet unngår at nye klienter blir tvunget til å sende feltet. Lokal schema-fetch er gjort mot en isolert introspection-server uten database/Kubernetes-kobling, ikke en kjørende produksjons-API.
- Liberator markerer `uses.postgres.branch` deprecated og utelater feltet fra anbefalte Application/Naisjob-eksempler, men beholder optional schemafelt for eksisterende manifests. Ordinær generering oppdaterer også tidligere utdaterte controller-gen-versjonsannotasjoner fra 0.22 til 0.23; dette endrer ikke valideringen av øvrige ressurser.
- Naiserator utelater alltid binding.spec.branch, aksepterer gammelt `main` og avviser andre branchverdier eksplisitt. Tester sammenligner hele AST-en for utelatt/main på både Application og Naisjob; bindings, env, credentials og mounts er identiske.
- En metadata-only `Adopt`-action er lagt til som fundament for den senere fysiske adopsjonen, men er ikke koblet til noen controller. Den krever verifiserte ressurs-/eier-UID-er, namespace og controller-type, re-leser eiere, flytter controller-reference og begge eierannotasjoner i én optimistic-lock-patch, bevarer øvrige data/finalizers/referanser og tåler gjentakelse. Den verken oppretter eller sletter ressurser, og endrer ikke spec. Tidligere controller må stoppes før den tas i bruk; actionen er ikke en controller-lås.
- Console er flyttet til direkte Postgres-state/konfigurasjon/workload-relasjoner og logisk database-inventory. Branches-tab, branchvelger og activate/delete-mutations er fjernet. Gamle `/branches`-main-lenker redirecter til oversikten; non-main/tomme/motstridende branch-parametre avvises i stedet for å åpne main. Historiske branch-issues/aktiviteter beholdes. Insights/Queries beholder metrics-filteret `branch="main"`, mens logger bruker faktisk clusterName og en escaped, forankret `<clusterName>-<serial>`-regex som utelukker poolere og nabodatabaser. Ukjent clusteridentitet gir ikke et bredt loggsøk.
- Console-schema er hentet med normal Houdini pull-schema fra den isolerte lokale introspection-serveren og Houdini regenerert, uten håndredigering av schema. Den midlertidige kildefilen og serverprosessen er ryddet etter schemahenting. Ingen database/Kubernetes-kobling ble startet for dette.
- Gjenstår: fysisk adopsjon/checkpoint og trygg detach/deletion, inklusive binding-resolver uten branch. API-/klientkompatibiliteten er ikke en autorisasjon til å slette branches ennå.

### Validering av første lokale del

- Pgrator: `mise run generate`, `mise run fmt`, `mise run test` (begge Go-moduler) og `mise run check` fullført. Access-testene dekker begge referanseformer før/etter detach, identiske rolle-/relay-/nettverksressurser, bevart password/token, avvisning av ugyldig/manglende/fremmed target, relasjonswatches, revoke uten database og actual envtest-schema for XOR og reference-immutabilitet.
- API med lokal workspace: `mise run fmt ::: generate` og `mise run test` fullført, inklusive integrasjonstester og race detector. Nye tester dekker direkte/gammel GraphQL-status, virtuelle main-node-ID-er og workloads uten branch-CR, kildevalg/deduplisering, gamle access-referanser etter detach og fresh CNPG-readiness før access.
- API staticcheck fullført. Den samlede API-sjekken er ikke grønn: gosec rapporterer G115 i urørt `internal/vulnerability/queries.go:105,130`, vulncheck rapporterer dependency-sårbarheter, og GraphQL-lint rapporterer brede schemafunn (220 feil). Disse er ikke ryddet som del av Postgres-overgangen.
- CLI: lokal schema-fetch, GraphQL-/doc-generering, format, full testsuite, build og `mise run check` fullført. Nye tester bekrefter at access-requesten utelater branch på wire, branch-skrivekommandoer avvises uten API-kall, direkte state overstyrer stale branchmetadata, og JSON main-aliaser og navnecompletion beholdes.
- Liberator: generering, format, full testsuite, staticcheck og vet fullført. Naiserator: format, full testsuite med race detector/envtest, build, staticcheck og vet fullført. Ingen deploy-/push-tasks er kjørt.
- Pgrators adopsjons-action er testet for metadata-only/idempotens, fremmede/manglende/byttede UID-er og eiere, deletion, namespace-skillet og concurrent-update-konflikt uten delvis overføring, samt en faktisk API-server-roundtrip i envtest. Hele pgrator-testsuiten og sjekkene består også etter denne tilleggsendringen. Dette beviser ikke CNPG-/GC-/applikasjonskontinuitet; fysisk adopsjon er fortsatt ikke aktivert.
- Naiserator-testsuiten er i tillegg kjørt mot lokal liberator gjennom `postgres-clients.go.work` i samme tempmappe. Også denne kombinasjonen består, uten publisering eller endringer i repoenes dependency-versjoner.
- Console: SvelteKit sync, check (0 feil/advarsler, 243 Houdini-dokumenter), lint, 94 testfiler / 1235 tester, build, Helm strict-lint og diff-check fullført. Nye tester dekker direkte-status-polling med visibility/fetch/cleanup, gamle lenker og clusteravgrensede logger. Ingen browser/live-API-E2E er kjørt.
- Dette er lokal kode-/API-validering, ikke en upgrade-E2E med en kjørende CNPG-database og applikasjon. Den testen gjenstår sammen med adopsjonen.

## Premisser og avgrensning

- Checkpointet pushes kun til feature-branches. Workflowene for faktisk utrulling er avgrenset til `main`/`master`. Feature-CI kan kjøre bygg/tester og publisere artefakter; ingen workflow-dispatch, PR-opprettelse, merge eller utrulling er del av dette checkpointet.

- Brukerens opplysning om kun aktiv `main` legges til grunn. Inventar må likevel bekrefte at det ikke finnes andre branch-objekter, pågående recoveries eller referanser som trenger manuell behandling. Avvik skal stoppe migrasjonen for den databasen, ikke utløse en generell flerbranch-migrator.
- Console har stengt branch-opprettelse. CLI er ikke stengt ennå. API bør derfor stenge branch-opprettelse/-aktivering server-side som del av første utrulling; gamle CLI-er skal få en forståelig feil.
- PITR gjennom opprettelse og aktivering av branch avvikles. Et nytt branchløst restore-design er en egen oppgave; eksisterende backups og WAL skal fortsatt være brukbare.
- Ingen kode- eller produksjonsendringer er utført som del av kartleggingen.
- Kildene er lokale arbeidskopier, ikke en bekreftelse på hvilke versjoner som kjører i plattformen. Release-digester, faktisk CRD-schema og cluster-inventar må registreres før utrulling.

## Viktige funn i pgrator

| Funn | Konsekvens | Kilde |
| --- | --- | --- |
| Postgres lager og eier main-branchen; den fysiske reconciler-logikken ligger i PostgresBranch. | Flytt fysisk reconcile/status/deletion til Postgres, ikke bare clusterets owner reference. | [postgres_controller.go:118–223](../internal/controller/postgres_controller.go#L118), [postgresbranch_controller.go:90–155,554–675](../internal/controller/postgresbranch_controller.go#L90) |
| Branch-objektnavnet er trunkert navn pluss hash av Postgres og lokal branch; CNPG-navnet legges oppå dette. | `pg-<postgres>` er ikke samme navn som dagens main-cluster. Ikke pars navn ved å fjerne `-main`. | [postgresbranch_names.go:14–28](../pkg/api/v1/postgresbranch_names.go#L14), [cnpg_names.go:15–28](../pkg/api/v1/cnpg_names.go#L15) |
| Pooler, Database, rolleobjekter, personlig autentiseringsgruppe, bindingroller, GSA og bucket avledes fra den fysiske identiteten. Bucket bruker dessuten Postgres-UID. | Alle avledninger må få samme identitet før og etter migrasjon, uavhengig av ny logisk eier. | [cnpg.go:112–128,205–209,545–546](../internal/resourcecreator/cnpg/cnpg.go#L112), [binding.go:42–55,82–96](../internal/resourcecreator/binding/binding.go#L42), [postgresbranch_controller.go:725–847](../internal/controller/postgresbranch_controller.go#L725) |
| Synchronizer oppdager urefererte ressurser via `<reconciler>/owner`; `Unclaim` sletter når den aktuelle annotasjonens siste eier fjernes. | Å legge til `postgres.nais.io/owner` beskytter ikke mot gammel branch-opprydding. Migrasjon må håndtere begge annotasjonsnøkler og stoppe gammel branch-writer. | [synchronizer.go:460–541](../internal/synchronizer/synchronizer.go#L460), [unclaim.go:40–62](../internal/synchronizer/action/unclaim.go#L40) |
| Vanlig `Claim` nekter å erstatte en annen controller-UID. | Adopsjon trenger en eksplisitt, verifisert metadata-operasjon; ikke bruk blind `SetOwnerReferences(nil)` eller vanlig Claim som migrator. | [claim.go:46–59](../internal/synchronizer/action/claim.go#L46) |
| Manglende/deleting branch gir `Unavailable`; bindingens Secret og nettverk blir ikke beholdt som actions og kan prunes. | Branch-objekter må være tilgjengelige til ny binding-resolver er i drift og testet. | [postgresbinding_controller.go:267–303,526–544](../internal/controller/postgresbinding_controller.go#L267), [synchronizer.go:480–541](../internal/synchronizer/synchronizer.go#L480) |
| PostgreSQL-konfigurasjon rapporteres på branch-status. | Flytt observert konfigurasjon og fysisk readiness til Postgres-status, ellers mister Console/API disse dataene. | [postgresbranch_types.go:50–67](../pkg/api/v1/postgresbranch_types.go#L50), [postgresbranch_controller.go:421–440](../internal/controller/postgresbranch_controller.go#L421) |
| Personlig tilgang krever branch-oppslag, og `spec.postgresBranch` er immutable. Maksimal levetid er én time. | Støtt eksisterende access-objekter til de utløper; ikke skriv om immutable spec eller roter passord/token under adopsjon. | [postgresaccess_types.go:23–42](../pkg/api/v1/postgresaccess_types.go#L23), [postgresaccess_controller.go:53,185–286](../internal/controller/postgresaccess_controller.go#L185) |
| Main/last-branch-sletting er blokkert; vanlig branch-deletion rydder fysiske barn. | Ikke slett main med dagens deletion-path. Migrert branch trenger en egen detach/finalization-path som aldri unclaimer adopterte barn. | [postgresbranch_controller.go:1141–1176](../internal/controller/postgresbranch_controller.go#L1141) |
| WAL-bucket har `cnrm.cloud.google.com/force-destroy: true`. | Feilaktig sletting kan også ødelegge backup-arkivet, ikke bare K8s-objektet. | [storagebucket.go:22–38](../internal/resourcecreator/storage/storagebucket.go#L22) |
| Branch-CRD har Helm `resource-policy: keep`. | Å fjerne templaten er ikke tilstrekkelig til å avvikle CRD-en; eksplisitt opprydding må planlegges. | [nais.io_postgresbranches.yaml:1–18](../charts/pgrator/templates/crd/nais.io_postgresbranches.yaml#L1) |

## Målmodell og minste nødvendige kompatibilitet

### Logisk Postgres og stabil fysisk identitet

- Eksisterende `Postgres` beholder navn, namespace og UID og blir controller for alle **direkte branch-eide** ressurser.
- Samle fysisk navngiving i én helper basert på dagens deterministiske main-identitet. Ta bort branch-begrepet fra dens offentlige grensesnitt, men behold algoritmens output. Skill denne identiteten fra det logiske Postgres-navnet i resource creators; unngå å bruke et falskt Postgres-objekt med fysisk navn som modell.
- Legg til `Postgres.status.configuration` og en tydelig observert cluster-referanse, eksempelvis `status.clusterName`. Bruk en liten, durabel migreringsmarkør med cluster-UID/checkpoint for å skille «ikke migrert» fra «adoptert», og for å hindre gjenoppretting av et bortkommet migreringsobjekt.
- `spec.activeBranch`, `status.activeBranch` og `PostgresBinding.spec.branch` beholdes midlertidig som deprecated kompatibilitetsfelter. I ny modell aksepteres utelatt/`main`; andre verdier avvises eksplisitt. Ikke fall tilbake til en annen database ved ugyldig referanse.
- Status skal rapportere fysisk readiness og konfigurasjon fra det samme clusteret også mens øvrige ressurser adopteres. Ikke presenter gammel «branch finnes» som ny «databasen er Ready».

### Personlig tilgang

- Utvid `PostgresAccessSpec` med `postgres` og gjør `postgresBranch` optional/deprecated. Valider at nøyaktig én referanse er satt ved ny opprettelse. Behold immutabilitet og test at eksisterende objekter kan oppdateres/finaliseres med det additive schemaet.
- Ny pgrator støtter både gammel main-referanse og ny Postgres-referanse. Gammel referanse identifiseres ved å beregne forventet main-objektnavn for den aktuelle Postgresen, ikke ved usikker string-parsing.
- La resolver levere fysisk identitet/cluster til rolle-, HBA-, nettverks- og RelayAccess-buildere. Samme bruker skal få samme rolleobjektnavn, autentiseringsgruppe og target etter migrasjon.
- Behold access-UID, credential-Secret, relay-token og eksisterende RelayAccess-spec uendret. Pgrator avviser en eksisterende RelayAccess med endret immutable spec; dette er en konkret grunn til ikke å endre target under migrasjon ([postgresaccess_controller.go:331–362](../internal/controller/postgresaccess_controller.go#L331)).
- API skriver gammel access-referanse mot gammel pgrator og ny referanse først når databasen har et verifisert nytt-model-checkpoint. API skal ikke dobbeltskrive samme tilgang som to CR-er.

### Eierskapsgrense

Postgres adopterer Cluster, de branch-eide DatabaseRole-objektene, Database, Pooler, NetworkPolicy, ScheduledBackup, ObjectStore, FQDNNetworkPolicy, IAMServiceAccount, IAMPolicyMember og StorageBucket når disse finnes. Verifiser øvrige direkte owner references ved inventar, ikke bare denne listen.

**Ikke flytt CNPGs barn direkte til Postgres.** Pods, PVC-er, Services, CA-/client-cert-Secrets og historiske cluster-eide Backups skal fortsatt ha CNPG-eiere. ScheduledBackup lager allerede cluster-eide Backups ([cnpg.go:432–459](../internal/resourcecreator/cnpg/cnpg.go#L432)). Binding-eide roller/connection-Secrets/nettverk og access-eide ressurser beholder også sine eiere. De er transitivt knyttet til databasen, men er ikke branch-eide ressurser som skal adopteres.

## Endringer i omkringliggende komponenter

### liberator

- Behold `uses.postgres[].branch` optional i kompatibilitetsvinduet, men marker det deprecated og fjern det fra anbefalte eksempler. Ny semantikk: utelatt og `main` er samme database.
- Ingen ny fysisk cluster-referanse i Application/Naisjob. `uses.postgres[].name`, role og envPrefix er fortsatt kontrakten.
- Vent med å fjerne schemafeltet til gamle manifests og writers er borte. Valider/avvis andre verdier på en kontrollert måte; inventar må gjøres før valideringen strammes inn.
- Oppdater schema-/eksempeltester; dagens branch-test godtar blant annet `pr-123`.
- Kilder: [naiserator_types.go:95–136](../../liberator/pkg/apis/nais.io/v1/naiserator_types.go#L95), [postgres_branch_test.go:16–57](../../liberator/pkg/crd/postgres_branch_test.go#L16), [application_doc_example.go:473](../../liberator/pkg/apis/nais.io/v1alpha1/application_doc_example.go#L473), [naisjob_doc_example.go:372](../../liberator/pkg/apis/nais.io/v1/naisjob_doc_example.go#L372).

### naiserator

- Slutt å sende `spec.branch` på nye/oppdaterte PostgresBindings. Tillat `uses.postgres.branch: main` som deprecated input; andre verdier skal ikke ignoreres som om de betydde main.
- Dette kan rulles ut **før pgrator endres**: gammel pgrator velger allerede observert active branch når branch er utelatt. Under premisset om kun main er dette samme target.
- Behold bindingnavn, secretName, credential-sett, miljøvariabler og mount paths. Ingen discovery av cluster i naiserator og ingen applikasjons-redeploy som migreringsmekanisme.
- Verifiser med golden-tester for både Application og Naisjob at eneste relevante endring er at `spec.branch` utelates. Test ingen branch, eksplisitt main, roles, flere databaser, JDBC og lange navn.
- Kilder: [postgresbinding.go:81–111,123–165](../../naiserator/pkg/resourcecreator/postgresbinding/postgresbinding.go#L81), [binding-resolver:180–185](../internal/controller/postgresbinding_controller.go#L180), [branch_test.go](../../naiserator/pkg/resourcecreator/postgresbinding/branch_test.go), [postgresbinding.yaml](../../naiserator/pkg/resourcecreator/testdata/postgresbinding.yaml), [naisjob/postgresbinding.yaml](../../naiserator/pkg/resourcecreator/testdata/naisjob/postgresbinding.yaml).

### relay

- Ingen branch-spesifikk kodeendring er nødvendig dersom CNPG RW-service, `pg-`-prefiks, service-labels/selectors, port og RelayAccess-kontrakt beholdes.
- Relay kjenner RelayAccess og CNPG Services, ikke PostgresBranch. Operatoren sjekker `pg-…-rw`, port 5432 og CNPG-primary-selector. Ikke endre disse som kosmetisk del av oppryddingen.
- Test eksisterende tunnel over adopsjon samt ny tilgang etter adopsjon. Bekreft uendret SNI/CA, token, mapping og egress policy.
- Kilder: [reconcile.go:49–59,149–153,291–309,395–414](../../relay/internal/operator/reconcile.go#L291), [pgrators relay-builder:65–80](../internal/resourcecreator/access/relay.go#L65).

### API, Console og CLI

**Versjonene undersøkt før kompatibilitetsendringene støtter ikke branchløs drift.** Helse og konfigurasjon ligger der på GraphQL PostgresBranch; gammel CLI finner active branch før vanlig psql/proxy. Bare å returnere `activeBranch: null` ville derfor bryte normal tilgang. Detaljer og testankere finnes i [klientnotatet](postgres-branch-client-research.md).

#### API: en kompatibilitetsrelease før adopsjon

1. Lag én intern Postgres-lesemodell per `(environment, namespace, name)`. Les metadata fra Postgres og status fra gammel main frem til ny pgrator har publisert en eksplisitt fysisk/adopsjonsmarkør. Deretter er Postgres-status autoritativ, **også når den er degraded/progressing**. Ikke fall tilbake til en stale frisk branch bare fordi ny status er dårlig. Håndter informer-sync; forbidden/timeouts skal ikke bli «ikke funnet».
2. Legg til direkte GraphQL `Postgres.state`, `configuration`, `createdAt`, backup-/restore-vindu der dette fortsatt vises, `issues` og en eksplisitt fysisk cluster/log-referanse. Bruk samme statussemantikk som før (observed generation, reconcile phase og CNPG conditions), og behold CNPG Ready-sjekken før personlig tilgang utstedes.
3. Behold gamle `activeBranch`, `branch(name: main)`, `branches` og non-null workload-/access-relasjoner gjennom en **virtuell main-fasade**. Den er bare et API-view; ikke lag nye Kubernetes-branchobjekter eller en ny controller for kompatibiliteten. Behold gamle `PG`- og `PBR`-ID-er og `__typename` på de respektive nodene. Ikke oversett non-main til main.
4. Gjør access-input branchløst/additivt, eksempelvis ved å gjøre eksisterende branch-input optional/deprecated med main-default, samtidig som Postgres fortsatt velges eksplisitt. Gamle requests skal fortsatt virke. Velg gammel/ny K8s access-spec etter verifisert pgrator-kapasitet per database. **Behold dagens deterministiske access-navn basert på den samme fysiske main-identiteten** i begge skrivebaner, så en bruker ikke får to konkurrerende access-/rolleobjekter. Gjenbruk eksisterende tilgang før immutable revoke/recreate.
5. Steng `createPostgresBranch`, `activatePostgresBranch` og `deletePostgresBranch` server-side med forståelig feil og uten Kubernetes-write. Behold schema/resolvers for gamle klientdokumenter i vinduet, inklusive autorisasjon. Branch-delete må aldri bli whole-Postgres-delete. Whole-Postgres-delete beholder fresh reference guards, UID-precondition, eksisterende finalizer-navn og asynkron acknowledgement.
6. Flytt **gRPC database-listing**, issue-checker/resolver og inventory fra ren branch-watcher til den samme lesemodellen. Bevar logisk main-navn, `app`, `NAIS_POSTGRES` og stabil paginering. Migrer/rydd eksisterende lagrede branch-issues kontrollert. Behold historiske branch-activity-eventtyper; disse er historikk, ikke aktive Kubernetes-ressurser.
7. Flytt dynamic-client-fabrikker bort fra `postgresBranchWatcher`: dagens kode bruker denne også for PostgresAccess, CNPG og deletion. Først deretter kan branch-watcher og tilhørende RBAC tas bort. Test restart uten branch-CRD. Oppgrader den importerte Go-typen før typed write-roundtrip kan skrive objekter med nye felter.

Kilder: [postgres.graphqls:184–249,686–730,831–857](../../api/internal/graph/schema/postgres.graphqls#L184), [queries.go:468–508,571–737](../../api/internal/persistence/postgres/queries.go#L468), [models.go:435–528](../../api/internal/persistence/postgres/models.go#L435), [grpcdatabase/server.go:16–39,80–91](../../api/internal/grpc/grpcdatabase/server.go#L16), [issue/checker/postgres.go](../../api/internal/issue/checker/postgres.go), [delete_postgres.go](../../api/internal/persistence/postgres/delete_postgres.go).

#### Console: ingen branch-UI, samme databaseinformasjon

- Bytt teamliste, oversikt, workload/persistence-visning og personlig tilgang til direkte Postgres-kontrakt. Behold progression-/feilvisning og polling, ikke bare fjern branch-queryene.
- Fjern Branches-tab og gjenværende activate/delete-mutations. Create er allerede fjernet; det betyr ikke at branch-skriving er helt borte.
- Behold redirect fra gamle `/branches`-lenker, normaliser `?branch=main` og gi tydelig beskjed for non-main-lenker. Historiske issues/activities må fortsatt kunne presenteres.
- Oppdater Insights og Queries samt admin-inventory. PromQL bruker `branch` som selector; behold `branch="main"` i produsert observabilitet gjennom overgangen, eller rull ut kompatible selectors **før** den eventuelt fjernes. Kilden til metric-labels utenfor disse repoene må bekreftes i faktisk cluster-konfigurasjon.
- Logger rekonstruerer i dag pod-prefix fra Postgres + branch. Bruk den eksplisitte cluster/log-referansen på nytt API, med test for eksisterende hashed/trunkerte main-navn. Ikke bytt podnavn for å passe ny UI.
- API-schema distribueres først; regenerer Houdini. Gamle åpne faner sender fortsatt gamle GraphQL-dokumenter, så backend-feltene skal ikke fjernes ved Console-deploy.

Kilder: [teamliste/query.gql](../../console-frontend/src/routes/team/[team]/postgres/query.gql), [Postgres-overview/query.gql](../../console-frontend/src/routes/team/[team]/[env]/postgres/[postgres]/(single)/query.gql), [branchesPage.test.ts:65–106](../../console-frontend/src/lib/domain/postgres/branchesPage.test.ts#L65), [insights.ts:30–40](../../console-frontend/src/lib/domain/postgres/insights.ts#L30), [queryInsights.ts:20–29](../../console-frontend/src/lib/domain/postgres/queryInsights.ts#L20), [lokiLogQuery.ts:61–96](../../console-frontend/src/lib/utils/lokiLogQuery.ts#L61).

#### CLI: branchløs normalbruk, gammel binær fortsatt brukbar

- Ny list/get leser direkte Postgres-status. Behold branch-uavhengig navnecompletion. Avklar/stabiliser JSON-output for scripts; ikke fjern gamle outputfelter samtidig med backend-adopsjon uten en avtalt CLI-kontraktsendring.
- Ny `psql`/`proxy` trenger ikke `GetActivePostgresBranch`; de bruker direkte Postgres og den additive access-operasjonen. `--branch main` kan beholdes som deprecated alias en periode, mens andre verdier feiler tydelig.
- Fjern/deprecate branch create/activate/delete og tilhørende completion/help. Gamle binærer beskyttes av API-sperren; vanlig list/get/psql/proxy beskyttes av main-fasaden.
- Behold access-polling, 60-sekunders setup-timeout, owner-only credentials og TLS/relay/JSON/env-kontrakten. Oppdater schema og genqlient etter API-release.

Kilder: [postgres/get.go:11–22](../../cli/internal/postgres/get.go#L11), [command/postgres.go:14–30](../../cli/internal/postgres/command/postgres.go#L14), [command/branch.go](../../cli/internal/postgres/command/branch.go), [command/access.go:309–318](../../cli/internal/postgres/command/access.go#L309), [access.go:43–149](../../cli/internal/postgres/access.go#L43).

Dette krever ikke et omfattende parallelt plattformopplegg: én midlertidig API-fasade, én fysisk navnealgoritme, samme Kubernetes-Postgres og samme binding-/relay-kontrakt. Det tidsbegrensede dobbeltopplegget er i **lese- og referansekompatibilitet**, ikke to aktive databaser eller to fysiske controllers.

## Selve adopsjonen i pgrator

Implementer en smal, idempotent overgang for eksisterende main, ikke et generelt migreringsrammeverk.

1. **Preflight per Postgres.** Verifiser samme namespace, Postgres-UID, forventet main-identitet, branchens eier-UID, clusterets eier-UID og ingen deletionTimestamp. Finn alle direkte branch-eide barn og lag et inventar med GVK/navn/UID, relevante specs, annotations og referanser. Avvis ukjent/fremmed eier. En allerede adoptert ressurs med samme Postgres-UID er OK ved retry. En eldre ressurs fra en slettet/reopprettet Postgres er ikke OK.
2. **Ingen konkurrerende writers.** Migrasjonsversjonen må ta over den fysiske reconcile-pathen og deaktivere gammel fysisk branch-reconcile for databaser den adopterer, inklusive prune/deletion. Enkelst å la branch-controlleren bare utføre trygg kompatibilitetsfinalisering i denne versjonen. Ikke bare returner en tom action-liste fra gammel controller: synchronizer kan fortsatt kjøre DetectUnreferenced. Gamle pgrator-prosesser må være ute av writer-rollen før adopsjon starter.
3. **Metadata-only adopsjon gjennom action-systemet.** Lag en eksplisitt adoption action som re-leser objektet, verifiserer forventet ressurs-UID og gammel/ny controller-UID og patcher med resourceVersion/optimistic lock. Erstatt branch-controller-reference med Postgres-reference i én patch per objekt; bevar andre legitime owner references. Legg til `postgres.nais.io/owner` og fjern akkurat den gamle branch-oppføringen i `postgresbranch.nais.io/owner` i samme patch. Ikke bruk Unclaim for overføringen.
4. **Ingen spec-endringer i adoption-pass.** Bevar Cluster.spec, Pooler.spec, DatabaseRole.spec, IAM-identitet, StorageBucket.spec.resourceID, ObjectStore destination og CNPG capability-annotations. Behold også branch-labels i Cluster.spec.inheritedMetadata under overgangen; kosmetisk fjerning er ikke verdt risiko for pod-endringer. Bevar eksisterende serverName/archive-path og permissions.
5. **Trygg resumering.** Checkpoint fysisk cluster-UID og migreringsstatus durabelt før en senere reconcile kan behandle et manglende cluster som nyopprettelse. Hvis cluster/bucket/rolleobjekt som inngår i en eksisterende migrering forsvinner eller byttes UID, stopp i stedet for å opprette en tom erstatning. Test avbrudd etter hver action. Synchronizer utfører actions sekvensielt og er ikke en transaksjon ([synchronizer.go:347–358](../internal/synchronizer/synchronizer.go#L347)).
6. **Ny normal reconcile.** Etter fullført adopsjon bygger Postgres de samme fysiske ressursene med samme navn og funksjonelle spec. Ny resolver brukes i bindingens Prepare, snapshot og certificateRoleName, source-secret-watches og cluster-event-mapping; det holder ikke å bytte ett branch-oppslag. Eksisterende access-resolver må virke uten branch-CR.
7. **Markér fullført først etter kontroll.** Alle direkte barn har riktig eier og annotasjoner; alle bindings har samme target/gyldige snapshots; status peker på riktig cluster; ingen gjenværende branch-referanser på barn. Behold main-CR til kompatibilitets- og rollbackvinduet er ferdig.
8. **Separat sikker branch-opprydding.** Verifiser på nytt at branch ikke lenger eier noen ressurser eller er nødvendig for noen reader. Fjern branch-finalizer via en eksplisitt trygg path og slett branch-objektet med UID-precondition. Den gamle «main er aktiv»/Unclaim-pathen skal ikke kjøres. Ikke slett Postgres for å komme rundt slettesperren.

Postgres-deletion må samtidig få den gamle fysiske oppryddingsplikten, inklusive asynkrone CNPG-/Config Connector-finalizers. Dette må testes som egen kontrakt, ikke overlates til «adopsjonen gikk bra».

## Utrullingsrekkefølge og porter

| Fase | Leveranse / handling | Port før neste fase |
| --- | --- | --- |
| 0. Inventar | Bekreft main-only, eierskap, CRD-schema, referanser, backupstatus og kjørende release-digester. Ta metadata/spec-inventar og sikker baseline. | Ingen ukjente eiere, ingen annen branch eller recovery under behandling. Uavklarte databaser holdes utenfor. |
| 1. Expand | Publiser `pgrator/pkg/api` med additive status/access-felter; installer additive CRD-schemas separat før feltene skrives. Rull API med dual-read/legacy-kontrakt og server-side branch-sperre. | API virker mot dagens pgrator, og gammel Console/CLI virker for vanlig databasebruk. Nytt schema round-tripper gamle objekter uten å prunes. |
| 2. Klienter | Rull branchløs Console og CLI mot kompatibilitets-API. Rull liberator/naiserator-endringene som utelater branch på bindings. | Nye klienter fungerer mot gammel pgrator via API. Eksisterende og nye Application/Naisjob-bindings virker med utelatt/main. |
| 3. Adopsjon | Rull migrasjons-pgrator, først testcluster og deretter minste egnede canary-cluster. En enkel eksisterende utrullingsmekanisme/opt-in kan brukes for mindre grupper hvis dette ikke krever to konkurrerende fysiske controllers. | Uendret Cluster/Pooler/PVC-UID, connection-data og backupidentitet. Kontinuerlig SQL-test og ny forbindelse fungerer. Ingen migrasjonsutløst DB-/app-rollout, prune eller IAM-recreate. |
| 4. Utvid | Migrer resten i små grupper, ett miljø/cluster om gangen. Test oppstart/restart og et representativt utvalg av applikasjoner. | Alle Postgreser rapporterer verifisert adopsjon; bindings, status, personlig tilgang, backups og klienter er friske. |
| 5. Detach | Når rollbackvinduet er avsluttet: slett detacherte branch-CR-er trygt. Eksisterende legacy accesses støttes til de har utløpt/finalisert. | Ingen barn har PostgresBranch owner reference eller gammel eierannotasjon. Ingen aktiv reader krever branch-CR. |
| 6. Contract | Fjern API-informers/listers for branch før CRD fjernes. Fjern gammel pgrator-controller, legacyfelter etter kontraktvindu, RBAC/CRD-template og eksplisitt CRD-installasjon. Rydd eksempler/docs/dashboards. | Test restart av alle komponenter uten branch-CRD. Ingen gammel writer, ingen legacy access under finalisering, ingen gamle manifests som krever branch. |

Additive CRD-schemaendringer kan leveres uten å aktivere ny pgrator-runtime. Hvis separat schema-utrulling er vanskelig i release-løpet, behold den gamle access-write-shapen til migrasjonsreleasen har installert nytt schema; ikke bygg et nytt utrullingssystem bare for dette. Console/CLI og API sin dual-read kan fortsatt komme først.

## Rollback

- **Før adopsjon:** vanlig rollback av API/klienter/naiserator. Ikke rull schema tilbake destruktivt; additive felter kan stå.
- **Etter første owner-transfer:** en blind rollback til gammel pgrator er **ikke trygg**. Gammel branch-controller kan ta tilbake eierskap; gammel Postgres-controller kan gjenskape main, og annotasjonsdrevet pruning kan slette feil ressurser. Leader election gjør ikke forskjellige datamodeller kompatible.
- Foretrukket respons på feil er å pause videre adopsjon og rette fremover med migrasjonsversjonen. Stans om nødvendig pgrator-writer midlertidig; CNPG og eksisterende databaser skal fortsatt kjøre. Verifiser separat at CNPG, Relay og binding-materiale fungerer uten pgrator-writer i dette korte vinduet.
- Hvis gammel pgrator må gjeninnføres: stans writers, behold/opprett verifisert main-CR, reverser owner references og begge annotasjoner gjennom samme UID-sikre metadata-prosess og verifiser connection/backup-identiteter før gammel writer startes. Et gjenskapt branch-objekt får **ny UID**, som må brukes i alle referanser; ikke replay gamle YAML-owner-UID-er.
- Ikke bygg automatisk reversering med mindre staging-testene viser at den trengs. Dokumenter og øv manuell, inventory-basert reversering før prod. Branch-objekter beholdes i observasjonsvinduet nettopp for å gjøre dette enklere.
- Etter contract-fasen er rollback en kontrollert gjeninnføring av schema og modell, ikke vanlig image-rollback.

## Påkrevde tester og observasjon

### Før utrulling

1. **Upgrade-E2E:** start gammel pgrator, opprett Postgres, binding og SQL-data; hold en eksisterende session og kontinuerlige korte skrive-/lesetransaksjoner i gang. Oppgrader, adopter, slett branch trygt og start ny pgrator på nytt. Bekreft samme data, samme SQL-rolleidentiteter og fungerende ny TLS-forbindelse uten app-redeploy.
2. **Identity/no-op:** før/etter-sammenligning av alle direkte barn og CNPG-genererte Pods/PVC-er/Services/cert-Secrets. Cluster/Pooler/PVC/rolleobjekter beholder UID, application-Secret har identiske data (inklusive PKCS#8), samme PGHOST/PGUSER/CA og ingen migrasjonsutløst rollout. Naturlig cert-rotasjon isoleres fra adopsjonstesten.
3. **Adoption failures:** fremmed/manglende owner, gjenopprettet Postgres-UID, deleting objekter, manglende eller byttet Cluster/bucket, konflikter og avbrudd etter hvert barn. Fail-closed, ingen erstatningscluster, ingen Delete/Unclaim/Recreate-actions på adopterte barn.
4. **Pruning/finalizers:** test hele synchronizer, ikke bare reconciler.Update. Metadata-only migreringsactions må ikke få andre allerede-adopterte ressurser prunet i en delvis pass. Main-opprydding må ikke trigge GC av barna. Legitim Postgres-sletting skal fortsatt rydde og vente korrekt.
5. **Bindings:** ingen/main deprecated branch, Application/Naisjob, alle credential-typer, admin/durabel app-rolle, lange navn, flere databaser, CA-/client-cert-data-only-rotasjon og nettverk. Alle relasjonswatches må fungere etter at branch-CR er borte.
6. **Personlig tilgang:** legacy access aktiv under adopsjon, ny access etterpå, samme rolle/token/RelayAccess/SNI og korrekt expiry/revoke etter én time. Test immutable-schema og API-autorisasjon for begge kontrakter. Ikke lek credential- eller token-data i migreringslogger/inventar.
7. **Backup:** WAL fortsetter til samme bucket/path med samme Workload Identity; ny backup lykkes. Bekreft brukbarhet av både pre- og post-migreringsbackup ved en isolert restore-test, uten å innføre et nytt restore-produkt i denne oppgaven.
8. **Klient-/schema-matrise:** gammel og ny Console/CLI mot kompatibilitets-API før og etter migrasjon; API med gammel branch-CR og med bare Postgres. Test langlivet Console-sesjon/stale IDs, informer/cache-endringer og restart uten branch-CRD.

Pgrator følger eksisterende standard-library-/golden-konvensjoner. Legg migreringscaser ved controller-testene og en ekte upgrade-test under `tests/e2e/`; kjør `mise run generate` ved type/RBAC-endringer, `mise run test` for begge Go-moduler og `mise run check`. Envtest alene beviser ikke Kubernetes garbage collection, CNPG/Config Connector eller applikasjonskontinuitet.

### Stoppkriterier under utrulling

- Enhver Delete/Unclaim/Recreate på cluster, pooler, bucket, IAM eller rolleobjekt som bare skulle adopteres.
- Endret fysisk UID/navn, uventet Secret.data, CA/SNI, nettverkstarget eller pod-rollout.
- Nye app-tilkoblingsfeil, credential-/Relay-feil, WAL-/backupfeil eller uventede API/Console/CLI-feil.
- Ukjent migreringsstatus, eierskapskonflikt eller databaser med annen branch/recovery.

Observasjon skal primært bruke kontinuerlig SQL, CNPG readiness/archiving/backup, workload-feil og UID-/spec-inventar. Pgrator `Completed` alene er ikke en tilgjengelighetsgaranti. Oppdater også branch-tellingen i [postgres-adoption-queries.libsonnet:15](../dashboards/src/postgres-adoption-queries.libsonnet#L15) i contract-fasen; bevart legacy-label er ikke bevis på at branch-ressursen fortsatt eksisterer.

## Arbeidspakker

1. **pgrator/pkg/api + schema:** stabil navnehelper uten offentlig branch-valg, additive Postgres-statusfelter og access-referanse, kompatibilitetsvalidering og round-trip-tester.
2. **API:** branchløs read/write-kontrakt, dual-read, gammelt main-kompatibilitetslag, branch-sperre, cache/informer- og autorisasjonstester.
3. **Console + CLI:** flytt vanlig databasebruk til ny kontrakt, fjern branchvalg/-opprettelse og oppdater generert GraphQL-kode.
4. **liberator + naiserator:** deprecated input, utelat binding.branch uten å endre app-kontrakten.
5. **pgrator runtime:** flytt fysisk reconcile, metadata-adopsjon/checkpoint, branchløse binding/access-resolvers og trygg deletion/detach. Golden-, synchronizer- og upgrade-E2E-tester. Eventuell opt-in skal være liten og ikke kreve et parallelt controllersystem.
6. **Drift:** inventar, canary, kontrollert utvidelse og verifisert observasjons-/rollbackvindu.
7. **Contract:** fjern branch-readers/controller/CR-er/CRD og senere gamle GraphQL-/manifestfelter; oppdater ADR/dokumentasjon og dashboards. Ikke omskriv historiske ADR-er som om branch aldri fantes.

Releasebetegnelser må fylles inn med faktiske tags/digester når arbeidspakkene er bygget og testet. Det er foreløpig ikke kartlagt eksisterende deployede versjoner som støtter begge modeller; slike versjoner skal lages og verifiseres, ikke antas å finnes.

## Kildegrunnlag

Kartlagt lokal pgrator HEAD: `7985f2f47af424d1ca13695d204738e606cb7fe3`.
Andre undersøkte lokale HEAD-er: naiserator `74038ca6`, liberator `1376de9`, relay `a5d98c0`.
Klientrepoenes versjoner og detaljer dokumenteres i [klientnotatet](postgres-branch-client-research.md).

Historisk modell: [ADR 0002](adr/0002-logical-postgres-and-instances.md), [ADR 0007](adr/0007-name-physical-postgres-resources-branches.md). Dette forslaget erstatter branch-modellen, men beholder kravene om stabile credentials, sikker eierkontroll og ikke-destruktiv behandling av eksisterende databaser.
