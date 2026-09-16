// Package access builds resources for personal Postgres access.
package access

import (
	"crypto/sha256"
	"fmt"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const roleNameLimit = 63

// DatabaseRoleName returns the stable personal role name for one user and
// physical instance. The hash includes the full email address so equal local
// parts from different email domains cannot share a database identity.
func DatabaseRoleName(username, instance string) string {
	localPart, _, _ := strings.Cut(username, "@")
	base := normalizeName(localPart) + "-" + normalizeName(instance)
	hash := sha256.Sum256([]byte(username + "\x00" + instance))
	suffix := fmt.Sprintf("-%x", hash[:8])
	if len(base)+len(suffix) > roleNameLimit {
		base = strings.TrimRight(base[:roleNameLimit-len(suffix)], "-")
	}
	return base + suffix
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

// CreateDatabaseRole creates the durable CNPG representation of a personal
// database identity. Access-specific login, credential and privilege state is
// added by the PostgresAccess lifecycle in a later reconciliation step.
func CreateDatabaseRole(access *v1.PostgresAccess) *cnpgv1.DatabaseRole {
	return &cnpgv1.DatabaseRole{
		TypeMeta: metav1.TypeMeta{Kind: "DatabaseRole", APIVersion: cnpgv1.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{
			Name:      DatabaseRoleName(access.Spec.Username, access.Spec.PostgresInstance),
			Namespace: access.Namespace,
			Labels: map[string]string{
				"postgres.nais.io/instance": access.Spec.PostgresInstance,
			},
		},
		Spec: cnpgv1.DatabaseRoleSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: rccnpg.ClusterNameFor(access.Spec.PostgresInstance)},
			ReclaimPolicy: cnpgv1.DatabaseRoleReclaimRetain,
			RoleConfiguration: cnpgv1.RoleConfiguration{
				Name:        DatabaseRoleName(access.Spec.Username, access.Spec.PostgresInstance),
				Comment:     "Personal database identity",
				Login:       false,
				Superuser:   false,
				CreateDB:    false,
				CreateRole:  false,
				Replication: false,
				BypassRLS:   false,
			},
		},
	}
}
