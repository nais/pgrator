// Package access builds resources for personal Postgres access.
package access

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const roleNameLimit = 63

// DatabaseRoleName returns the Kubernetes name of the DatabaseRole for one
// user and physical instance. The -email marker distinguishes it from the
// previous resource, whose immutable spec.name contained a branch-specific
// PostgreSQL role name.
func DatabaseRoleName(username, instance string) string {
	localPart, _, _ := strings.Cut(username, "@")
	base := normalizeName(localPart) + "-" + normalizeName(instance)
	hash := sha256.Sum256([]byte(username + "\x00" + instance))
	suffix := fmt.Sprintf("-email-%x", hash[:8])
	if len(base)+len(suffix) > roleNameLimit {
		base = strings.TrimRight(base[:roleNameLimit-len(suffix)], "-")
	}
	return base + suffix
}

// CredentialSecretName returns the short-lived Secret name for one access.
func CredentialSecretName(access *v1.PostgresAccess) string {
	return boundedName(access.Name, "-credentials", validation.DNS1123SubdomainMaxLength)
}

// boundedName returns name+suffix, shortened deterministically with a hash tag
// when the result would exceed maxLen. The hash keeps distinct long inputs from
// colliding after truncation.
func boundedName(name, suffix string, maxLen int) string {
	if len(name)+len(suffix) <= maxLen {
		return name + suffix
	}
	hash := sha256.Sum256([]byte(name))
	tag := fmt.Sprintf("-%x", hash[:4])
	keep := maxLen - len(suffix) - len(tag)
	return strings.TrimRight(name[:keep], "-") + tag + suffix
}

// postgresRoleName uses the authenticated email verbatim as the PostgreSQL
// role name. PostgreSQL truncates identifiers beyond 63 bytes, which could
// otherwise make two different emails share a role and its object ownership.
func postgresRoleName(username string) (string, error) {
	if len(username) > roleNameLimit || strings.ContainsRune(username, 0) {
		return "", fmt.Errorf("personal PostgreSQL username must be at most %d bytes and contain no NUL", roleNameLimit)
	}
	return username, nil
}

// CreateCredentialSecret creates a basic-auth Secret that CNPG can use to set
// the current access password. Password generation is deliberately separate so
// a reconciler can preserve an existing credential across retries.
func CreateCredentialSecret(scheme *runtime.Scheme, access *v1.PostgresAccess, password string) (*corev1.Secret, error) {
	roleName, err := postgresRoleName(access.Spec.Username)
	if err != nil {
		return nil, err
	}
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CredentialSecretName(access),
			Namespace: access.Namespace,
			Labels:    map[string]string{"cnpg.io/reload": "true"},
		},
		Type: corev1.SecretTypeBasicAuth,
		StringData: map[string]string{
			corev1.BasicAuthUsernameKey: roleName,
			corev1.BasicAuthPasswordKey: password,
		},
	}
	if err := controllerutil.SetControllerReference(access, secret, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on credential Secret: %w", err)
	}
	return secret, nil
}

func NewPassword() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func normalizeName(value string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
			lastHyphen = r == '-'
			continue
		}
		if !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "user"
	}
	return name
}

// CreateDatabaseRole creates the CNPG representation of a personal database
// identity. The DatabaseRole CR is owned by the access, while Retain keeps the
// PostgreSQL role and objects it owns after the access is deleted.
func CreateDatabaseRole(scheme *runtime.Scheme, access *v1.PostgresAccess, active bool) (*cnpgv1.DatabaseRole, error) {
	roleName, err := postgresRoleName(access.Spec.Username)
	if err != nil {
		return nil, err
	}
	configuration := cnpgv1.RoleConfiguration{
		Name:        roleName,
		Comment:     "Personal database identity",
		Login:       active,
		Superuser:   false,
		CreateDB:    false,
		CreateRole:  false,
		Replication: false,
		BypassRLS:   false,
	}
	if active {
		configuration.PasswordSecret = &cnpgv1.LocalObjectReference{Name: CredentialSecretName(access)}
		configuration.ValidUntil = &access.Spec.ExpiresAt
		configuration.InRoles = []string{groupRole(access.Spec.AccessLevel), rccnpg.PersonalAccessRole(access.Spec.PostgresBranch)}
	} else {
		configuration.DisablePassword = true
	}
	role := &cnpgv1.DatabaseRole{
		TypeMeta: metav1.TypeMeta{Kind: "DatabaseRole", APIVersion: cnpgv1.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{
			Name:      DatabaseRoleName(access.Spec.Username, access.Spec.PostgresBranch),
			Namespace: access.Namespace,
		},
		Spec: cnpgv1.DatabaseRoleSpec{
			ClusterRef:        corev1.LocalObjectReference{Name: rccnpg.ClusterNameFor(access.Spec.PostgresBranch)},
			ReclaimPolicy:     cnpgv1.DatabaseRoleReclaimRetain,
			RoleConfiguration: configuration,
		},
	}
	if err := controllerutil.SetControllerReference(access, role, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on DatabaseRole: %w", err)
	}
	return role, nil
}

func groupRole(level v1.PostgresAccessLevel) string {
	switch level {
	case v1.PostgresAccessLevelRead:
		return rccnpg.ReadRole
	case v1.PostgresAccessLevelReadWrite:
		return rccnpg.ReadWriteRole
	default:
		return rccnpg.ReadWriteCreateRole
	}
}
