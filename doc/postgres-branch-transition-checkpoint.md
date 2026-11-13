# Gjenopptaking: avvikling av PostgresBranch

## Siste implementasjon: ekte PostgresIssue (etter compaction)

Brukerens siste bestilling er implementert lokalt i API og Console. Dette
avsnittet overstyrer beskrivelser av mellomarbeidet nedenfor.

- API har `PostgresIssue implements Issue & Node` med `postgres: Postgres!`.
  Checkeren leser Postgres-status direkte, uten MainViews/branch-modell, og
  skriver issue_type/resource_type `POSTGRES` med logisk databasenavn.
  Postgres.issues og den midlertidige branch-fasadens issues scopes peker på
  samme logiske database. Facets og filtre bruker POSTGRES.
- Undersøkelse av lagringen viste at checkeren allerede sletter alle issues
  og setter dem inn på nytt hvert minutt med tilfeldige UUID-er. Det finnes
  ikke varig issue-identitet å migrere. Ingen datamigrering er lagt til.
  Gamle POSTGRES_BRANCH-rader konverteres til ekte PostgresIssue frem til neste
  checker-refresh; deres eksisterende UUID/Node-ID beholdes mens raden finnes.
  En eksplisitt LegacyPhysicalName-adapter slår opp parent innen team/miljø,
  uten branch-API. SQL-filtre og database-scopes matcher lagrede nøkler, så
  nye POSTGRES-filtre/scopes krever at den nye checkeren har kjørt minst én
  gang. Verifiser dette før Console deployes. Adapteren skal ikke bli en ny
  permanent branch-modell.
- Node-regresjonen avdekket en eksisterende feil: GetByIdent forsøkte å parse
  den kodede GraphQL-ID-en som UUID. Den bruker nå dekodet id.ID. Testene
  dekker Node-oppslag både før og etter konvertering av gammel lagret rad.
- Gammel PostgresBranchIssue med branch-felt og POSTGRES_BRANCH-enumverdier
  finnes bare som schema-kompatibilitet under klientutrullingen. De returneres
  ikke av ny issue-konvertering/checker. Den foreslåtte mellomløsningen
  PostgresBranchIssue.postgres er fjernet. Gamle Console-klienter kan validere
  sine dokumenter, men får ikke branch-spesifikk presentasjon av den nye typen;
  API og Console bør derfor rulles ut tett etter hverandre.
- Console bruker PostgresIssue i home/issue-presentere, direkte databasefelt,
  lenker uten branch-parametre og POSTGRES-filteret. Testfilen er omdøpt til
  PostgresIssue.test.ts. Filteret skjuler kompatibilitetsverdien POSTGRES_BRANCH.
  Query-regresjonen avviser nå også PostgresBranchIssue. Historiske aktiviteter
  er bevart. Schema er hentet normalt fra isolert lokal introspection-server;
  serveren er stoppet og midlertidig kildefil fjernet.
- Begge Copilot-rettingene er beholdt uendret: tidlig Adopting-vakt ved sletting
  i pgrator og stale implicit activeBranch-normalisering i API.
- Validering: pgrator full test/check/build og controller/synchronizer race
  består. API full test med integrasjon/race, generering, build, staticcheck,
  deadcode og Helm består. Full API-check feiler fortsatt på fire eksisterende
  G115-funn i vulnerability/webhook; separat vulncheck har fortsatt kjente
  sikkerhetsfunn. Console sync/check/lint, 1841 tester i 111 filer, build og
  Helm består. Svelte-filene er validert av svelte-file-editor med skills/MCP;
  eneste autofixer-advarsler er kjent falsk positiv om Houdini fragment-stores.
- Ingen commits, push, PR-svar/resolving, deployments eller live clusterendringer
  i denne runden. Alle endringene er fremdeles lokale på de samme branchene.

Neste utrullingsrekkefølge: API med ekte PostgresIssue → bekreft checker-refresh
og POSTGRES-rader → ny Console overalt → fjern gammel GraphQL-kontrakt samlet.
CRD/RBAC-retirement er fortsatt separat og krever eksplisitt autorisasjon.

## LES FØRST etter compaction: siste beslutning om issues

Brukeren ba om compaction før videre arbeid. **Siste avtalte mål er en ordentlig
PostgresIssue med direkte postgres-relasjon, IKKE å fjerne Postgres-issues og IKKE
å beholde PostgresBranchIssue med et nytt postgres-felt som sluttmodell.** Dette
avsnittet overstyrer den foregående arbeidsretningen dokumentert nedenfor.

Avtalt videre retning:

1. Implementer `PostgresIssue implements Issue & Node { ... postgres: Postgres! }`
   i API #620, og la issue-checker/konvertering representere feil på Postgres.
   Bevar gjerne lagrede issue-identiteter gjennom en eksplisitt adapter fremfor
   å lage duplikate issues eller en unødvendig datamigrering; dette er **ikke
   ferdig designet/implementert** ennå. Undersøk issue-modell/lagring før endring.
2. Flytt Console til den nye PostgresIssue-typen og direkte databaselenker.
3. API må kjenne ny GraphQL-type før Console kan spørre etter den. Påstanden om
   at en slik Console kan rulles ut helt før noen API-forberedelse var feil og er
   korrigert med brukeren. Behold gammel schema-kontrakt kun gjennom nødvendig
   klientutrulling; fjern deretter legacy branch-GraphQL samlet. Ikke fjern
   funksjonaliteten som rapporterer Postgres-feil.
4. CRD-/RBAC-retirement er fortsatt separat etter at uavhengige binærer er ute.
   Ingen deploy, clusteropprydding eller ny push er autorisert av dette notatet.

**Faktisk lokal kode ved compaction (mellomarbeid, ikke forveksle med målet):**

- pgrator: branch `feat/postgres-branch-contraction`, publisert commit `8d9f497`,
  PR https://github.com/nais/pgrator/pull/206. Ucommittet Copilot-fiks og test:
  Adopting må avvises før deletion-shortcut, slik at delvis overførte ressurser
  ikke slettes som et komplett direkte inventar. Reprodusert rødt før retting;
  test/check/build/race er grønne etterpå. Behold denne rettingen.
- API: samme branchnavn, publisert commit `6d3ad6d4`, PR
  https://github.com/nais/api/pull/620. Ucommittet Copilot-fiks: stale implicit
  activeBranch uten clusterName normaliseres til main/Progressing; eksplisitt
  ikke-main avvises fortsatt. Enhets-/GraphQL-regresjon og oppdatert modelltest
  er grønne. Behold denne rettingen.
- API har i tillegg ucommittet **tidligere foreslått mellomløsning**:
  PostgresBranchIssue.postgres, deprecated branch-relasjon, resolver og
  GetPostgresByPhysicalName-helper, generert GraphQL og integrasjonstester.
  Dette må nå tilpasses den nye PostgresIssue-modellen; ikke commit/push det som
  om brukeren ønsket å beholde branch-issue-modellen permanent.
- Console: eksisterende `feat/postgres-without-branches`, PR #735, lokalt
  fast-forwardet til remote `446a5dba` (inkluderer brukerens branchfrie metrics).
  Ucommittet arbeid fjerner branch-modellspørringer fra issues/home og bruker
  **fortsatt typename PostgresBranchIssue med postgres-feltet** fra mellomløsningen.
  Endre dette videre til PostgresIssue når API-designet er klart. Historiske
  activity-typer skal ikke slettes blindt.
- Console har en ny, ennå utracket test
  `src/lib/domain/postgres/branchFreeQueries.test.ts` som kontrollerer 246
  GraphQL-dokumenter (inklusive inline queries). Check/lint/build/Helm og 1833
  tester i 110 filer består på mellomløsningen. To endrede Svelte-filer er
  validert med skill/MCP av svelte-file-editor-agenten; husk samme krav videre.
- Console-schema ble generert fra lokal introspection-server, ikke manuelt
  redigert. Tre whitespace-linjer var allerede endret før denne runden.
  Den midlertidige serveren på 39001 og kildefilen er stoppet/fjernet.
- API full test inkl. integrasjon/race, build/staticcheck/deadcode og generering
  består på mellomløsningen. Full API-check har fortsatt kjente, uvedkommende
  gosec-/Go-/dependency-funn. Docker-testene på denne maskinen trenger
  `DOCKER_HOST=unix:///Users/Frode.Sundby/.docker/run/docker.sock`, og publiserte
  avhengigheter skal testes med `GOWORK=off`.
- Ingen av de siste Copilot-rettingene eller frontend-/issue-endringene er
  committet/pushet. PR-kommentarene i #206 og #620 er ikke besvart/resolved.
  Ingen live clusterendringer i denne runden. Dokumentene har ucommittede noter.

Start etter compaction med git-status i alle tre repoer og AGENTS.md. Ikke
overskriv brukerarbeid eller revert hele filer for å bytte issue-retning; skill
de to ønskede Copilot-fiksene fra mellomløsningen som skal videreutvikles.

## Nyeste prioritering: branchløs Console før brytende GraphQL-fjerning

Brukeren ønsker å få Console helt fri for branch-spørringer og rulle den ut først,
før GraphQL-fasaden fjernes samlet. Dette har forrang over eldre oppryddingsplaner.

- PR pgrator #206 (branch feat/postgres-branch-contraction, publisert base 8d9f497)
  har én Copilot-retting lokalt: Adopting-journal avvises før deletion-shortcut,
  slik at delvis overførte ressurser ikke kan slettes/finaliseres som et komplett
  direkte inventar. Regresjon dekker slettende parent med fortsatt branch-eid barn.
- PR API #620 (samme branchnavn, publisert base 6d3ad6d4) har én Copilot-retting
  lokalt: implisitt activeBranch normaliseres til main også uten clusterName ved
  stale ikke-main metadata. Virtuell main forblir Progressing; eksplisitt ikke-main
  gir fortsatt not-found. Dekket både med enhetstest og GraphQL-integrasjonstest.
- Konkret gjenværende Console-konsument var PostgresBranchIssue.branch i
  homeAttention.gql og issue-presenterne. API har nå additiv PostgresBranchIssue.postgres
  lokalt, og branch-feltet beholdes deprecated. Direkte parent-oppslag er scoped
  til team/environment og skjer uten branch-API-et.
- Console arbeides på eksisterende feat/postgres-without-branches / PR #735.
  Den ble fast-forwardet fra 1498551a til brukerens remote 446a5dba, som allerede
  hadde fjernet branch-avhengigheten i metrics og merget nyere main. Eksisterende
  schema.graphql-diff var bare tre whitespace-linjer og ble ikke forkastet.
- Lokale Console-endringer bruker postgres { name } for issues, databasebaserte
  lenker/labels uten «/ main», og ingen aktive branch-modellspørringer. En ny
  regresjon skanner 246 GraphQL-dokumenter, inklusive inline component-queries;
  historiske branch-activity scalarfelt og issue-typenavn beholdes som historikk.
- Console-schema er hentet normalt fra en isolert lokal introspection-server med
  den additive API-endringen. Serveren og midlertidig kildefil er stoppet/fjernet.
- Validering: pgrator test/check/build og race består. API full test inkl.
  integrasjon/race, build/staticcheck/deadcode og GraphQL-generering består.
  Console check har 0 feil/advarsler; lint, build, Helm og 1833 tester i 110 filer
  består. De gamle API-gosec-/dependency-funnene er ikke oppgradert i denne runden.
- Ingen av disse siste rettelsene/Console-endringene er committet eller pushet,
  og ingen PR-tråder er besvart/resolved eller miljøer endret i denne runden.

Praktisk rekkefølge: additivt API-issuefelt tilgjengelig → branchløs Console ut
overalt → samlet brytende GraphQL-opprydding og separat, kontrollert CRD-retirement.
Ikke deploy ny Console før API-feltet finnes; ikke fjern GraphQL-branch-modellen
før de branchløse UI-queryene er i bruk.

## Pågående kontraktrunde etter fullført adopsjon

Nyeste arbeidsrunde: bruker har pullet main i pgrator og API etter at adopsjon er
rullet ut overalt. Begge repoer arbeides nå på **feat/postgres-branch-contraction**.
Se [kontraktplanen](postgres-branch-contraction.md) for gjeldende scope og rekkefølge.
Tidligere ucommittede valideringsnotater i denne filen er bevart.

- Pgrator: aktive Postgres-/binding-/access-controllere bruker ikke branch-API-et.
  Watch/oppslag/adopsjon/detach er fjernet fra den registrerte stien. Ny database
  provisioneres direkte; ufullført adopsjon må ferdigstilles med forrige release.
  Journal-/UID-sperrer, fysiske identiteter, metric-labels og eksplisitt deletion
  beholdes. Delte historiske renderer-/Go-typeadaptere er ikke en live CRD-avhengighet.
- API: branch-informeren og dens loader/gRPC/issue/inventory-avhengigheter er borte.
  Bare Postgres-status brukes, også for logger og restore-vindu. Virtuell main,
  stabile node-/issue-identiteter og lesing av gamle immutable main-accessrefs
  beholdes. Nye accesses skriver alltid spec.postgres. Ikke-main velger aldri main.
- Testene er oppdatert til direkte modell. En faktisk envtest-server uten branch-CRD
  starter pgrator-controllerne og provisionerer en database. API-testen starter den
  vanlige watcher-pakken med branch-kall sperret og dekker main-fasade/access/logger.
  Full API-integrasjonstestsuite består med eksplisitt lokal Docker Desktop-socket.
- CRD-templater og eksisterende RBAC-rettigheter er **bevisst beholdt** i dette første
  kontraktsteget, slik at gamle prosesser ikke mister tilgang midt i utrullingen.
  Selve CRD-/RBAC-/resterende typefjerningen er separat, etter at begge nye binærer
  er ute og inventaret er kontrollert.
- Ingen live clusterendringer eller push er gjort i denne arbeidsrunden.
- Sluttvalidering: pgrator full check/build/test (begge moduler), generering og
  controller/synchronizer race består. API full test inkl. integrasjon/race, build,
  vet, staticcheck, deadcode, Helm lint og generering består med GOWORK=off.
  API full check har fortsatt eksisterende G115-funn i vulnerability/webhook-kode
  og dependency-/Go-sårbarheter; ikke presentér samlet API-check som grønn.

Oppdatert 2026-11-12. Dette notatet er gjeldende arbeidstilstand og har forrang over historiske fremdriftsavsnitt i [overgangsplanen](postgres-branch-transition-plan.md). Les også [klientkartleggingen](postgres-branch-client-research.md) ved behov.

## Ny arbeidsrunde: automatisk fysisk overtakelse (2026-11-12)

Dette avsnittet har forrang over det historiske kompatibilitetscheckpointet nedenfor.

- Brukeren bekrefter at pgrator, liberator/CRD-er, naiserator og API nå er ute i **alle miljøer**. Console er foreløpig bare ute i test; Insights, Queries og Logs fungerer der.
- Console-branchen er oppdatert med main `3d647748` og pushet som `1498551a`. 109 testfiler / 1587 tester, check/lint/build og Helm-lint bestod. Copilot på PR #735 har ingen konkrete kodefunn; spørsmålet var live backend-/metrics-kompatibilitet, som brukeren etterpå bekreftet fungerer.
- Brukeren ønsker **automatisk adopsjon uten bruker-opt-in**, validert i dedikerte testmiljøer først. Kun pgrator endres i denne runden. Ikke krev manifestendringer eller app-redeploy.
- Brukeren pullet main (`341d6e8`, kompatibilitets-PR #203 merget). Ny lokal arbeidsbranch: **`feat/postgres-direct-ownership`**. Dokumentnotatene som allerede var ucommittet på main ble bevart. Ingen push eller deploy er utført i denne arbeidsrunden.
- Implementert lokalt: `PostgresClusterReconciler` erstatter begge gamle controllere ved runtime-registrering. Legacy physical renderers deles som private identitetsadaptere; ingen syntetisk branch opprettes i Kubernetes. Tidligere controllerkode/tester beholdes som referanse for ressurskontrakten.
- `Postgres.status.ownership` journalfører fase og branch-/cluster-/ressurs-UID-er før adopsjon. Første overtakelsespass er metadata-only. Direkte status publiseres etter overført eierskap. Frakobling verifiserer ferskt inventar og fjerner kun branchens egen finalizer før branch-sletting, aldri gammel Unclaim-path.
- Bindings fungerer uten branch etter publisert direkte status, med samme fysisk identitet, roller, Secrets og credentials. Ugyldig direkte target gir feil uten pruning av eksisterende credentials. Access kontrollerer også journalført cluster-UID.
- PostgreSQL-synchronizere bruker uncached API-klient for å unngå stale status/branch-slettings-race. Leader election på samme lease er påkrevd for skrivende runtime. Additive CRD-endringer må installeres før ny binær.
- Journalførte ressurser som forsvinner/byttes stopper reconciliation fremfor å bli automatisk erstattet. Dette gjelder foreløpig også reparerbare barn; ikke slett journalen for å rydde en feil. Eksisterende arkiver beholdes om WAL-opprettelse senere slås av.
- Generisk annotasjonsbasert pruning er eksplisitt deaktivert for den nye Postgres-controlleren; fysisk sletting skjer bare via dens ferske, eksplisitte Delete-inventar. Andre controllere beholder vanlig pruning. Dette beskytter også fremmede ressurser med en feilaktig eierannotasjon.
- Tester dekker metadata/spec/UID-bevaring med/uten WAL, delvis adopsjon og restart/retry, mislykket checkpoint-write, configdrift, fremmed/byttet/manglende target, ekte API-server/admission, ny branchløs provisioning, trygg frakobling/deletion og uendrede Application/Naisjob binding-snapshots med utelatt/main.
- Ekte CNPG/data-plane upgrade-E2E er **ikke kjørt**. `tests/upgrade/postgres-ownership/README.md` og `continuity.yaml` beskriver isolert test med sentinel, vedvarende forbindelse og nye skrivende forbindelser. Denne testen, backup/recovery-kontroll og stop/resume-repetisjon må gjennomføres før videre rollout. Ikke påstå bevist nedetidsfrihet basert på envtest.
- Etter brukerens presisering bruker upgrade-fixturen en vanlig Application med `spec.uses.postgres`, ikke håndlaget Pod/PostgresBinding. Naiserator skal generere binding, env/mounts og netpol. To SQL-workers kjører i appen; enhver worker-exit låses som FAIL, og uendret Pod-UID/oppstartstid og null restarter er egne akseptansekriterier (Applications bruker restartPolicy Always).
- Etter at adopsjon er startet er rollback til gammel branch-eiende binær **ikke støttet**: den kan gjenskape branch og konkurrere om/prune ressurser. Stopp pgrator og deploy en rettet, adopsjonsbevisst binær ved feil. Ingen automatisk revers migrering implementeres her.

Lokal sluttvalidering: `mise run fmt ::: generate`, `mise run test ::: build ::: check` består (begge Go-moduler, lint/vet/deadcode/Helm/vulncheck). Race-tester av `internal/controller` og `internal/synchronizer/...` består. Upgrade-fixturens YAML er syntaksvalidert; reell CNPG-kjøring gjenstår.

Neste: avtal commit/push og kjør den isolerte oppgraderingstesten før utrulling. All eldre versjons-/rollout-status nedenfor er historisk.

### Dev-testen er etablert, før eierskapsoppgradering (2026-11-12)

Brukeren autoriserte opprettelse i `dev-nais-dev`, namespace `basseng`, via `nais apply`. Første forsøk ble stoppet før apply fordi NAIS-innloggingen pekte på nav-tenanten. Etter at brukeren byttet til `frode@dev-nais.io`, ble `nais apply tests/upgrade/postgres-ownership/continuity.yaml --team basseng --environment dev --wait` kjørt og begge ressurser opprettet.

- **Pgrator er ikke oppgradert:** fortsatt `2026-11-11-154133-341d6e8`, med gammel branch-eiende controller. Naiserator kjører `2026-11-11-101104-2c4ff6f`.
- Postgres `ownership-continuity`, UID `8fabae66-548b-49e0-98db-d22cbfb08f41`.
- Application `ownership-probe`, UID `dbba2088-6d07-4be9-bc35-397905a5e7b2`. Naiserator har automatisk laget Deployment, binding, env/TLS-mounts og app-netpol. Pgrator har automatisk laget bindingens ingress- og egress-netpol og connection-Secret. Ingen håndlagde nettverksressurser/Secrets/bindings.
- App-Pod `ownership-probe-585578f448-mhmpd`, UID `009264c3-7881-46f0-9cf5-6c24e48c36f3`, container startet `2026-11-12T08:50:06Z`, Ready og null restarter.
- Binding `ownership-continuity-ownership-probe`, UID `2dff6648-010d-4ab6-9133-120a80525fe7`, Ready, uten spec.branch, credentials admin/readwrite. Secret-referanse `ownership-continuity--ownership-probe-d0436faa-connection`.
- Branch `ownership-continuity-main-9e8f586e`, UID `bf93c99b-5b63-4adc-9a74-bc60433d0b99`.
- Cluster `pg-ownership-continuity-main-9e8f586e`, UID **`a1086c18-2591-4f18-b8d2-28ca5c372b8e`**, fortsatt kontrollert av denne branchen. Healthy og 2 instanser klare.
- CNPG system ID **`7707204630358376469`**, lest med read-only `pg_control_system()` på primary.
- Database-Pod-UID-er: primary `0dfd89fc-8670-427e-931b-7bbad1777565`, replica `83d813b1-58cd-4004-9215-d2ac6f3fac2e`. Pooler-Pod-UID-er `680b39b3-124b-449a-b45a-bf71836bcd91` og `f05e1f90-5bd7-4514-883f-ed36670b0167`. Alle Ready, null restarter.
- Innledende oppstartstimeouts og midlertidig WAL-provisioneringsfeil løste seg før baseline. Begge SQL-workers kjører nå: sentinel-count 1 og vellykkede nye skrivende forbindelser økte fra 114 til **272** ved siste kontroll rundt 08:56 UTC. Vedvarende worker viste vellykket sentinel-lesing hvert sekund. Ready-markør finnes, failure-markør finnes ikke.
- ContinuousArchiving True fra `08:52:19Z`. Backup `pg-ownership-continuity-main-9e8f586e-20261112085219`, UID `5b1e3481-d800-4bec-a610-ec0e0ce25cd6`, completed `08:52:21Z`.
- Baseline med **47 ressurser**, funksjonelle specs, eier-referanser og SHA256-digester for 4 relevante Secrets er lagret med filmodus 0600 i `/private/var/folders/4m/5kvghh9d6klgr9vkw642dp0r0000gn/T/opencode/postgres-ownership-baseline-dev-nais-dev-basseng-20261112.json`. Ingen Secret-data er skrevet til snapshotet eller samtalen. Bevar filen for før/etter-sammenligning; den ligger utenfor git.

Appen er **etterlatt kjørende med kontinuerlige testskrivinger** (omtrent én ny forbindelse/INSERT per sekund). Ikke redeploy/restart den før overtakelsestesten. Neste steg er avtalt publisering/utrulling av den nye pgrator-versjonen med additive checkpoint-CRD-er, deretter sammenligne baseline og kontrollere SQL-kontinuitet/backup. Denne opprettelsen og baselinen er **ikke** en test av adopsjon ennå; ny kode er fortsatt lokal og ucommittet.

### Baseline avbrutt av Pod-/nodeutskifting før rollout

Senere 2026-11-12: implementasjonen er committet/pushet som **`3828014`** på `feat/postgres-direct-ownership`, men **ikke rullet ut**. Brukeren meldte SSL-feil i ownership-probe. Lesende undersøkelser og en ny read-only SQL-forbindelse bekrefter følgende:

- Pgrator kjører fortsatt `341d6e8`. Clusteret har samme UID `a1086c18-2591-4f18-b8d2-28ca5c372b8e`, generation 1; Pooler/Deployment har generation/revision 1. Ingen adopsjon har skjedd.
- **08:58:02 UTC:** opprinnelig pooler-Pod `...-vbnhb` stoppes. **08:58:03:** ny-forbindelses-worker får `Connection refused`, og proben logger `FAIL ownership continuity: a probe worker stopped (exit=2)`. Failure-markøren er til stede. Skrivingene stopper på **382**.
- **09:00:07:** CNPG rapporterer eksplisitt SwitchingOver fordi primary-noden `...-j49h` er unschedulable: primary flyttes fra instance 1 til 2.
- **09:02:52:** den andre opprinnelige pooler-Poden `...-krfwh` stoppes. **09:02:53:** den vedvarende psql-forbindelsen logger `SSL error: unexpected eof while reading` / `connection to server was lost`.
- **09:10:01:** ny SwitchingOver fra instance 2 til 1 fordi node `...-jtoc` er unschedulable.
- Node-events viser at opprinnelige pooler-/databasenoder blir NotSchedulable/NotReady og fjernes fordi de ikke lenger finnes hos cloud provider. Det er **nodeutskifting/drenering**, men eksakt utløsende årsak (autoskalering, nodeoppgradering eller annet) er ikke fastslått her.
- App-Pod har fortsatt opprinnelig UID/oppstartstid og null restarter. Probe-main holder feilen låst. `kill` av bakgrunnsfunksjonen avsluttet ikke dens psql-barn med en gang; derfor fortsatte sentinel-utskrifter etter den første FAIL-meldingen, til pooler-forbindelsen også ble brutt. Dette skjulte FAIL i korte logg-tail-utdrag, men failure-markøren og full logg fanget korrekt første feil. Forbedring av prosessopprydding i testscriptet kan vurderes separat.
- Ny read-only SQL-forbindelse fra appen lykkes etterpå: sentinel-count **1**, successful_new_connections **382**. Dette beviser data/ny tilgang nå, ikke uavbrutt kontinuitet.
- Bevis lagret i OpenCodes tempmappe: `ownership-probe-before-upgrade-failure.log`, `ownership-probe-pod-events.json`, `ownership-probe-node-events.json`. Ikke overskriv den opprinnelige baseline-filen.

**Gjeldende teststatus: feilet baseline, workers er stoppet.** Ikke rull ut ny pgrator og tolk denne proben som kontinuitetsmåling. Først avklar/stabiliser nodeutskiftingen, avtal restart/redeploy av bare testappen, og registrer et nytt målevindu/baseline mens gammel pgrator fortsatt kjører. Ingen restart, nodemanipulasjon eller utrulling er utført av assistenten under diagnosen.

### Nytt grønt målevindu klargjort på brukerens beskjed

Brukeren ba om å gjøre klart før egen pgrator-utrulling. Assistenten restartet **kun** Deployment `ownership-probe` i basseng. Ny app-Pod `ownership-probe-5ddb657f46-25ck4`, UID **`0293262c-482f-4811-81fc-df769f315ede`**, startet `2026-11-12T09:16:54Z`. Gammel feilet Pod er borte. Ny Pod har null restarter, begge SQL-workers viser vellykkede operasjoner, ready-markør finnes og failure-markør finnes ikke. Testtabellen har nå to sentinels (én per oppstart) og skrivingene økte fra 382 til **632** ved siste kontroll rundt 09:21 UTC; dette er kumulative tellere, ikke 632 nye siden restart.

Database/poolere har vært uendret i dette nye målevinduet, alle Ready og null restarter: instance 1 UID `7b5d7f79-e3ac-48bc-94b0-6d3234d5de54`, instance 2 UID `501aadf1-b090-41c6-a94d-460dba86a76e`, pooler-UID-er `b5b47c61-1794-4b34-8b44-31b02427c304` og `a1091c80-ff59-44ef-9a2c-622829c4c634`. De tre aktuelle nodene er Ready og ikke unschedulable. Cluster-UID er fortsatt `a1086c18-2591-4f18-b8d2-28ca5c372b8e`, branch-kontrollert; gammel pgrator `341d6e8` kjører fortsatt.

Fersk baseline (48 ressurser, 4 Secret-hasher, capturedAt `2026-11-12T09:20:32Z`) ligger i samme tempmappe som originalen, med navn **`postgres-ownership-baseline-dev-nais-dev-basseng-20261112-run2.json`**. Original feilet baseline er bevart.

**Utrullingsforutsetning:** installert Postgres-CRD i dev mangler fortsatt `status.ownership` ved klargjøringen. Brukeren må rulle ut nytt chart/additiv CRD **før** ny controller starter, ikke bare endre image. Assistenten har ikke installert CRD eller rullet ut pgrator. La ny app-Pod kjøre urørt gjennom testen.

### Eierskapsovertakelse validert i dev etter brukerens utrulling

Brukeren rullet deretter ut **`2026-11-12-100708-3828014`** i `dev-nais-dev`. Assistenten verifiserte lesende ca. 09:25–09:27 UTC; ingen restart, deploy eller opprydding ble gjort i valideringen.

- `ownership-continuity.status.ownership.phase = Managed`, journalført cluster-UID `a1086c18-2591-4f18-b8d2-28ca5c372b8e`, legacy branch-UID `bf93c99b-5b63-4adc-9a74-bc60433d0b99` og 14 fysisk eide ressurser. Direkte `clusterName`, observert konfigurasjon og cluster-readiness er publisert. Reconciliation Completed kl. 09:23:49 UTC. Additivt checkpoint-schema er dermed installert og statusjournalen bevart.
- Legacy branch er borte. **Alle fire Postgres i basseng** (`frogres`, `johngres`, `nobranchgres`, `ownership-continuity`) viser Managed/Completed og direkte clusterName; ingen PostgresBranch-objekter gjenstår i namespacet. Kun testdatabasen har full før/etter-sammenligning og automatisk SQL-måling.
- Run2-baseline er sammenlignet mot levende ressurser: **48 referanser kontrollert, eneste forventede bortfall er legacy branch; 14 direkte branch-barn har skiftet controller til riktig Postgres-UID. Ingen uventede endringer i UID, funksjonell spec, labels, finalizers eller øvrige annotations/ownerReferences. Alle fire Secret-datahashene er uendret.** CNPG-descendants og binding/app-eide ressurser har beholdt sine eiere.
- App-Pod har samme UID `0293262c-482f-4811-81fc-df769f315ede`, samme starttid `09:16:54Z`, Ready og null restarter. Ready-markør finnes og failure-markør finnes ikke. Database-/pooler-Pod-UID-er samsvarer med run2-baseline, uten restarter under adopsjonen.
- SQL-logg fra `09:16:55` til `09:26:54`: 601 vellykkede vedvarende lesinger (maksimalt tidsrom 1,006 s), 548 vellykkede nye skrivende forbindelser (maksimalt tidsrom 1,314 s), **ingen SQL-/SSL-feil eller FAIL**. Rundt adopsjonen (`09:23:35–09:24:05`) er største tidsrom henholdsvis 1,000 s og 1,102 s. Dette beviser observert kontinuitet i dette målevinduet, ikke en generell garanti mot alle driftsavbrudd.
- Read-only ekstrasjekk fra appen lykkes etter adopsjon: 2 sentinels, kumulativt 939 vellykkede skrivinger ved kontrollen (tallet fortsetter å vokse). CNPG system ID er fortsatt `7707204630358376469`.
- Binding er Ready etter ny reconcile kl. `09:23:51Z`, med samme binding-UID og connection-Secret-referanse. Statusmelding bruker fortsatt historisk «selected Postgres branch»-ordlyd; funksjonen er branch-uavhengig.
- Clusteret er healthy, WAL ContinuousArchiving True. Eksisterende backup fra `08:52:21Z` er fortsatt completed. **En ny backup etter adopsjonen og reell recovery fra arkivet er ikke testet ennå.** Ingen error/failed/refusing/panic-funn i innhentet pgrator-logg.
- Full sammenligningsrapport (inkludert filtrert ettersnapshot, uten Secret-plaintext): `postgres-ownership-after-adoption-dev-nais-dev-basseng-20261112.json` i samme OpenCode-tempmappe. SQL-logg `ownership-probe-after-adoption.log` og controllerlogg `pgrator-direct-live-adoption.log` er også bevart der.

Gjeldende status: **automatisk, WAL-aktivert adopsjon av eksisterende database med reell NAIS Application og observerte uavbrutte SQL-operasjoner er grønn i dev**. Appen er fortsatt etterlatt kjørende. Før bredere rollout gjenstår blant annet ny backup/recovery, nye/gamle personlige tilganger, kontrollert stop/resume av adopsjonsbevisst pgrator og separat slettetest av en disponibel database. Ikke hevde at alle disse akseptansekriteriene allerede er dekket av happy-path-testen.

### Supplerende dev-tester bekreftet av brukeren

Brukeren bekrefter etter adopsjonen:

- Personlig tilgang fungerer som forventet.
- Restart av ny pgrator gikk fint.
- Opprettelse av ny database, tilkobling av Application og sletting fungerer fint.

Dette er brukerens testresultater, ikke ytterligere tester utført av assistenten. Brukeren opplyser også at det ikke finnes noen ny brukerrettet backup-/restore-mekanisme etter at branch-flyten ble fjernet. Skill dette fra automatisk CNPG/Barman WAL-arkivering og ScheduledBackup, som skal bevares av adopsjonen. Ny branchløs restore-funksjonalitet er en egen oppgave; den skal ikke snikinnføres i eierskapsovergangen. Ny vellykket backup etter adopsjon og en faktisk restore er fortsatt ikke dokumentert i denne arbeidsrunden. En eventuell teknisk engangsbackup/recovery-test kan bruke CNPG/Barman direkte i testmiljø, uten å gjeninnføre branches, men ingen slik handling er utført eller autorisert her.

### Copilot PR #205: avgrensede sikkerhetsrettelser

Brukeren ba om å vurdere reviewet med få/lite bevegelige databaser og overgangskodens levetid i mente. Alle fire konkrete funn ble reprodusert med regresjonstester og rettet **lokalt**, uten generell ressursutskiftings-/IAM-migreringsprotokoll:

1. `recreateIAM` avviser direkte Postgres-eiere før en delete kan planlegges, også med ResyncIAMPermissions aktivert. Gammel branch-kontrakt er uendret.
2. `checkInventory` bygger ny slice lokalt og erstatter journalen først etter komplett validering. Prepare-feil/deferred status-write kan dermed ikke miste lagrede UID-sperrer.
3. Planlagte writes til journalførte ressurser pakkes med `action.RequireUID`, basert på journalens UID, ikke en senere related-read. Gjennomføringslesinger krever samme levende UID, create-fallback/delete av target er sperret, og eksisterende Update/Claim beholder optimistic-write-beskyttelsen. Ingen automatisk utskifting implementeres.
4. Binding Prepare håndterer slettende Postgres før direkte cluster-oppslag. Eksplisitt sletting trekker tilbake Secret/netpol og Ready selv med manglende/deleting cluster; levende, midlertidig ugyldige targets bevarer credentials som før.

Nye tester dekker IAM-kontoforskjeller/policyforskjeller, bevart persistert journal ved ny deleting-child, 6 ressurstyper med bortfall/utskifting mellom Prepare/planlegging/gjennomføring, og bindings under parent-deletion med manglende eller finalizer-blokkert cluster. `mise run test`, `mise run check ::: build`, format/generering og race-tester av controller/synchronizer består. Rettelsene og oppdaterte devnotater inngår i reviewoppfølgingen på feature-branchen. **Ingen clusterendringer i denne runden.**

### Utrullet overalt; lesende validering i nav-dev / nais

Brukeren bekrefter utrulling til alle miljøer. På brukerens forespørsel ble namespace **nais** i kubecontext **nav-dev** validert lesende 2026-11-12. Dette er ikke en kontroll av alle namespace i clusteret. I nais finnes én Postgres: **vegar-test**.

- Pgrator-image `2026-11-12-121429-36bcaab`, Deployment 1/1 Ready/Updated/Available, utrulling fullført ca. `11:20:57Z`.
- Postgres `vegar-test`, UID `df700625-2d0c-46ca-8799-2d719d6bbc09`, Managed/Completed, generation og observedGeneration 1. Siste reconcile `11:22:36Z`. Ingen PostgresBranch-objekter i nais.
- Cluster `pg-vegar-test-main-908b5c9e`, UID `bac209ce-64bd-4842-93b6-44f2e4cfa5d8`, healthy, 2/2 Ready og eid direkte av korrekt Postgres. Alle **14 journalførte ressurser** er kontrollert: faktisk UID matcher journal, riktig controller-UID/kind/API-versjon, ny owner-annotasjon og ingen gammel branch-owner-annotasjon eller deletionTimestamp. Alle 5 IAM-/bucket-ressurser er Ready/UpToDate med observert generation lik faktisk generation.
- Database og alle 3 DatabaseRoles Applied; durable roller eies av Postgres, workloadrollen beholder PostgresBinding-eier. Pooler active, 2 instanser og 2 Ready service-endepunkter på 5432. Begge PVC-er Bound med 10Gi.
- Database-Podene er fra 10. november, pooler-Podene fra 11. november; alle Ready og null restarter. Appen frode-testapp har 2 Ready Pods fra `2026-11-11T15:05:53Z`, null restarter. De observerte Podene er dermed eldre enn denne pgrator-utrullingen.
- Binding `vegar-test-frode-testapp`, UID `13809870-790a-487e-98fc-0e621a2a9bc7`, Completed og Ready/CredentialsReady etter utrullingen. Secret-referanse `vegar-test--frode-testapp-208730ba-connection`; binding rapporterer secret/Available True. Direkte Secret-GET er sperret av brukerens Cloud IAM/RBAC; ingen omgåelse ble forsøkt og ingen Secret-data ble lest.
- WAL ContinuousArchiving True. Sist vellykkede backup fullført **12. november 02:00:03 UTC**, før adopsjonen; alle 15 observerte historiske backups completed og fortsatt cluster-eide. ScheduledBackup neste kjøring **13. november 02:00 UTC**. ObjectStore rapporterer første recoverability point **29. oktober 13:46:42 UTC**. Ingen ny backup eller restore ble trigget.
- Den observerte personlige tilgangen til vegar-test er historisk/utløpt og korrekt Ready False med reason Expired, ikke en ny tilgangstest.
- Ingen error/fatal/panic/refusing/failed-to-treff i innhentet pgrator-logg siste 30 minutter. En eldre HPA-advarsel «No recommendation» på frode-testapp var fra 11:00:01Z; nå er ScalingActive/ValidMetricFound True, current/desired begge 2, og ScalingLimited skyldes bare minimumsgrensen. Enkelte kubectl-discovery-kall fikk en transient API-nettverkstimeout før forespørslene deretter lyktes; dette ble ikke diagnostisert som en databasefeil.
- Ingen SQL-/dataplane-test eller før/etter Secret-hash-sammenligning ble gjort i nav-dev. Lagrede metadata/statusuttrekk i OpenCode-temp: `nav-dev-nais-cnpg-status.json`, `nav-dev-nais-validation-events.json`, `nav-dev-pgrator-validation.log`.

Konklusjon: ingen påviste eierskaps-, reconciliation-, CNPG-, WAL- eller workloadhelseproblemer for den observerte databasen i nais. Følg særlig neste planlagte backup som første dokumenterte backup etter denne adopsjonen. Ingen ressurser i clusteret ble endret under valideringen.

## Målet og hvor vi er

Avvikle PostgresBranch slik at eksisterende `nais.io/v1 Postgres` overtar direkte eierskap til eksisterende CNPG-ressurser, **uten å flytte eller gjenopprette databaser, endre forbindelser eller kreve app-redeploy**. Alle eksisterende databaser har kun aktiv `main`; ikke bygg en generell flerbranch-migrator.

Vi er fortsatt i **expand/kompatibilitetsfasen** av expand → adopt → contract. Omkringliggende klienter og typer er tilpasset. Fysisk adopsjon er **ikke aktivert eller ferdig implementert**. Ikke slett branch-objekter eller CRD.

## Utrulling: siste avtalte rekkefølge

1. Liberator og additive CRD-er — **brukeren bekrefter ute overalt**.
2. API med kompatibilitetsstøtte — **brukeren sier på vei ut i alle miljøer**; ikke anta at utrullingen er ferdig.
3. Pgrator med kompatibilitetsstøtte — før naiserator i øvrige miljøer. Dev er validert, se nedenfor.
4. Naiserator som produserer branchless bindings — dev er ute; øvrige miljøer etter pgrator.
5. Ny Console — må vente til ny API støtter loggforespørsler med Postgres-ID.

API og kompatibel pgrator kan rulles ut uavhengig. Viktigste produsent/konsument-regel: pgrator må støtte det naiserator skriver før ny naiserator rulles ut.

Assistenten har **ikke utført deploy**, workflow-dispatch eller merge til standardbranch. Utrullingene er utført av brukeren. Ingen nye deploy-handlinger er autorisert av dette notatet.

## Repoer og commits

Alle lokale repoer ligger under `/Users/Frode.Sundby/ws/` og står på `feat/postgres-without-branches`. Status kontrollert lokalt 2026-11-12, uten ny fetch. Alle seks arbeidskopier var rene før dette dokumentnotatet ble skrevet.

| Repo | Lokal HEAD / sist pushet feature-HEAD | Merknad |
| --- | --- | --- |
| pgrator | `f0b6ee8` | PR https://github.com/nais/pgrator/pull/203. Kompatibilitet + inaktiv Adopt-action. |
| api | `09abc4e4` | PR https://github.com/nais/api/pull/614. Logg-endring `7a698e31`, deretter merge av remote `2dca4185` (inkluderer CloudEvent source-retting fra main). |
| console-frontend | `0bbefd44` | Logg-endring + tidligere main-merge `28ce5799` er nå **pushet**, ikke lenger bare lokal. |
| cli | `15a424ec` | Direkte status/access; branch-skrivekommandoer fjernet. |
| liberator | `85283e9` | Brukeren har merget PR #365 til main som `f5e86a2c2e57b51260bb3aa721f731f2ab4dae91`. Lokal feature-branch er ikke byttet til main. |
| naiserator | `2c4ff6f0` | Pin til merget liberator-versjon, ikke lokal workspace. Standardbranch er **master**, ikke main. |

Bruk merge commits for publiserte brancher, ikke rebase/force-push. Ikke overskriv annet arbeid som dukker opp etter restart. Hent og inspiser remote før neste push.

### Dependency-versjoner

- Naiserator bruker `github.com/nais/liberator v0.0.0-20261111085329-f5e86a2c2e57`.
- Liberator og naiserator er oppdatert til Go **1.26.11** i både go.mod og mise.toml. Liberator x/net er **v0.60.0**. Full check inkl. vulncheck var grønn da disse endringene ble laget/pushet (nye advisories kan komme).
- API bruker `github.com/nais/pgrator/pkg/api v0.0.0-20261106143424-e72706ac0330`; senere pgrator-push oppdaterer ikke denne dependencyen automatisk.
- Verifiser publiserte avhengigheter med `GOWORK=off`, uten lokale replace-stier.

## Siste endring: skjul fysisk cluster i loggkontrakten

Thokra-nav kommenterte på API PR #614 at GraphQL `Postgres.clusterName` er en teknisk detalj:
https://github.com/nais/api/pull/614#discussion_r4537388181

Beslutning: **Console skal bare kjenne logisk Postgres-ID**, ikke navnealgoritme, fysisk cluster eller branch-overgang.

Implementert og pushet i begge repoer:

- Fjernet det nye `Postgres.clusterName`-feltet fra GraphQL (ikke fra intern Kubernetes-status).
- API `internal/persistence/postgres/logs.go`: `WithLogScope` er montert bak autentisering rundt eksisterende Loki-proxy. GET `/api/loki/api/v1/query_range` og `/api/loki/api/v1/query` støtter valgfri `postgres=<GraphQL Postgres-ID>` sammen med query og vanlige tids-/limit-parametre.
- API resolver riktig database og fysisk cluster fra lagret direkte status eller legacy main. Påfører namespace/service/environment samt postgres-container og escaped, forankret `<clusterName>-[0-9]+` podfilter. Fysiske metadatafilter legges før brukerens pipeline. Både logglinjer, paginering og volume/metric-expressions avgrenses. Uavklart target gir feil, **aldri bred fallback**. Vanlige Loki-requests uten Postgres-ID er uendret.
- Console har `postgresId` i LogExplorer/transport og sender denne på alle logg-, boundary-/older-page- og volume-requests. Fysisk podnavnlogikk er fjernet. Postgres logs-query velger kun id.
- Console-schema er regenerert fra isolert lokal introspection-server. Dette hentet også eksisterende webhook-typer fra gjeldende API; homeActivity.gql fikk de tre nødvendige konkrete webhook-fragmentene så SSR-kontrakttesten fortsatt består. Midlertidig server og kildefil er stoppet/fjernet.

**PR-kommentaren er ikke besvart eller resolved av assistenten.** Brukeren fikk dette svarforslaget; ikke anta at det er publisert:

> Enig, Console trenger ikke kjenne det underliggende clusteret. Har fjernet `Postgres.clusterName` fra GraphQL og flyttet oppslaget og loggfiltreringen inn i API-et. Console sender nå bare Postgres-ID; API-et velger riktig cluster, også under overgangen fra branch-basert eierskap. Oppdatert i begge repoer, med tester for både gammel og ny modell.

### Validering av loggendringen

- API: full `mise run test` inkl. integrasjon/race, build, målrettet vet/race og staticcheck/deadcode bestod. Ny dekning: legacy/direct/incomplete-direct, ugyldig/manglende ID/target, ingen bred fallback, autentisering, query og query_range, flere selectors og escaped eksakt podavgrensing (poolere/nabodatabaser ekskluderes).
- API: format/generering etter commit/remote-merge gir ren diff. `mise run --jobs 1 fmt ::: generate` ble brukt fordi parallell fmt/gqlgen kjørte i race på midlertidig `models_gen.go`.
- API samlet check er **ikke grønn**: separate G115-funn i `internal/vulnerability/queries.go:105,130` og `internal/activitylog/webhook/dispatcher.go:328,378`; vulncheck finner eksisterende Go/dependency-problemer (bl.a. Go 1.26.10, compress og OTel). Disse ble ikke oppgradert i loggendringen. Ikke si at alle API-sjekker består.
- Console: sync, Houdini/typesjekk (0 feil/advarsler), lint, **97 testfiler / 1300 tester**, produksjonsbygg og strict Helm lint bestod. Ingen browser/live-API logg-E2E er kjørt.

## Dev-observasjoner fra 2026-11-11

Kun lesende kubectl, ingen ressurser endret eller Secret-innhold hentet. Context var **`dev-nais-dev`**. Verifiser context på nytt før videre arbeid.

- Controllers i `nais-system`.
- Naiserator image `2026-11-11-101104-2c4ff6f`, Ready.
- Pgrator var først feilaktig gammel image `42c338c`; brukeren rettet dette. Deretter verifisert Deployment **og kjørende Pod** på `2026-11-10-155642-f0b6ee8`, image digest `sha256:a56712520368abb710f067fd09044c41ca8fd82f385a36fb8c35e91264c54a57`. 1/1 Ready, 0 restarter, generation observert.
- Naiserator-webhook viste fremdeles image `2026-10-08-235359-0aecd9a` ved første sjekk; ikke validert oppdatert senere.

Testkjeden ligger i namespace **basseng**:

| Ressurs | Verifisert baseline |
| --- | --- |
| Application | `frode-testapp`, UID `4fd30db6-88ad-4c62-b90e-8a89ab150159`, uses.postgres `[{name: frogres}]` uten branch |
| Postgres | `frogres`, UID `28bb9098-51cd-4fcb-9633-870186308530`, opprettet 2026-11-09 12:59:45 UTC, generation 1, PG18, 10Gi, 100m, 512Mi |
| Binding | `frogres-frode-testapp`, UID `80827bfc-a7af-4288-bbb1-5b8fd26777f5`, uten spec.branch, Ready, credentials admin/readwrite |
| Connection Secret-referanse | `frogres--frode-testapp-b0fbb68c-connection`, både i binding og app-volumer |
| Legacy branch | `frogres-main-d3b842b0`, UID `beb44726-a0ac-430b-bd96-1431651a2db0` |
| CNPG cluster | `pg-frogres-main-d3b842b0`, UID **`4e1a15f6-fc55-4402-b4cc-f2d340eb78ad`**, opprettet 2026-11-09, fortsatt eid av legacy branch |
| App-pods | `frode-testapp-5fd94d88d8-2cvkc` og `...-dhgjq`, opprettet 2026-11-09 13:30:45 UTC, begge Ready, 0 restarter |

Etter riktig pgrator-image var Postgres, branch og binding alle reconcilet **2026-11-11 09:47:48 UTC**, phase Completed. Cluster hadde samme UID før/etter, healthy, 2/2 ready. Continuous archiving True, siste backup rapportert vellykket 2026-11-11 02:00:05 UTC.

**Begrensning:** Appens siste rapporterte synkronisering var fortsatt 9. november. Dette beviser ikke at ny naiserator har kjørt appen gjennom sin ressursgenerering. Ingen SQL-forbindelse/read/write, kontrollert app-redeploy eller Naisjob-smoketest er utført av assistenten. Ingen credential-/sertifikatinnhold er sammenlignet. Ikke kall dette full oppgraderingstest eller fysisk adopsjonstest.

Bruk alltid fullt kvalifisert **`postgres.nais.io`** ved kubectl: kort `postgres` slo opp `postgres.data.nais.io` og ga misvisende NotFound.

## Viktige beslutninger og sikkerhetskrav

- Behold fysisk cluster-, pooler-, rolle-, IAM-, bucket-, sertifikat-, nettverks- og backup-identitet. Bruk eksisterende deterministiske main-avledede navn også for nye databaser i denne overgangen.
- API dual-read: lagret `Postgres.status.clusterName` velger direkte modell. Når valgt, ikke fall tilbake til stale healthy branch-status ved degraded/incomplete direkte status.
- Behold gamle GraphQL-lesekontrakter med virtuell main; ikke opprett syntetiske K8s-branches eller konkurrerende controllere.
- PostgresAccess krever nøyaktig én immutable referanse: ny postgres eller deprecated postgresBranch. Gamle tilganger må virke til expiry/revoke uten credential-rotasjon.
- Bare å bytte ownerReferences er **farlig**: pruning bruker `postgresbranch.nais.io/owner` og `postgres.nais.io/owner`. Gammel Unclaim kan slette ressurser selv etter ny controller-reference.
- WAL-bucket har force-destroy: feil opprydding kan ødelegge backups så vel som databasen.
- Adopter kun tidligere **direkte branch-eide** ressurser. Ikke reparent CNPG-eide Pods/PVC-er/Services/certifikater eller binding-/access-eide ressurser.
- `internal/synchronizer/action/adopt.go` er metadata-only, guarded, UID/controller/namespace-validert, fresh-owner-read og optimistic-lock/idempotent. Testet fake/envtest, men **ikke koblet til controllere** og er ikke en lås mot gammel writer.
- Brukeren tillater mise uten ekstra spørsmål. Push bare ved autorisasjon; aldri deploy automatisk.

## Neste steg ved gjenopptaking

1. Les dette notatet og AGENTS.md i relevante repoer. Kontroller git-status/remote og faktiske utrullingsversjoner på nytt; ikke anta at observasjonene ovenfor fremdeles gjelder.
2. Følg utrullingen API → kompatibel pgrator → naiserator i øvrige miljøer. Console må få API med ny loggkontrakt først.
3. Kontrollert dev-smoketest med eksisterende `frode-testapp`/`frogres` etter ny naiserator-reconciliation: uendrede bindings/Secret-ref/cluster-UID/roller/mounts, nye og eksisterende DB-forbindelser. Test også Application/Naisjob med utelatt og eksplisitt main. Endringer i clusteret må avtales, ikke utledes av tillatelse til lesing.
4. Neste substansielle kodearbeid i pgrator: branch-uavhengig binding-resolver, fysisk reconcile under Postgres, stoppe gammel writer/pruning, durabelt UID/checkpoint, restart/retry-sikker adopsjon og trygg detach/Postgres-deletion.
5. Før fysisk migrasjon: reell CNPG/app upgrade-E2E, datakontinuitet/forbindelser/backups og rollback-repetisjon. Ikke slett main via gammel branch deletion-path.
6. API/Console lagringsminimum bør avklares før Console-rollout: pgrator/Console har 5Gi minimum, 10Gi default; API feature var tidligere observert med 10Gi minimum. Ikke endret i loggendringen.

## Filer å starte med

- Pgrator: `internal/controller/postgres_controller.go`, `postgresbranch_controller.go`, `postgresbinding_controller.go`, `postgresaccess_target.go`, `internal/synchronizer/synchronizer.go`, `internal/synchronizer/action/adopt.go`.
- API: `internal/persistence/postgres/physical_status.go`, `logs.go`, `logs_test.go`, `internal/graph/schema/postgres.graphqls`, `internal/cmd/api/http.go`.
- Console: `src/routes/team/[team]/[env]/postgres/[postgres]/(single)/logs/`, `src/lib/domain/logs/LogExplorer.svelte`, `loki.ts` og tester.
- Naiserator: `pkg/resourcecreator/postgresbinding/`; liberator: `pkg/apis/nais.io/v1/naiserator_types.go`.

Eksterne midlertidige workspaces kan fremdeles finnes under `/private/var/folders/4m/5kvghh9d6klgr9vkw642dp0r0000gn/T/opencode/`: `postgres-branch-transition.go.work` og `postgres-clients.go.work`. Ikke bruk dem ved validering av publiserte avhengigheter. Ikke anta at noen lokal schema-server kjører.
