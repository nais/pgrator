package resourcecreator

import (
	"fmt"
	"strconv"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/config"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	cnpgDefaultInstances = 2
	cnpgHAInstances      = 3

	cnpgDefaultPoolerInstances = int32(2)

	cnpgDatabaseName = "app"
	cnpgDatabaseUser = "app"

	cnpgKSAName = "postgres-pod"

	// PostgreSQL memory tuning ratios
	sharedBuffersFraction      = 4                      // 1/4 of memory (25%)
	effectiveCacheSizeFraction = 4                      // 3/4 of memory (75%), computed as mem*3/4
	workMemFraction            = 64                     // 1/64 of memory (~1.5%)
	maintenanceWorkMemFraction = 8                      // 1/8 of memory (12.5%)
	maxMaintenanceWorkMemBytes = 2 * 1024 * 1024 * 1024 // 2GB cap
)

func MinimalCNPGCluster(postgres *data_nais_io_v1.Postgres, clusterName, namespace string) *cnpgv1.Cluster {
	objectMeta := CreateObjectMeta(postgres)
	objectMeta.Name = clusterName
	objectMeta.Namespace = namespace

	return &cnpgv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       cnpgv1.ClusterKind,
			APIVersion: cnpgv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: objectMeta,
	}
}

func CreateCNPGClusterSpec(postgres *data_nais_io_v1.Postgres, cfg *config.Config, clusterName, namespace string) (*cnpgv1.Cluster, error) {
	cluster := MinimalCNPGCluster(postgres, clusterName, namespace)

	instances := cnpgDefaultInstances
	minSyncReplicas := 0
	maxSyncReplicas := 0
	if postgres.Spec.Cluster.HighAvailability {
		instances = cnpgHAInstances
		minSyncReplicas = 1
		maxSyncReplicas = 1
	}

	majorVersion, err := strconv.Atoi(postgres.Spec.Cluster.MajorVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid major version %q: %w", postgres.Spec.Cluster.MajorVersion, err)
	}

	diskSize := enforceMinimum2GiDisk(postgres.Spec.Cluster.Resources.DiskSize)

	var storageClass *string
	if cfg.CNPG.StorageClass != "" {
		storageClass = ptr.To(cfg.CNPG.StorageClass)
	}

	collation := "en_US.UTF-8"
	if postgres.Spec.Database != nil && postgres.Spec.Database.Collation != "" {
		collation = fmt.Sprintf("%s.UTF-8", postgres.Spec.Database.Collation)
	}

	cluster.Spec = cnpgv1.ClusterSpec{
		Instances:       instances,
		MinSyncReplicas: minSyncReplicas,
		MaxSyncReplicas: maxSyncReplicas,

		ImageCatalogRef: &cnpgv1.ImageCatalogRef{
			TypedLocalObjectReference: corev1.TypedLocalObjectReference{
				APIGroup: ptr.To("postgresql.cnpg.io"),
				Kind:     "ClusterImageCatalog",
				Name:     cfg.CNPG.ImageCatalogName,
			},
			Major: majorVersion,
		},

		PostgresConfiguration: cnpgv1.PostgresConfiguration{
			Parameters: makeCNPGPostgresParameters(postgres.Spec.Cluster),
		},

		Bootstrap: &cnpgv1.BootstrapConfiguration{
			InitDB: &cnpgv1.BootstrapInitDB{
				Database:      cnpgDatabaseName,
				Owner:         cnpgDatabaseUser,
				LocaleCollate: collation,
				LocaleCType:   collation,
				Encoding:      "UTF8",
			},
		},

		StorageConfiguration: cnpgv1.StorageConfiguration{
			StorageClass: storageClass,
			Size:         diskSize.String(),
		},

		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    postgres.Spec.Cluster.Resources.Cpu,
				corev1.ResourceMemory: postgres.Spec.Cluster.Resources.Memory,
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: func() resource.Quantity {
					mem := postgres.Spec.Cluster.Resources.Memory.DeepCopy()
					mem.Mul(memoryLimitFactor)
					return mem
				}(),
			},
		},

		Affinity: cnpgv1.AffinityConfiguration{
			NodeSelector: map[string]string{
				"nais.io/type": "postgres",
			},
			EnablePodAntiAffinity: ptr.To(true),
			TopologyKey:           "kubernetes.io/hostname",
		},

		// Reuse the existing KSA created by the IAM actions, which already has
		// workload identity bindings to the correct GCP service account.
		ServiceAccountName: cnpgKSAName,

		EnableSuperuserAccess: ptr.To(false),
	}

	// Configure barman-cloud plugin for WAL archiving and backups
	if cfg.CNPG.BackupBucket != "" {
		cluster.Spec.Plugins = []cnpgv1.PluginConfiguration{
			{
				Name:          cfg.CNPG.BarmanPluginName,
				Enabled:       ptr.To(true),
				IsWALArchiver: ptr.To(true),
				Parameters: map[string]string{
					"barmanObjectName": clusterName,
				},
			},
		}
	}

	return cluster, nil
}

func CreateCNPGScheduledBackup(postgres *data_nais_io_v1.Postgres, cfg *config.Config, clusterName, namespace string) *cnpgv1.ScheduledBackup {
	objectMeta := CreateObjectMeta(postgres)
	objectMeta.Name = clusterName
	objectMeta.Namespace = namespace

	backup := &cnpgv1.ScheduledBackup{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ScheduledBackup",
			APIVersion: cnpgv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: objectMeta,
		Spec: cnpgv1.ScheduledBackupSpec{
			// Daily at 02:00
			Schedule: "0 0 2 * * *",
			Cluster: cnpgv1.LocalObjectReference{
				Name: clusterName,
			},
			BackupOwnerReference: "cluster",
			Target:               cnpgv1.BackupTargetStandby,
			Method:               cnpgv1.BackupMethodPlugin,
			PluginConfiguration: &cnpgv1.BackupPluginConfiguration{
				Name: cfg.CNPG.BarmanPluginName,
			},
		},
	}

	return backup
}

func MinimalCNPGScheduledBackup(postgres *data_nais_io_v1.Postgres, clusterName, namespace string) *cnpgv1.ScheduledBackup {
	objectMeta := CreateObjectMeta(postgres)
	objectMeta.Name = clusterName
	objectMeta.Namespace = namespace

	return &cnpgv1.ScheduledBackup{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ScheduledBackup",
			APIVersion: cnpgv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: objectMeta,
	}
}

func CreateCNPGPooler(postgres *data_nais_io_v1.Postgres, clusterName, namespace string) *cnpgv1.Pooler {
	objectMeta := CreateObjectMeta(postgres)
	objectMeta.Name = fmt.Sprintf("%s-pooler", clusterName)
	objectMeta.Namespace = namespace

	return &cnpgv1.Pooler{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pooler",
			APIVersion: cnpgv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: objectMeta,
		Spec: cnpgv1.PoolerSpec{
			Cluster: cnpgv1.LocalObjectReference{
				Name: clusterName,
			},
			Type:      cnpgv1.PoolerTypeRW,
			Instances: ptr.To(cnpgDefaultPoolerInstances),
			PgBouncer: &cnpgv1.PgBouncerSpec{
				PoolMode: cnpgv1.PgBouncerPoolModeTransaction,
			},
		},
	}
}

func MinimalCNPGPooler(postgres *data_nais_io_v1.Postgres, clusterName, namespace string) *cnpgv1.Pooler {
	objectMeta := CreateObjectMeta(postgres)
	objectMeta.Name = fmt.Sprintf("%s-pooler", clusterName)
	objectMeta.Namespace = namespace

	return &cnpgv1.Pooler{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pooler",
			APIVersion: cnpgv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: objectMeta,
	}
}

func makeCNPGPostgresParameters(cluster data_nais_io_v1.PostgresCluster) map[string]string {
	memBytes := cluster.Resources.Memory.Value()
	diskBytes := enforceMinimum2GiDisk(cluster.Resources.DiskSize).Value()

	// PostgreSQL memory tuning parameters based on available memory
	sharedBuffers := memBytes / sharedBuffersFraction
	effectiveCacheSize := memBytes * 3 / effectiveCacheSizeFraction
	workMem := memBytes / workMemFraction
	maintenanceWorkMem := memBytes / maintenanceWorkMemFraction
	if maintenanceWorkMem > maxMaintenanceWorkMemBytes {
		maintenanceWorkMem = maxMaintenanceWorkMemBytes
	}

	// WAL sizing: ~2% of disk, clamped between 1GB and 8GB
	maxWalSize := diskBytes / 50
	if maxWalSize < 1*1024*1024*1024 {
		maxWalSize = 1 * 1024 * 1024 * 1024
	}
	if maxWalSize > 8*1024*1024*1024 {
		maxWalSize = 8 * 1024 * 1024 * 1024
	}

	// wal_buffers: 1/32 of shared_buffers, clamped to 64MB (PostgreSQL max useful)
	walBuffers := sharedBuffers / 32
	if walBuffers < 1*1024*1024 {
		walBuffers = 1 * 1024 * 1024
	}
	if walBuffers > 64*1024*1024 {
		walBuffers = 64 * 1024 * 1024
	}

	params := map[string]string{
		// Memory
		"shared_buffers":       fmt.Sprintf("%dMB", sharedBuffers/(1024*1024)),
		"effective_cache_size": fmt.Sprintf("%dMB", effectiveCacheSize/(1024*1024)),
		"work_mem":             fmt.Sprintf("%dMB", workMem/(1024*1024)),
		"maintenance_work_mem": fmt.Sprintf("%dMB", maintenanceWorkMem/(1024*1024)),
		"huge_pages":           "off",

		// WAL performance
		"max_wal_size":    fmt.Sprintf("%dMB", maxWalSize/(1024*1024)),
		"wal_compression": "zstd",
		"wal_buffers":     fmt.Sprintf("%dMB", walBuffers/(1024*1024)),

		// Checkpoint / background writer
		"checkpoint_timeout":    "10min",
		"bgwriter_lru_maxpages": "200",

		// I/O tuning for SSD
		"effective_io_concurrency": "200",
		"random_page_cost":         "1.1",

		// Monitoring
		"track_io_timing":            "on",
		"log_min_duration_statement": "1000",

		// CNPG auto-manages shared_preload_libraries when it detects these prefixed parameters.
		// Setting pg_stat_statements.track triggers loading of pg_stat_statements,
		// and pgaudit.log (set below) triggers loading of pgaudit.
		"pg_stat_statements.track": "all",
	}

	// Group commit reduces sync-rep round trips in HA clusters
	if cluster.HighAvailability {
		params["commit_delay"] = "100"
		params["commit_siblings"] = "10"
	}

	if cluster.Audit != nil && cluster.Audit.Enabled {
		classes := make([]string, 0, len(cluster.Audit.StatementClasses))
		for _, c := range cluster.Audit.StatementClasses {
			classes = append(classes, string(c))
		}
		params["pgaudit.log"] = strings.Join(classes, ",")
	} else {
		params["pgaudit.log"] = "ddl,role"
	}

	return params
}
