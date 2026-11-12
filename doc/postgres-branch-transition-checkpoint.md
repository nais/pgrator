# Gjenopptaking: avvikling av PostgresBranch

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
