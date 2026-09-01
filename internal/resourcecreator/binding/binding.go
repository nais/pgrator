// Package binding builds the resources that give a workload access to a Postgres
// instance: a CloudNativePG DatabaseRole authenticated by a client certificate,
// the Secrets a workload needs in order to connect, and NetworkPolicies opening
// the path to the connection pooler.
package binding

import (
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	core_v1 "k8s.io/api/core/v1"
	networking_v1 "k8s.io/api/networking/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	nameLabel = "postgres.nais.io/binding"

	// appDatabase is the database created by InitDB at provisioning time.
	appDatabase = "app"
)

func objectMeta(b *v1.PostgresBinding, name string) meta_v1.ObjectMeta {
	var annotations map[string]string
	if b.GetCorrelationId() != "" {
		annotations = map[string]string{
			api.DeploymentCorrelationIDAnnotation: b.GetCorrelationId(),
		}
	}
	return meta_v1.ObjectMeta{
		Name:        name,
		Namespace:   b.GetNamespace(),
		Labels:      map[string]string{nameLabel: b.GetName()},
		Annotations: annotations,
	}
}

// CreateDatabaseRole builds the DatabaseRole for a read or readwrite binding.
//
// Admin bindings return nil: the owner role is created by the Postgres resource
// at provisioning time and must outlive any individual binding, so a binding must
// never claim ownership of it. An admin binding therefore only produces Secrets
// and NetworkPolicies, pointing at the already-existing owner credentials.
func CreateDatabaseRole(scheme *runtime.Scheme, b *v1.PostgresBinding) (*cnpgv1.DatabaseRole, error) {
	if b.Spec.Role == v1.PostgresBindingRoleAdmin {
		return nil, nil
	}

	role := &cnpgv1.DatabaseRole{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "DatabaseRole",
			APIVersion: cnpgv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: objectMeta(b, b.DatabaseRoleName()),
		Spec: cnpgv1.DatabaseRoleSpec{
			ClusterRef:    core_v1.LocalObjectReference{Name: b.Spec.Postgres},
			ReclaimPolicy: cnpgv1.DatabaseRoleReclaimDelete,
			RoleConfiguration: cnpgv1.RoleConfiguration{
				Name:    b.RoleName(),
				Login:   true,
				Comment: fmt.Sprintf("Managed by pgrator for workload %q", b.Spec.Workload.Name),
				InRoles: []string{groupRole(b.Spec.Role)},
			},
			ClientCertificate: &cnpgv1.ClientCertificateConfiguration{
				Enabled: ptr.To(true),
			},
		},
	}

	if err := controllerutil.SetControllerReference(b, role, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on DatabaseRole: %w", err)
	}
	return role, nil
}

// groupRole maps a binding role onto the NOLOGIN group role that cluster bootstrap
// created, and whose default privileges keep it current as new tables appear.
func groupRole(role v1.PostgresBindingRole) string {
	switch role {
	case v1.PostgresBindingRoleRead:
		return cnpg.ReadRole
	default:
		return cnpg.ReadWriteRole
	}
}

func workloadSelector(workload v1.PostgresBindingWorkload) meta_v1.LabelSelector {
	return meta_v1.LabelSelector{
		MatchLabels: map[string]string{"app": workload.Name},
	}
}

// CreateConfigSecret builds the Secret a workload consumes through envFrom.
//
// Connections go through PgBouncer rather than straight to the instance: the
// pooler authenticates the client's certificate and then maps it onto the
// requested role via pg_ident, so current_user and the audit log still show the
// workload's own role.
func CreateConfigSecret(scheme *runtime.Scheme, b *v1.PostgresBinding) (*core_v1.Secret, error) {
	secret := &core_v1.Secret{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: objectMeta(b, b.ConfigSecretName()),
		StringData: map[string]string{
			"PGHOST":        fmt.Sprintf("%s.%s", cnpg.PoolerNameFor(b.Spec.Postgres), b.GetNamespace()),
			"PGPORT":        "5432",
			"PGDATABASE":    appDatabase,
			"PGUSER":        b.RoleName(),
			"PGSSLMODE":     "verify-full",
			"PGSSLCERT":     fmt.Sprintf("%s/tls.crt", v1.ClientCertMountPath),
			"PGSSLKEY":      fmt.Sprintf("%s/tls.key", v1.ClientCertMountPath),
			"PGSSLROOTCERT": fmt.Sprintf("%s/ca.crt", v1.CAMountPath),
		},
	}

	if err := controllerutil.SetControllerReference(b, secret, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on config Secret: %w", err)
	}
	return secret, nil
}

// CreateCASecret copies the cluster CA certificate into a Secret of its own.
//
// The CloudNativePG CA Secret cannot be mounted directly: it also contains ca.key,
// and a workload holding the CA private key could mint a client certificate for
// any role in the cluster.
func CreateCASecret(scheme *runtime.Scheme, b *v1.PostgresBinding, caCert []byte) (*core_v1.Secret, error) {
	secret := &core_v1.Secret{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: objectMeta(b, b.CASecretName()),
		Data: map[string][]byte{
			"ca.crt": caCert,
		},
	}

	if err := controllerutil.SetControllerReference(b, secret, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on CA Secret: %w", err)
	}
	return secret, nil
}

// CreateNetworkPolicy allows the workload to reach the connection pooler.
//
// The Postgres NetworkPolicy selects every pod labelled cnpg.io/cluster, which
// includes the pooler pods, and denies all ingress it does not name explicitly.
// Without this policy a workload cannot connect at all.
func CreateNetworkPolicy(scheme *runtime.Scheme, b *v1.PostgresBinding) (*networking_v1.NetworkPolicy, error) {
	netpol := &networking_v1.NetworkPolicy{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: objectMeta(b, b.ConfigSecretName()),
		Spec: networking_v1.NetworkPolicySpec{
			// Selects the pooler pods, not the workload: this grants ingress.
			PodSelector: meta_v1.LabelSelector{
				MatchLabels: map[string]string{
					cnpg.PoolerNameLabel: cnpg.PoolerNameFor(b.Spec.Postgres),
				},
			},
			PolicyTypes: []networking_v1.PolicyType{networking_v1.PolicyTypeIngress},
			Ingress: []networking_v1.NetworkPolicyIngressRule{
				{
					From: []networking_v1.NetworkPolicyPeer{
						{
							PodSelector: ptr.To(workloadSelector(b.Spec.Workload)),
						},
					},
					Ports: []networking_v1.NetworkPolicyPort{
						{Port: ptr.To(intstr.FromInt32(5432))},
					},
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(b, netpol, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on NetworkPolicy: %w", err)
	}
	return netpol, nil
}

// CreateEgressNetworkPolicy allows the workload to resolve and reach the
// connection pooler. It selects workload pods, complementing CreateNetworkPolicy
// which selects pooler pods and grants their ingress.
func CreateEgressNetworkPolicy(scheme *runtime.Scheme, b *v1.PostgresBinding) (*networking_v1.NetworkPolicy, error) {
	netpol := &networking_v1.NetworkPolicy{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: objectMeta(b, b.ConfigSecretName()+"-egress"),
		Spec: networking_v1.NetworkPolicySpec{
			PodSelector: workloadSelector(b.Spec.Workload),
			PolicyTypes: []networking_v1.PolicyType{networking_v1.PolicyTypeEgress},
			Egress: []networking_v1.NetworkPolicyEgressRule{
				{
					To: []networking_v1.NetworkPolicyPeer{
						{
							PodSelector: &meta_v1.LabelSelector{
								MatchLabels: map[string]string{
									cnpg.PoolerNameLabel: cnpg.PoolerNameFor(b.Spec.Postgres),
								},
							},
						},
					},
					Ports: []networking_v1.NetworkPolicyPort{
						{Port: ptr.To(intstr.FromInt32(5432))},
					},
				},
				{
					To: []networking_v1.NetworkPolicyPeer{
						{
							NamespaceSelector: &meta_v1.LabelSelector{},
							PodSelector: &meta_v1.LabelSelector{
								MatchLabels: map[string]string{"k8s-app": "kube-dns"},
							},
						},
					},
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(b, netpol, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on egress NetworkPolicy: %w", err)
	}
	return netpol, nil
}
