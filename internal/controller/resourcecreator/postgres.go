package resourcecreator

import (
	"fmt"
	"time"

	acid_zalan_do_v1 "github.com/nais/liberator/pkg/apis/acid.zalan.do/v1"
	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	"github.com/nais/pgrator/internal/synchronizer/action"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	cpuLimitFactor = 4

	maintenanceDuration = 1

	allowDeletionAnnotation = "nais.io/postgresqlDeleteResource"

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

func CreateClusterSpec(postgres *data_nais_io_v1.Postgres, pgClusterName string, pgNamespace string) action.Action {
	objectMeta := CreateObjectMeta(postgres)
	objectMeta.Name = pgClusterName
	objectMeta.Namespace = pgNamespace
	objectMeta.Labels["apiserver-access"] = "enabled"

	if postgres.Spec.Cluster.AllowDeletion {
		objectMeta.Annotations[allowDeletionAnnotation] = pgClusterName
	}

	cpuLimit := postgres.Spec.Cluster.Resources.Cpu.DeepCopy()
	cpuLimit.Mul(cpuLimitFactor)

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

	cluster := &acid_zalan_do_v1.Postgresql{
		TypeMeta: metav1.TypeMeta{
			Kind:       "postgresql",
			APIVersion: "acid.zalan.do/v1",
		},
		ObjectMeta: objectMeta,
		Spec: acid_zalan_do_v1.PostgresSpec{
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
				Size:         postgres.Spec.Cluster.Resources.DiskSize.String(),
				StorageClass: "", // TODO: cfg.PostgresStorageClass(),
			},
			Patroni: acid_zalan_do_v1.Patroni{
				InitDB: map[string]string{
					"encoding": "UTF8",
					"locale":   collation,
				},
				SynchronousMode:       true,
				SynchronousModeStrict: true,
			},
			Resources: &acid_zalan_do_v1.Resources{
				ResourceRequests: acid_zalan_do_v1.ResourceDescription{
					CPU:    ptr.To(postgres.Spec.Cluster.Resources.Cpu.String()),
					Memory: ptr.To(postgres.Spec.Cluster.Resources.Memory.String()),
				},
				ResourceLimits: acid_zalan_do_v1.ResourceDescription{
					CPU:    ptr.To(cpuLimit.String()),
					Memory: ptr.To(postgres.Spec.Cluster.Resources.Memory.String()),
				},
			},
			TeamID:             postgres.GetNamespace(),
			DockerImage:        "", // TODO: cfg.PostgresImage(),
			NumberOfInstances:  numberOfInstances,
			MaintenanceWindows: maintenanceWindows,
			PreparedDatabases: map[string]acid_zalan_do_v1.PreparedDatabase{
				defaultDatabaseName: {
					DefaultUsers:    true,
					Extensions:      extensions,
					SecretNamespace: postgres.GetNamespace(),
					PreparedSchemas: map[string]acid_zalan_do_v1.PreparedSchema{
						defaultSchema: {},
					},
				},
			},
			SpiloRunAsUser:  ptr.To(runAsUser),
			SpiloRunAsGroup: ptr.To(runAsGroup),
			SpiloFSGroup:    ptr.To(fsGroup),
		},
	}

	return action.CreateOrUpdate(cluster)
}

func makePostgresParameters(audit *data_nais_io_v1.PostgresAudit) map[string]string {
	postgresParameters := map[string]string{
		"log_destination":          "jsonlog",
		"log_filename":             "postgresql.log",
		"shared_preload_libraries": sharedPreloadLibraries,
		"pg_stat_statements.track": "all",
		"track_io_timing":          "on",
	}
	if audit != nil && audit.Enabled {
		classes := ""
		if len(audit.StatementClasses) == 0 {
			classes = "write,ddl,role"
		}
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
