package cnpg

import (
	"fmt"
	"strconv"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/resourcecreator"
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

	computeClass = "n4-machines"
)

var dedicatedPostgresToleration = corev1.Toleration{
	Key:      "dedicated",
	Operator: "Equal",
	Value:    "postgres",
	Effect:   "NoSchedule",
}

func MinimalCluster(postgres *data_nais_io_v1.Postgres, clusterName, namespace string) *cnpgv1.Cluster {
	objectMeta := resourcecreator.CreateObjectMeta(postgres)
	objectMeta.Name = clusterName
	objectMeta.Namespace = namespace
	objectMeta.Labels["apiserver-access"] = "enabled"

	return &cnpgv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       cnpgv1.ClusterKind,
			APIVersion: cnpgv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: objectMeta,
	}
}

func CreateClusterSpec(postgres *data_nais_io_v1.Postgres, cfg *config.Config, clusterName, namespace string) (*cnpgv1.Cluster, error) {
	cluster := MinimalCluster(postgres, clusterName, namespace)

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

	diskSize, err := resourcecreator.EnforceMinimumDisk(postgres.Spec.Cluster.Resources.DiskSize, cfg.CNPG.StorageClass)
	if err != nil {
		return nil, err
	}

	var storageClass *string
	if cfg.CNPG.StorageClass != "" {
		storageClass = ptr.To(cfg.CNPG.StorageClass)
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
			Parameters: makePostgresParameters(postgres.Spec.Cluster.Audit, postgres.Spec.Cluster.Resources.Memory),
		},

		Bootstrap: &cnpgv1.BootstrapConfiguration{
			InitDB: &cnpgv1.BootstrapInitDB{
				Database: cnpgDatabaseName,
				Owner:    cnpgDatabaseUser,
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
					mem.Mul(resourcecreator.MemoryLimitFactor)
					return mem
				}(),
			},
		},

		Affinity: cnpgv1.AffinityConfiguration{
			NodeSelector: map[string]string{
				"cloud.google.com/compute-class": computeClass,
			},
			EnablePodAntiAffinity: ptr.To(true),
			TopologyKey:           "kubernetes.io/hostname",
			Tolerations: []corev1.Toleration{
				dedicatedPostgresToleration,
			},
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

func CreateScheduledBackup(postgres *data_nais_io_v1.Postgres, cfg *config.Config, clusterName, namespace string) *cnpgv1.ScheduledBackup {
	objectMeta := resourcecreator.CreateObjectMeta(postgres)
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

func MinimalScheduledBackup(postgres *data_nais_io_v1.Postgres, clusterName, namespace string) *cnpgv1.ScheduledBackup {
	objectMeta := resourcecreator.CreateObjectMeta(postgres)
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

func CreatePooler(postgres *data_nais_io_v1.Postgres, clusterName, namespace string) *cnpgv1.Pooler {
	objectMeta := resourcecreator.CreateObjectMeta(postgres)
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
			Template: &cnpgv1.PodTemplateSpec{
				ObjectMeta: cnpgv1.Metadata{
					Labels: map[string]string{
						"apiserver-access": "enabled",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "pgbouncer",
							Resources: corev1.ResourceRequirements{
								Limits: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceMemory: resource.MustParse("100Mi"),
								},
								Requests: map[corev1.ResourceName]resource.Quantity{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
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
					Tolerations: []corev1.Toleration{
						dedicatedPostgresToleration,
					},
				},
			},
			PgBouncer: &cnpgv1.PgBouncerSpec{
				PoolMode: cnpgv1.PgBouncerPoolModeTransaction,
			},
		},
	}
}

func MinimalPooler(postgres *data_nais_io_v1.Postgres, clusterName, namespace string) *cnpgv1.Pooler {
	objectMeta := resourcecreator.CreateObjectMeta(postgres)
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

func makePostgresParameters(audit *data_nais_io_v1.PostgresAudit, memory resource.Quantity) map[string]string {
	memBytes := memory.Value()

	// PostgreSQL tuning parameters based on available memory
	sharedBuffers := memBytes / sharedBuffersFraction
	effectiveCacheSize := memBytes * 3 / effectiveCacheSizeFraction
	workMem := memBytes / workMemFraction
	maintenanceWorkMem := memBytes / maintenanceWorkMemFraction
	if maintenanceWorkMem > maxMaintenanceWorkMemBytes {
		maintenanceWorkMem = maxMaintenanceWorkMemBytes
	}

	params := map[string]string{
		"log_min_duration_statement": "1000",
		"shared_buffers":             fmt.Sprintf("%dMB", sharedBuffers/(1024*1024)),
		"effective_cache_size":       fmt.Sprintf("%dMB", effectiveCacheSize/(1024*1024)),
		"work_mem":                   fmt.Sprintf("%dMB", workMem/(1024*1024)),
		"maintenance_work_mem":       fmt.Sprintf("%dMB", maintenanceWorkMem/(1024*1024)),
		"random_page_cost":           "1.1",
		"effective_io_concurrency":   "200",
		"huge_pages":                 "off",
		// CNPG auto-manages shared_preload_libraries when it detects these prefixed parameters.
		// Setting pg_stat_statements.track triggers loading of pg_stat_statements,
		// and pgaudit.log (set below) triggers loading of pgaudit.
		"pg_stat_statements.track": "all",
	}

	if audit != nil && audit.Enabled {
		if len(audit.StatementClasses) > 0 {
			classes := make([]string, 0, len(audit.StatementClasses))
			for _, c := range audit.StatementClasses {
				classes = append(classes, string(c))
			}
			params["pgaudit.log"] = strings.Join(classes, ",")
		} else {
			params["pgaudit.log"] = "write,ddl,role"
		}
	}

	return params
}
