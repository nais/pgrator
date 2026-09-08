// Package binding builds pgrator-owned workload binding resources.
package binding

import (
	"crypto/sha256"
	"fmt"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const nameLabel = "postgres.nais.io/binding"
const appDatabase = "app"

func objectMeta(b *v1.PostgresBinding, name string) metav1.ObjectMeta {
	annotations := map[string]string(nil)
	if b.GetCorrelationId() != "" {
		annotations = map[string]string{api.DeploymentCorrelationIDAnnotation: b.GetCorrelationId()}
	}
	return metav1.ObjectMeta{Name: name, Namespace: b.GetNamespace(), Labels: map[string]string{nameLabel: shortenedName(b.GetName(), "", 63)}, Annotations: annotations}
}

func shortenedName(name, suffix string, limit int) string {
	if len(name)+len(suffix) <= limit {
		return name + suffix
	}
	hash := sha256.Sum256([]byte(name + suffix))
	hashText := fmt.Sprintf("%x", hash[:8])
	prefix := strings.TrimRight(name[:limit-len(suffix)-len(hashText)-1], "-_.")
	return fmt.Sprintf("%s-%s%s", prefix, hashText, suffix)
}

// DatabaseRoleName is unique for a binding, credential and physical instance.
// The suffix leaves room for CNPG's -client-cert Secret suffix.
func DatabaseRoleName(b *v1.PostgresBinding, instance string, credential v1.PostgresBindingCredential) string {
	return shortenedName(b.GetName()+"-"+instance, "-"+string(credential), 241)
}

func CreateDatabaseRole(scheme *runtime.Scheme, b *v1.PostgresBinding, instance string, credential v1.PostgresBindingCredential) (*cnpgv1.DatabaseRole, error) {
	role := &cnpgv1.DatabaseRole{
		TypeMeta:   metav1.TypeMeta{Kind: "DatabaseRole", APIVersion: cnpgv1.SchemeGroupVersion.String()},
		ObjectMeta: objectMeta(b, DatabaseRoleName(b, instance, credential)),
		Spec: cnpgv1.DatabaseRoleSpec{
			ClusterRef:        corev1.LocalObjectReference{Name: cnpg.ClusterNameFor(instance)},
			ReclaimPolicy:     cnpgv1.DatabaseRoleReclaimDelete,
			RoleConfiguration: cnpgv1.RoleConfiguration{Name: b.RoleName(credential), Login: true, Comment: fmt.Sprintf("Managed by pgrator for workload %q", b.Spec.Consumer.Workload.Name), InRoles: []string{groupRole(credential)}},
			ClientCertificate: &cnpgv1.ClientCertificateConfiguration{Enabled: new(true)},
		},
	}
	if err := controllerutil.SetControllerReference(b, role, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on DatabaseRole: %w", err)
	}
	return role, nil
}

func groupRole(credential v1.PostgresBindingCredential) string {
	if credential == v1.PostgresBindingCredentialRead {
		return cnpg.ReadRole
	}
	return cnpg.ReadWriteRole
}

// CredentialMaterial is the certificate and private key CNPG issued for one
// binding credential.
type CredentialMaterial struct {
	Certificate []byte
	PrivateKey  []byte
}

// CreateConfigSecret creates the complete stable workload-facing snapshot for an
// active instance. Callers must supply only validated material.
func CreateConfigSecret(scheme *runtime.Scheme, b *v1.PostgresBinding, instance string, caCertificate []byte, credentials map[v1.PostgresBindingCredential]CredentialMaterial) (*corev1.Secret, error) {
	data := make(map[string]string, len(b.Spec.Credentials)*5)
	secretData := make(map[string][]byte, 1+len(b.Spec.Credentials)*2)
	secretData["ca.crt"] = caCertificate
	for _, credential := range b.Spec.Credentials {
		prefix := v1.ConnectionEnvPrefix(credential)
		data[prefix+"PGHOST"] = fmt.Sprintf("%s.%s", cnpg.PoolerNameFor(instance), b.GetNamespace())
		data[prefix+"PGPORT"] = "5432"
		data[prefix+"PGDATABASE"] = appDatabase
		data[prefix+"PGUSER"] = b.RoleName(credential)
		data[prefix+"PGSSLMODE"] = "verify-full"
		material := credentials[credential]
		secretData[string(credential)+".tls.crt"] = material.Certificate
		secretData[string(credential)+".tls.key"] = material.PrivateKey
	}
	secret := &corev1.Secret{TypeMeta: metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"}, ObjectMeta: objectMeta(b, b.GetName()), StringData: data, Data: secretData}
	if err := controllerutil.SetControllerReference(b, secret, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on config Secret: %w", err)
	}
	return secret, nil
}

func workloadSelector(workload v1.PostgresBindingWorkload) metav1.LabelSelector {
	return metav1.LabelSelector{MatchLabels: map[string]string{"app": workload.Name}}
}

func CreateNetworkPolicy(scheme *runtime.Scheme, b *v1.PostgresBinding, instance string) (*networkingv1.NetworkPolicy, error) {
	netpol := &networkingv1.NetworkPolicy{
		TypeMeta:   metav1.TypeMeta{Kind: "NetworkPolicy", APIVersion: "networking.k8s.io/v1"},
		ObjectMeta: objectMeta(b, b.GetName()),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{cnpg.PoolerNameLabel: cnpg.PoolerNameFor(instance)}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{{PodSelector: new(workloadSelector(*b.Spec.Consumer.Workload))}},
				Ports: []networkingv1.NetworkPolicyPort{{Port: new(intstr.FromInt32(5432))}},
			}},
		},
	}
	if err := controllerutil.SetControllerReference(b, netpol, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on NetworkPolicy: %w", err)
	}
	return netpol, nil
}

func CreateEgressNetworkPolicy(scheme *runtime.Scheme, b *v1.PostgresBinding, instance string) (*networkingv1.NetworkPolicy, error) {
	netpol := &networkingv1.NetworkPolicy{
		TypeMeta:   metav1.TypeMeta{Kind: "NetworkPolicy", APIVersion: "networking.k8s.io/v1"},
		ObjectMeta: objectMeta(b, shortenedName(b.GetName(), "-egress", 253)),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: workloadSelector(*b.Spec.Consumer.Workload),
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{cnpg.PoolerNameLabel: cnpg.PoolerNameFor(instance)}}}}, Ports: []networkingv1.NetworkPolicyPort{{Port: new(intstr.FromInt32(5432))}}},
				{To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{}, PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}}}}},
			},
		},
	}
	if err := controllerutil.SetControllerReference(b, netpol, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on NetworkPolicy: %w", err)
	}
	return netpol, nil
}
