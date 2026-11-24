// Package drbackup builds the Kubernetes and Config Connector resources for
// team disaster-recovery buckets and backup copy/cleanup Jobs.
package drbackup

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/nais/pgrator/internal/backupcopy"
	"github.com/nais/pgrator/internal/namegen"
	fqdnv1alpha3 "github.com/nais/pgrator/internal/thirdparty/google/networking/v1alpha3"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// WorkerResourceName is the shared Kubernetes ServiceAccount, NetworkPolicy,
	// and FQDNNetworkPolicy name in a team namespace for DR copy/cleanup Jobs.
	WorkerResourceName = "pg-dr-backup-worker"

	// WorkerComponentLabel selects DR backup worker Job pods.
	WorkerComponentLabel = "app.kubernetes.io/component"
	WorkerComponentValue = "postgres-backup-worker"

	// BackupNameLabel identifies the PostgresBackup a Job belongs to.
	BackupNameLabel = "postgresbackup.nais.io/name"
	// JobRoleLabel distinguishes copy vs cleanup Jobs.
	JobRoleLabel       = "postgresbackup.nais.io/role"
	JobRoleCopy        = "copy"
	JobRoleCleanup     = "cleanup"
	ManagedByLabel     = "app.kubernetes.io/managed-by"
	ManagedByValue     = "pgrator"
	workloadIDAnnotKey = "iam.gke.io/gcp-service-account"
	forceDestroyAnnot  = "cnrm.cloud.google.com/force-destroy"

	bucketMaxLength        = 63
	bucketHashSuffixLength = 12
	gsaMaxLength           = 30
	defaultWorkerImage     = "europe-north1-docker.pkg.dev/nais-io/nais/images/pgrator:latest"
)

// DRBucketName returns the deterministic team DR bucket name
// (<drPrefix>-<team>-<hash>, e.g. nais-dr-<tenant>-<env>-<team>-<hash>) for a
// namespace and configured prefix. Empty when prefix is empty.
func DRBucketName(prefix, namespace string) string {
	trimmed := strings.Trim(prefix, "-")
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "nais-wal-") {
		trimmed = "nais-dr-" + strings.TrimPrefix(trimmed, "nais-wal-")
	}
	sum := sha256.Sum256([]byte(trimmed + "/" + namespace))
	suffix := fmt.Sprintf("%x", sum[:bucketHashSuffixLength/2])
	maxBase := bucketMaxLength - len(suffix) - 1
	if len(trimmed) > maxBase-2 {
		trimmed = trimmed[:maxBase-2]
	}
	nsMax := maxBase - len(trimmed) - 1
	ns := namespace
	if len(ns) > nsMax {
		ns = strings.TrimRight(ns[:nsMax], "-")
	}
	return fmt.Sprintf("%s-%s-%s", trimmed, ns, suffix)
}

// GSAName returns the Google Service Account name for the team's DR worker.
func GSAName(namespace string) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-dr-%s", namespace), gsaMaxLength)
}

// WorkloadIdentityPolicyName returns the IAMPolicyMember name binding the
// namespace's DR worker KSA to its GSA.
func WorkloadIdentityPolicyName(namespace string) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-dr-wi-%s", namespace), validation.DNS1123SubdomainMaxLength)
}

// BucketObjectUserPolicyName returns the IAMPolicyMember name granting the DR
// worker GSA objectUser on the team DR bucket.
func BucketObjectUserPolicyName(namespace string) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-dr-bucket-%s", namespace), validation.DNS1123SubdomainMaxLength)
}

// BucketViewerPolicyName returns the IAMPolicyMember name granting the DR
// worker GSA bucketViewer on the team DR bucket.
func BucketViewerPolicyName(namespace string) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-dr-viewer-%s", namespace), validation.DNS1123SubdomainMaxLength)
}

// SourceBucketPolicyName returns the temporary IAMPolicyMember name granting
// the DR worker GSA objectViewer on a source database's WAL bucket while a
// PostgresBackup is copying from it.
func SourceBucketPolicyName(backupName string) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-dr-src-%s", backupName), validation.DNS1123SubdomainMaxLength)
}

// CopyJobName returns the deterministic Job name for copying a PostgresBackup.
func CopyJobName(backupName string) string {
	return namegen.MustShortenName(fmt.Sprintf("%s-copy", backupName), validation.DNS1123LabelMaxLength)
}

// CleanupJobName returns the deterministic Job name for deleting a PostgresBackup's
// prefix from the DR bucket.
func CleanupJobName(backupName string) string {
	return namegen.MustShortenName(fmt.Sprintf("%s-cleanup", backupName), validation.DNS1123LabelMaxLength)
}

// CreateDRStorageBucket builds the shared team DR bucket in namespace.
// Pgrator owns expiry and deletion via PostgresBackup finalizers, so the
// bucket has an explicit empty LifecycleRules list.
func CreateDRStorageBucket(namespace, bucketName, location string) *storagecnrm.StorageBucket {
	return &storagecnrm.StorageBucket{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageBucket",
			APIVersion: storagecnrm.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      bucketName,
			Namespace: namespace,
			Annotations: map[string]string{
				forceDestroyAnnot: "true",
			},
			Labels: map[string]string{
				ManagedByLabel: ManagedByValue,
			},
		},
		Spec: storagecnrm.StorageBucketSpec{
			Location:                 location,
			UniformBucketLevelAccess: true,
			PublicAccessPrevention:   storagecnrm.PublicAccessPreventionInherited,
			LifecycleRules:           []storagecnrm.StorageBucketLifecycleRule{},
		},
	}
}

// CreateWorkerServiceAccount builds the Kubernetes ServiceAccount bound via
// Workload Identity to the team's DR Google Service Account.
func CreateWorkerServiceAccount(namespace, gsaName, teamProjectID string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      WorkerResourceName,
			Namespace: namespace,
			Annotations: map[string]string{
				workloadIDAnnotKey: fmt.Sprintf("%s@%s.iam.gserviceaccount.com", gsaName, teamProjectID),
			},
			Labels: map[string]string{
				ManagedByLabel: ManagedByValue,
			},
		},
	}
}

// CreateWorkerNetworkPolicy allows DNS egress for DR backup worker Job pods.
func CreateWorkerNetworkPolicy(namespace string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      WorkerResourceName,
			Namespace: namespace,
			Labels: map[string]string{
				ManagedByLabel: ManagedByValue,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					WorkerComponentLabel: WorkerComponentValue,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{},
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"k8s-app": "kube-dns"},
							},
						},
					},
				},
			},
		},
	}
}

// CreateWorkerFQDNNetworkPolicy allows DR backup worker Job pods to reach the
// GKE metadata server and Google Cloud Storage.
func CreateWorkerFQDNNetworkPolicy(namespace string) *fqdnv1alpha3.FQDNNetworkPolicy {
	return &fqdnv1alpha3.FQDNNetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "FQDNNetworkPolicy",
			APIVersion: fqdnv1alpha3.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      WorkerResourceName,
			Namespace: namespace,
			Labels: map[string]string{
				ManagedByLabel: ManagedByValue,
			},
		},
		Spec: fqdnv1alpha3.FQDNNetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					WorkerComponentLabel: WorkerComponentValue,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []fqdnv1alpha3.FQDNNetworkPolicyEgressRule{
				fqdnEgressRule("metadata.google.internal", 80),
				fqdnEgressRule("private.googleapis.com", 443),
				fqdnEgressRule("storage.googleapis.com", 443),
			},
		},
	}
}

func fqdnEgressRule(host string, port int32) fqdnv1alpha3.FQDNNetworkPolicyEgressRule {
	return fqdnv1alpha3.FQDNNetworkPolicyEgressRule{
		Ports: []networkingv1.NetworkPolicyPort{{
			Protocol: new(corev1.ProtocolTCP),
			Port:     new(intstr.FromInt32(port)),
		}},
		To: []fqdnv1alpha3.FQDNNetworkPolicyPeer{{FQDNs: []string{host}}},
	}
}

// CreateCopyJob builds the Kubernetes Job that copies a completed CNPG Backup
// from sourceBucket to an isolated prefix in drBucket and verifies checksums.
func CreateCopyJob(scheme *runtime.Scheme, backup *v1.PostgresBackup, image, sourceBucket, drBucket string) (*batchv1.Job, error) {
	if image == "" {
		image = defaultWorkerImage
	}
	status := backup.Status
	if status == nil {
		status = &v1.PostgresBackupStatus{}
	}
	extNames := make([]string, 0, len(status.Extensions))
	for _, e := range status.Extensions {
		extNames = append(extNames, e.Name)
	}
	var diskSize string
	if status.DiskSize != nil {
		diskSize = status.DiskSize.String()
	}

	args := []string{
		"backup-copy",
		"--source-bucket=" + sourceBucket,
		"--dr-bucket=" + drBucket,
		"--backup-name=" + backup.Name,
		"--server-name=" + status.ServerName,
		"--backup-id=" + status.BackupID,
		"--begin-wal=" + status.BeginWal,
		"--end-wal=" + status.EndWal,
		"--source-uid=" + status.SourceUID,
		"--major-version=" + status.MajorVersion,
		"--extensions=" + strings.Join(extNames, ","),
		"--disk-size=" + diskSize,
	}

	job := buildWorkerJob(backup, CopyJobName(backup.Name), JobRoleCopy, image, args)
	if err := controllerutil.SetControllerReference(backup, job, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on copy Job: %w", err)
	}
	return job, nil
}

// CreateCleanupJob builds the Kubernetes Job that deletes a PostgresBackup's
// prefix from drBucket when the backup expires or is deleted.
func CreateCleanupJob(scheme *runtime.Scheme, backup *v1.PostgresBackup, image, drBucket, prefix string) (*batchv1.Job, error) {
	if image == "" {
		image = defaultWorkerImage
	}
	if prefix == "" {
		prefix = backup.Name
	}
	args := []string{
		"backup-delete",
		"--dr-bucket=" + drBucket,
		"--backup-name=" + prefix,
	}
	job := buildWorkerJob(backup, CleanupJobName(backup.Name), JobRoleCleanup, image, args)
	if err := controllerutil.SetControllerReference(backup, job, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on cleanup Job: %w", err)
	}
	return job, nil
}

func buildWorkerJob(backup *v1.PostgresBackup, name, role, image string, args []string) *batchv1.Job {
	labels := map[string]string{
		WorkerComponentLabel:     WorkerComponentValue,
		BackupNameLabel:          backup.Name,
		JobRoleLabel:             role,
		"postgres.nais.io/name":  backup.Spec.Postgres,
		"logs.nais.io/flow-cnpg": "true",
	}
	backoffLimit := int32(3)
	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Job",
			APIVersion: "batch/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: backup.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           WorkerResourceName,
					AutomountServiceAccountToken: new(false),
					RestartPolicy:                corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: new(true),
						RunAsUser:    new(int64(65532)),
						RunAsGroup:   new(int64(65532)),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:                     "backup-worker",
							Image:                    image,
							Args:                     args,
							TerminationMessagePath:   backupcopy.DefaultTerminationMessagePath,
							TerminationMessagePolicy: corev1.TerminationMessageReadFile,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: new(false),
								ReadOnlyRootFilesystem:   new(true),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
					},
				},
			},
		},
	}
}
