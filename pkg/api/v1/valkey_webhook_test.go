package v1

import (
	"context"
	"strings"
	"testing"

	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newValkey(name, namespace string) *Valkey {
	return &Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: ValkeySpec{
			Tier:   ValkeyTierSingleNode,
			Memory: ValkeyMemory4GB,
		},
	}
}

func TestValkeyValidatorValidateCreate(t *testing.T) {
	tests := []struct {
		name      string
		valkey    *Valkey
		wantError string
	}{
		{
			name:   "allows any valid Valkey resource",
			valkey: newValkey("my-valkey", "my-team"),
		},
		{
			name:   "allows name at exactly the max length",
			valkey: newValkey(strings.Repeat("a", 48), "my-team"),
		},
		{
			name:      "rejects name that is too long",
			valkey:    newValkey(strings.Repeat("a", 49), "my-team"),
			wantError: "metadata.name is too long",
		},
		{
			name:      "rejects resource when namespace is excessively long",
			valkey:    newValkey("valkey", strings.Repeat("n", 60)),
			wantError: "metadata.namespace is too long",
		},
	}

	validator := &ValkeyValidator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validator.ValidateCreate(context.Background(), tt.valkey)
			if tt.wantError != "" {
				requireErrorContains(t, err, tt.wantError)
				return
			}
			requireNoError(t, err)
		})
	}
}

func TestValkeyValidatorValidateUpdate(t *testing.T) {
	oldObj := newValkey("my-valkey", "my-team")
	newObj := newValkey("my-valkey", "my-team")
	newObj.Spec.Tier = ValkeyTierHighAvailability
	newObj.Spec.Memory = ValkeyMemory8GB

	_, err := (&ValkeyValidator{}).ValidateUpdate(context.Background(), oldObj, newObj)
	requireNoError(t, err)
}

func TestValkeyValidatorValidateDelete(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantError   string
	}{
		{
			name:        "allows deletion when allowDeletion annotation is true",
			annotations: map[string]string{api.AllowDeletionAnnotation: "true"},
		},
		{
			name:      "refuses deletion when allowDeletion annotation is missing",
			wantError: "refusing deletion",
		},
		{
			name:        "refuses deletion when allowDeletion annotation is not true",
			annotations: map[string]string{api.AllowDeletionAnnotation: "false"},
			wantError:   "refusing deletion",
		},
	}

	validator := &ValkeyValidator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := newValkey("my-valkey", "my-team")
			obj.Annotations = tt.annotations

			_, err := validator.ValidateDelete(context.Background(), obj)
			if tt.wantError != "" {
				requireErrorContains(t, err, tt.wantError)
				return
			}
			requireNoError(t, err)
		})
	}
}
