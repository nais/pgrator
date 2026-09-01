// Package cnpg builds CloudNativePG resources (Cluster, DatabaseRole, Pooler,
// ScheduledBackup) for the nais.io/v1 Postgres type. WAL archiving to Google
// Cloud Storage is wired in via the barman-cloud plugin and GKE Workload
// Identity; see the storage and iam packages for the surrounding resources.
package cnpg

import (
	"fmt"
	"strconv"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// DatabaseName is the application database and owner role created by InitDB.
	DatabaseName = "app"
	// OwnerRole is the durable database owner created by InitDB.
	OwnerRole = "app"
	// ReadRole and ReadWriteRole are the pre-created NOLOGIN group roles that
	// carry the object-level privileges. Memberships are managed declaratively;
	// the GRANTs themselves are a one-time bootstrap step.
	ReadRole      = "app_read"
	ReadWriteRole = "app_readwrite"

	// Every cluster runs a primary and a warm standby, so a lost node or a drained
	// pod fails over instead of taking the database down. HighAvailability adds a
	// third instance and turns on synchronous replication, which trades write
	// latency for a guarantee that no acknowledged commit is lost on failover.
	defaultInstances = 2
	haInstances      = 3
	poolerInstances  = int32(2)

	computeClass      = "n4-machines"
	nameLabel         = "postgres.nais.io/name"
	memoryLimitFactor = 4

	// BarmanPluginName is the CNPG-I plugin that performs WAL archiving and base
	// backups against an ObjectStore.
	BarmanPluginName = "barman-cloud.cloudnative-pg.io"

	// workloadIdentityAnnotation binds a Kubernetes service account to a Google
	// service account on GKE.
	workloadIdentityAnnotation = "iam.gke.io/gcp-service-account"

	// PostgreSQL memory tuning ratios.
	sharedBuffersFraction      = 4
	effectiveCacheSizeFraction = 4
	workMemFraction            = 64
	maintenanceWorkMemFraction = 8
	maxMaintenanceWorkMemBytes = 2 * 1024 * 1024 * 1024
)

var dedicatedPostgresToleration = corev1.Toleration{
	Key:      "dedicated",
	Operator: "Equal",
	Value:    "postgres",
	Effect:   "NoSchedule",
}

var minimumDiskPerStorageClass = map[string]resource.Quantity{
	"hyperdisk-balanced": resource.MustParse("4Gi"),
	"hyperdisk-premium":  resource.MustParse("4Gi"),
	"standard-rwo":       resource.MustParse("2Gi"),
	"premium-rwo":        resource.MustParse("2Gi"),
	"":                   resource.MustParse("2Gi"),
}

// ClusterName returns the CNPG Cluster name for a Postgres resource.
func ClusterName(postgres *v1.Postgres) string {
	return postgres.GetName()
}

// PoolerName returns the CNPG Pooler resource name for a Postgres resource.
func PoolerName(postgres *v1.Postgres) string {
	return PoolerNameFor(ClusterName(postgres))
}

// PoolerNameFor is PoolerName for callers that only hold the cluster name.
func PoolerNameFor(clusterName string) string {
	return clusterName + "-pooler"
}

const (
	// poolerSelectorName names the pg_hba podSelectorRef covering the pooler pods.
	poolerSelectorName = "pooler"

	// PoolerNameLabel is set by CloudNativePG on pods belonging to a Pooler.
	PoolerNameLabel = "cnpg.io/poolerName"

	// poolerAuthUser is the certificate CN CloudNativePG gives PgBouncer for its
	// connections to PostgreSQL (pgbouncer's server_tls_cert_file).
	poolerAuthUser = "cnpg_pooler_pgbouncer"

	// pgIdentMap names the pg_ident map referenced from the pooler pg_hba rule.
	pgIdentMap = "pooler"
)

func objectMeta(postgres *v1.Postgres, name string) metav1.ObjectMeta {
	var annotations map[string]string
	if postgres.GetCorrelationId() != "" {
		annotations = map[string]string{
			api.DeploymentCorrelationIDAnnotation: postgres.GetCorrelationId(),
		}
	}

	return metav1.ObjectMeta{
		Name:      name,
		Namespace: postgres.GetNamespace(),
		Labels: map[string]string{
			nameLabel: postgres.GetName(),
		},
		Annotations: annotations,
	}
}

func enforceMinimumDisk(diskSize resource.Quantity, storageClass string) (resource.Quantity, error) {
	minimum, ok := minimumDiskPerStorageClass[storageClass]
	if !ok {
		return resource.Quantity{}, fmt.Errorf("no minimum disksize defined for storage class %q (this is a platform error)", storageClass)
	}
	if diskSize.Cmp(minimum) < 0 {
		return minimum, nil
	}
	return diskSize, nil
}

// WALArchive describes the Google-backed WAL archive for a cluster. It is empty
// when WAL archiving is disabled (for instance in local development, where no
// Config Connector is available), in which case neither Workload Identity nor
// the barman-cloud plugin is configured.
type WALArchive struct {
	// GSAName is the Google service account impersonated through Workload Identity.
	GSAName string
	// TeamProjectID is the Google project owning the service account.
	TeamProjectID string
	// BucketName is the barman-cloud ObjectStore (and bucket) name.
	BucketName string
}

// Enabled reports whether WAL archiving should be wired into the cluster.
func (w WALArchive) Enabled() bool {
	return w.BucketName != ""
}

// CreateCluster builds the CNPG Cluster for a Postgres resource. Authentication
// is certificate-based (hostssl ... cert); the durable app owner is created by
// InitDB with superuser access disabled, and bootstrap SQL pre-creates the
// app_read/app_readwrite group roles and their default privileges.
func CreateCluster(scheme *runtime.Scheme, postgres *v1.Postgres, cfg *config.Config, wal WALArchive) (*cnpgv1.Cluster, error) {
	instances := defaultInstances
	minSync, maxSync := 0, 0
	if postgres.Spec.HighAvailability {
		instances = haInstances
		minSync, maxSync = 1, 1
	}

	majorVersion, err := strconv.Atoi(postgres.Spec.MajorVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid major version %q: %w", postgres.Spec.MajorVersion, err)
	}

	diskSize, err := enforceMinimumDisk(postgres.Spec.Resources.DiskSize, cfg.CNPG.StorageClass)
	if err != nil {
		return nil, err
	}

	var storageClass *string
	if cfg.CNPG.StorageClass != "" {
		storageClass = ptr.To(cfg.CNPG.StorageClass)
	}

	memory := postgres.Spec.Resources.Memory
	memLimit := memory.DeepCopy()
	memLimit.Set(memLimit.Value() * memoryLimitFactor)

	cluster := &cnpgv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       cnpgv1.ClusterKind,
			APIVersion: cnpgv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: objectMeta(postgres, ClusterName(postgres)),
		Spec: cnpgv1.ClusterSpec{
			Instances:       instances,
			MinSyncReplicas: minSync,
			MaxSyncReplicas: maxSync,

			ImageCatalogRef: &cnpgv1.ImageCatalogRef{
				TypedLocalObjectReference: corev1.TypedLocalObjectReference{
					APIGroup: ptr.To("postgresql.cnpg.io"),
					Kind:     "ClusterImageCatalog",
					Name:     cfg.CNPG.ImageCatalogName,
				},
				Major: majorVersion,
			},

			PostgresConfiguration: cnpgv1.PostgresConfiguration{
				Parameters: makePostgresParameters(memory),
				Extensions: makeExtensions(postgres.Spec.Extensions),

				// Rules are evaluated top to bottom, first match wins.
				PgHBA: []string{
					// PgBouncer terminates TLS, so a client certificate cannot be
					// forwarded: proving ownership requires the private key, which
					// never leaves the client. PgBouncer therefore authenticates
					// the client itself (CN must equal the requested role) and then
					// opens its own connection as that role, presenting its own
					// certificate. This rule lets it do so via the pgIdentMap below.
					//
					// Restricted to the pooler pod IPs, so possession of the pooler
					// certificate alone is not enough to impersonate a role.
					fmt.Sprintf("hostssl all all ${podselector:%s} cert map=%s", poolerSelectorName, pgIdentMap),

					// Certificate authentication for all other clients. Without a
					// map, PostgreSQL requires the certificate CN to equal the role,
					// so clients connecting directly prove their own identity.
					"hostssl all all all cert",

					// CloudNativePG unconditionally appends "host all all all
					// scram-sha-256" as the final rule, and "host" matches non-TLS
					// connections too. Without this rule any client could bypass
					// certificate authentication by connecting with sslmode=disable.
					"hostnossl all all all reject",
				},

				// "all" lets the pooler connect as any role, so no entry is needed
				// per role and bindings never have to mutate this shared Cluster.
				// The pooler still cannot be reached as an arbitrary role: its own
				// pg_hba requires the client certificate CN to equal the requested
				// role, and certificates are only issued through DatabaseRole.
				PgIdent: []string{
					fmt.Sprintf("%s %s all", pgIdentMap, poolerAuthUser),
				},
			},

			// Resolved by the operator into the current pooler pod IPs and kept
			// up to date as pods are rescheduled.
			PodSelectorRefs: []cnpgv1.PodSelectorRef{
				{
					Name: poolerSelectorName,
					Selector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							PoolerNameLabel: PoolerName(postgres),
						},
					},
				},
			},

			Certificates: &cnpgv1.CertificatesConfiguration{
				// The pooler fronts the cluster under its own service name, so the
				// server certificate must cover it or clients using
				// sslmode=verify-full are rejected on hostname mismatch.
				ServerAltDNSNames: poolerAltDNSNames(postgres),
			},

			Bootstrap: &cnpgv1.BootstrapConfiguration{
				InitDB: &cnpgv1.BootstrapInitDB{
					Database:               DatabaseName,
					Owner:                  OwnerRole,
					PostInitSQL:            postInitSQL(),
					PostInitApplicationSQL: postInitApplicationSQL(),
				},
			},

			StorageConfiguration: cnpgv1.StorageConfiguration{
				StorageClass: storageClass,
				Size:         diskSize.String(),
			},

			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    postgres.Spec.Resources.Cpu,
					corev1.ResourceMemory: memory,
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: memLimit,
				},
			},

			Affinity: cnpgv1.AffinityConfiguration{
				NodeSelector: map[string]string{
					"cloud.google.com/compute-class": computeClass,
				},
				EnablePodAntiAffinity: ptr.To(true),
				TopologyKey:           "kubernetes.io/hostname",
				Tolerations:           []corev1.Toleration{dedicatedPostgresToleration},
			},

			EnableSuperuserAccess: ptr.To(false),
		},
	}

	if wal.Enabled() {
		// Bind the instance pods' service account to the Google service account so
		// barman-cloud can reach the bucket with a metadata-server OAuth token.
		cluster.Spec.ServiceAccountTemplate = &cnpgv1.ServiceAccountTemplate{
			Metadata: cnpgv1.Metadata{
				Annotations: map[string]string{
					workloadIdentityAnnotation: fmt.Sprintf("%s@%s.iam.gserviceaccount.com", wal.GSAName, wal.TeamProjectID),
				},
			},
		}

		cluster.Spec.Plugins = []cnpgv1.PluginConfiguration{
			{
				Name:          BarmanPluginName,
				Enabled:       ptr.To(true),
				IsWALArchiver: ptr.To(true),
				Parameters: map[string]string{
					"barmanObjectName": wal.BucketName,
				},
			},
		}
	}

	if err := controllerutil.SetControllerReference(postgres, cluster, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on Cluster: %w", err)
	}
	return cluster, nil
}

// CreateScheduledBackup schedules a nightly base backup taken from a standby, so
// the primary is left alone. Backups go through the barman-cloud plugin to the
// same ObjectStore as the WAL archive.
func CreateScheduledBackup(scheme *runtime.Scheme, postgres *v1.Postgres) (*cnpgv1.ScheduledBackup, error) {
	backup := &cnpgv1.ScheduledBackup{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ScheduledBackup",
			APIVersion: cnpgv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: objectMeta(postgres, ClusterName(postgres)),
		Spec: cnpgv1.ScheduledBackupSpec{
			// Daily at 02:00.
			Schedule:             "0 0 2 * * *",
			Cluster:              cnpgv1.LocalObjectReference{Name: ClusterName(postgres)},
			BackupOwnerReference: "cluster",
			Target:               cnpgv1.BackupTargetStandby,
			Method:               cnpgv1.BackupMethodPlugin,
			PluginConfiguration: &cnpgv1.BackupPluginConfiguration{
				Name: BarmanPluginName,
			},
		},
	}

	if err := controllerutil.SetControllerReference(postgres, backup, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on ScheduledBackup: %w", err)
	}
	return backup, nil
}

// CreatePooler builds a PgBouncer connection Pooler for the cluster's primary
// (read-write). Pooling uses transaction mode; authentication to Postgres remains
// certificate-based (OAUTHBEARER is not used), so no OAuth pass-through is needed.
func CreatePooler(scheme *runtime.Scheme, postgres *v1.Postgres) (*cnpgv1.Pooler, error) {
	pooler := &cnpgv1.Pooler{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pooler",
			APIVersion: cnpgv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: objectMeta(postgres, PoolerName(postgres)),
		Spec: cnpgv1.PoolerSpec{
			Cluster:   cnpgv1.LocalObjectReference{Name: ClusterName(postgres)},
			Type:      cnpgv1.PoolerTypeRW,
			Instances: ptr.To(poolerInstances),
			Template: &cnpgv1.PodTemplateSpec{
				ObjectMeta: cnpgv1.Metadata{
					Labels: map[string]string{"apiserver-access": "enabled"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							// The container name must be "pgbouncer".
							Name: "pgbouncer",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("100Mi"),
								},
							},
						},
					},
					NodeSelector: map[string]string{
						"cloud.google.com/compute-class": computeClass,
					},
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
								{
									LabelSelector: &metav1.LabelSelector{
										MatchExpressions: []metav1.LabelSelectorRequirement{
											{
												Key:      "cnpg.io/podRole",
												Operator: metav1.LabelSelectorOpIn,
												Values:   []string{"pooler"},
											},
										},
									},
									TopologyKey: "kubernetes.io/hostname",
								},
							},
						},
					},
					Tolerations: []corev1.Toleration{dedicatedPostgresToleration},
				},
			},
			PgBouncer: &cnpgv1.PgBouncerSpec{
				PoolMode: cnpgv1.PgBouncerPoolModeTransaction,
				// PgBouncer terminates the client connection and authenticates the
				// client itself, so it needs its own cert rule; without this it
				// falls back to asking for a password.
				PgHBA: []string{
					"hostssl all all all cert",
				},
				Parameters: map[string]string{
					// Required for client certificate validation; CNPG defaults to
					// "prefer", which does not request a client certificate.
					"client_tls_sslmode": "verify-ca",
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(postgres, pooler, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on Pooler: %w", err)
	}
	return pooler, nil
}

func makeExtensions(extensions []v1.PostgresExtension) []cnpgv1.ExtensionConfiguration {
	res := make([]cnpgv1.ExtensionConfiguration, 0, len(extensions))
	for _, e := range extensions {
		res = append(res, cnpgv1.ExtensionConfiguration{Name: e.Name})
	}
	return res
}

// postInitSQL creates the NOLOGIN group roles globally, before the application
// database bootstrap grants privileges to them (avoids operator ordering races).
// postInitSQL runs exactly once at initdb on a fresh cluster, so the roles cannot
// exist yet and a plain CREATE ROLE is safe (no IF NOT EXISTS / DO block needed;
// CNPG runs each entry as a single statement and dollar-quoted blocks with inner
// semicolons break it).
func postInitSQL() []string {
	return []string{
		fmt.Sprintf("CREATE ROLE %s NOLOGIN", ReadRole),
		fmt.Sprintf("CREATE ROLE %s NOLOGIN", ReadWriteRole),
	}
}

// postInitApplicationSQL runs as superuser on the application database. It wires
// the group roles' object-level privileges and sets schema-less default
// privileges for objects the app owner creates later (in any schema).
func postInitApplicationSQL() []string {
	both := ReadRole + ", " + ReadWriteRole
	return []string{
		fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s;", DatabaseName, both),
		fmt.Sprintf("GRANT USAGE ON SCHEMA public TO %s;", both),
		fmt.Sprintf("GRANT SELECT ON ALL TABLES IN SCHEMA public TO %s;", ReadRole),
		fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s;", ReadWriteRole),
		fmt.Sprintf("GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO %s;", both),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s GRANT SELECT ON TABLES TO %s;", OwnerRole, ReadRole),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s;", OwnerRole, ReadWriteRole),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s GRANT USAGE, SELECT ON SEQUENCES TO %s;", OwnerRole, both),
	}
}

func makePostgresParameters(memory resource.Quantity) map[string]string {
	memBytes := memory.Value()

	sharedBuffers := memBytes / sharedBuffersFraction
	effectiveCacheSize := memBytes * 3 / effectiveCacheSizeFraction
	workMem := memBytes / workMemFraction
	maintenanceWorkMem := memBytes / maintenanceWorkMemFraction
	if maintenanceWorkMem > maxMaintenanceWorkMemBytes {
		maintenanceWorkMem = maxMaintenanceWorkMemBytes
	}

	mb := func(b int64) string { return fmt.Sprintf("%dMB", b/(1024*1024)) }

	return map[string]string{
		"log_min_duration_statement": "1000",
		"shared_buffers":             mb(sharedBuffers),
		"effective_cache_size":       mb(effectiveCacheSize),
		"work_mem":                   mb(workMem),
		"maintenance_work_mem":       mb(maintenanceWorkMem),
		"random_page_cost":           "1.1",
		"effective_io_concurrency":   "200",
		"huge_pages":                 "off",
		"track_io_timing":            "on",
		"commit_delay":               "100",
		"commit_siblings":            "10",
		"max_wal_size":               "2GB",
		"wal_compression":            "zstd",
		"checkpoint_timeout":         "10min",
		"bgwriter_lru_maxpages":      "200",

		// CNPG auto-loads the matching shared_preload_libraries when it sees
		// these prefixed parameters. Audit is always on with sane defaults.
		"pg_stat_statements.track": "all",
		"pgaudit.log":              strings.Join([]string{"write", "ddl", "role"}, ","),
	}
}

// poolerAltDNSNames returns the DNS names the pooler service is reachable by,
// so they can be included in the cluster's server certificate.
func poolerAltDNSNames(postgres *v1.Postgres) []string {
	name := PoolerName(postgres)
	ns := postgres.GetNamespace()
	return []string{
		name,
		fmt.Sprintf("%s.%s", name, ns),
		fmt.Sprintf("%s.%s.svc", name, ns),
		fmt.Sprintf("%s.%s.svc.cluster.local", name, ns),
	}
}
