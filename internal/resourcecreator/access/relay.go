package access

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/nais/pgrator/internal/resourcecreator/cnpg"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var RelayAccessGVK = schema.GroupVersionKind{Group: "nais.io", Version: "v1alpha1", Kind: "RelayAccess"}

const TokenKey = "token"

func RelayAccessName(access *v1.PostgresAccess) string { return access.Name }
func TokenSecretName(access *v1.PostgresAccess) string {
	return boundedName(access.Name, "-relay-token", validation.DNS1123SubdomainMaxLength)
}

// NewToken returns a canonical base64url encoding of 32 random bytes.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate relay token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// TokenDigest validates the stored token and hashes the raw bytes expected by relay.
func TokenDigest(token string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != token {
		return "", fmt.Errorf("invalid relay bearer token")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func CreateTokenSecret(scheme *runtime.Scheme, access *v1.PostgresAccess, token string) (*corev1.Secret, error) {
	if _, err := TokenDigest(token); err != nil {
		return nil, err
	}
	secret := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: TokenSecretName(access), Namespace: access.Namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{TokenKey: []byte(token)},
	}
	if err := controllerutil.SetControllerReference(access, secret, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on token Secret: %w", err)
	}
	return secret, nil
}

func CreateRelayAccess(scheme *runtime.Scheme, access *v1.PostgresAccess, digest string) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": RelayAccessGVK.GroupVersion().String(),
		"kind":       RelayAccessGVK.Kind,
		"metadata":   map[string]any{"name": RelayAccessName(access), "namespace": access.Namespace},
		"spec": map[string]any{
			"target":      map[string]any{"serviceName": cnpg.ClusterNameFor(access.Spec.PostgresBranch) + "-rw", "port": int64(5432)},
			"expiresAt":   access.Spec.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
			"tokenSHA256": digest,
		},
	}}
	obj.SetGroupVersionKind(RelayAccessGVK)
	if err := controllerutil.SetControllerReference(access, obj, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on RelayAccess: %w", err)
	}
	return obj, nil
}
