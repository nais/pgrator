package zalando

import (
	"fmt"
	"time"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/resourcecreator"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	cpuLimitFactor = int64(10)

	maintenanceDuration = 1

	allowDeletionAnnotation   = "nais.io/postgresqlDeleteResource"
	resourceRemoverAnnotation = "resource-remover.nais.io/skip"

	defaultNumInstances = int32(2)
	haNumInstances      = int32(3)

	defaultSchema = "public"

	defaultDatabaseName = "app"

	sharedPreloadLibraries = "bg_mon,pg_stat_statements,pgextwlist,pg_auth_mon,set_user,timescaledb,pg_cron,pg_stat_kcache,pgaudit"

	runAsUser  = int64(101)
	runAsGroup = int64(103)
	fsGroup    = int64(103)
)

var defaultExtensions = []string{
	"pgaudit",
}

func MinimalCluster(postgres *data_nais_io_v1.Postgres, pgClusterName string, pgNamespace string) *acid_zalan_do_v1.Postgresql {
	objectMeta := resourcecreator.CreateObjectMeta(postgres)
	objectMeta.Name = pgClusterName
	objectMeta.Namespace = pgNamespace
	objectMeta.Labels["apiserver-access"] = "enabled"

	if postgres.Spec.Cluster.AllowDeletion {
		metav1.SetMetaDataAnnotation(&objectMeta, allowDeletionAnnotation, pgClusterName)
	}
	metav1.SetMetaDataAnnotation(&objectMeta, resourceRemoverAnnotation, "true")

	return &acid_zalan_do_v1.Postgresql{
		TypeMeta: metav1.TypeMeta{
			Kind:       "postgresql",
			APIVersion: "acid.zalan.do/v1",
		},
		ObjectMeta: objectMeta,
	}
}

func CreateClusterSpec(postgres *data_nais_io_v1.Postgres, cfg *config.Config, pgClusterName string, pgNamespace string) (*acid_zalan_do_v1.Postgresql, error) {
	cluster := MinimalCluster(postgres, pgClusterName, pgNamespace)

	cpuLimit := postgres.Spec.Cluster.Resources.Cpu.DeepCopy()
	cpuLimit.Mul(cpuLimitFactor)

	memoryLimit := postgres.Spec.Cluster.Resources.Memory.DeepCopy()
	memoryLimit.Mul(resourcecreator.MemoryLimitFactor)

	numberOfInstances := defaultNumInstances
	if postgres.Spec.Cluster.HighAvailability {
		numberOfInstances = haNumInstances
	}

	var maintenanceWindows []acid_zalan_do_v1.MaintenanceWindow
	if postgres.Spec.MaintenanceWindow != nil && postgres.Spec.MaintenanceWindow.Day != 0 && postgres.Spec.MaintenanceWindow.Hour != nil {
		startTime := time.Hour * time.Duration(*postgres.Spec.MaintenanceWindow.Hour)

		maintenanceStartTime := metav1.NewTime(time.Time{}.Add(startTime))
		maintenanceEndTime := metav1.NewTime(maintenanceStartTime.Add(maintenanceDuration * time.Hour))

		maintenanceWindows = append(maintenanceWindows, acid_zalan_do_v1.MaintenanceWindow{
			Everyday:  postgres.Spec.MaintenanceWindow.Day == 0,
			Weekday:   makeWeekday(postgres),
			StartTime: maintenanceStartTime,
			EndTime:   maintenanceEndTime,
		})
	}

	extensions := map[string]string{}
	if postgres.Spec.Database != nil && postgres.Spec.Database.Extensions != nil {
		for _, extension := range postgres.Spec.Database.Extensions {
			extensions[extension.Name] = defaultSchema
		}
	}
	for _, extension := range defaultExtensions {
		extensions[extension] = defaultSchema
	}

	collation := "en_US.UTF-8"
	if postgres.Spec.Database != nil && postgres.Spec.Database.Collation != "" {
		collation = fmt.Sprintf("%s.UTF-8", postgres.Spec.Database.Collation)
	}

	diskSize, err := resourcecreator.EnforceMinimumDisk(postgres.Spec.Cluster.Resources.DiskSize, cfg.PostgresStorageClass)
	if err != nil {
		return nil, err
	}

	cluster.Spec = acid_zalan_do_v1.PostgresSpec{
		EnableConnectionPooler:        ptr.To(true),
		EnableReplicaConnectionPooler: ptr.To(false),
		ConnectionPooler: &acid_zalan_do_v1.ConnectionPooler{
			Resources: &acid_zalan_do_v1.Resources{
				ResourceRequests: acid_zalan_do_v1.ResourceDescription{
					CPU:    ptr.To("50m"),
					Memory: ptr.To("50Mi"),
				},
			},
		},
		NodeAffinity: &v1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
				NodeSelectorTerms: []v1.NodeSelectorTerm{
					{
						MatchExpressions: []v1.NodeSelectorRequirement{
							{
								Key:      "nais.io/type",
								Operator: "In",
								Values:   []string{"postgres"},
							},
						},
					},
				},
			},
		},
		PostgresqlParam: acid_zalan_do_v1.PostgresqlParam{
			PgVersion:  postgres.Spec.Cluster.MajorVersion,
			Parameters: makePostgresParameters(postgres.Spec.Cluster.Audit),
		},
		Volume: acid_zalan_do_v1.Volume{
			Size:         diskSize.String(),
			StorageClass: cfg.PostgresStorageClass,
		},
		Patroni: acid_zalan_do_v1.Patroni{
			InitDB: map[string]string{
				"encoding": "UTF8",
				"locale":   collation,
			},
			SynchronousMode:       true,
			SynchronousModeStrict: true,
			PgHba: []string{
				// Implicitly trust unix-socket connections from inside the pod
				"local     all           all                        trust",
				// Only members of zalandos (human users) should be allowed from localhost (aka kubectl port-forward)
				"hostssl   all           +zalandos  127.0.0.1/32    pam",
				"host      all           all        127.0.0.1/32    reject",
				"hostssl   all           +zalandos  ::1/128         pam",
				"host      all           all        ::1/128         reject",
				// Replication can use unix-socket or SSL connection
				"local     replication   standby                    trust",
				"hostssl   replication   standby    all             md5",
				// Reject any connection not using SSL
				"hostnossl all           all        all             reject",
				// Reject human users connecting from elsewhere
				"hostssl   all           +zalandos  all             reject",
				// Accept SSL connections from anywhere not previously rejected
				"hostssl   all           all        all             md5",
			},
		},
		Resources: &acid_zalan_do_v1.Resources{
			ResourceRequests: acid_zalan_do_v1.ResourceDescription{
				CPU:    ptr.To(postgres.Spec.Cluster.Resources.Cpu.String()),
				Memory: ptr.To(postgres.Spec.Cluster.Resources.Memory.String()),
			},
			ResourceLimits: acid_zalan_do_v1.ResourceDescription{
				CPU:    ptr.To(cpuLimit.String()),
				Memory: ptr.To(memoryLimit.String()),
			},
		},
		TeamID:             postgres.GetNamespace(),
		DockerImage:        cfg.PostgresImage,
		NumberOfInstances:  numberOfInstances,
		MaintenanceWindows: maintenanceWindows,
		PreparedDatabases: map[string]acid_zalan_do_v1.PreparedDatabase{
			defaultDatabaseName: {
				DefaultUsers:    true,
				Extensions:      extensions,
				SecretNamespace: postgres.GetNamespace(),
				PreparedSchemas: map[string]acid_zalan_do_v1.PreparedSchema{
					defaultSchema: {
						DefaultRoles: ptr.To(false),
						DefaultUsers: false,
					},
				},
			},
		},
		SpiloRunAsUser:  ptr.To(runAsUser),
		SpiloRunAsGroup: ptr.To(runAsGroup),
		SpiloFSGroup:    ptr.To(fsGroup),
	}

	return cluster, nil
}

func makePostgresParameters(audit *data_nais_io_v1.PostgresAudit) map[string]string {
	postgresParameters := map[string]string{
		"log_destination":          "jsonlog",
		"shared_preload_libraries": sharedPreloadLibraries,
		"pg_stat_statements.track": "all",
		"track_io_timing":          "on",
	}
	if audit != nil && audit.Enabled {
		classes := ""
		for _, statementClass := range audit.StatementClasses {
			if classes != "" {
				classes += ","
			}
			classes += string(statementClass)
		}
		postgresParameters["pgaudit.log"] = classes
		postgresParameters["pgaudit.log_parameter"] = "on"
	}
	return postgresParameters
}

// makeWeekday creates a weekday from an integer day
// Weekday is Sun 0-6 Sat, while Day is Mon 1-7 Sun
func makeWeekday(postgres *data_nais_io_v1.Postgres) time.Weekday {
	if postgres.Spec.MaintenanceWindow == nil {
		return time.Tuesday
	}
	return time.Weekday(postgres.Spec.MaintenanceWindow.Day % 7)
}
