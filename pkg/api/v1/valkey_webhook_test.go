package v1

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newValkey(name, namespace string, version ValkeyVersion) *Valkey {
	return &Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: ValkeySpec{
			Tier:    ValkeyTierSingleNode,
			Memory:  ValkeyMemory4GB,
			Version: version,
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
			valkey: newValkey("my-valkey", "my-team", ValkeyVersionV9_1),
		},
		{
			name:   "allows creating directly on the oldest supported version",
			valkey: newValkey("my-valkey", "my-team", ValkeyVersionV8_1),
		},
		{
			name:   "allows creating on a deprecated version",
			valkey: newValkey("my-valkey", "my-team", ValkeyVersionV9_0),
		},
		{
			name:   "allows name at exactly the max length",
			valkey: newValkey(strings.Repeat("a", 48), "my-team", ValkeyVersionV9_1),
		},
		{
			name:      "rejects name that is too long",
			valkey:    newValkey(strings.Repeat("a", 49), "my-team", ValkeyVersionV9_1),
			wantError: "metadata.name is too long",
		},
		{
			name:      "rejects resource when namespace is excessively long",
			valkey:    newValkey("valkey", strings.Repeat("n", 60), ValkeyVersionV9_1),
			wantError: "metadata.namespace is too long",
		},
		{
			name:      "rejects unknown version",
			valkey:    newValkey("my-valkey", "my-team", ValkeyVersion("7.2")),
			wantError: `unknown Valkey version: "7.2"`,
		},
		{
			name:      "rejects omitted version",
			valkey:    newValkey("my-valkey", "my-team", ""),
			wantError: "spec.version is required",
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

func TestValkeyValidatorDeprecationWarning(t *testing.T) {
	tests := []struct {
		name        string
		version     ValkeyVersion
		wantWarning bool
	}{
		{name: "warns on 9.0", version: ValkeyVersionV9_0, wantWarning: true},
		{name: "silent on 8.1", version: ValkeyVersionV8_1},
		{name: "silent on 9.1", version: ValkeyVersionV9_1},
	}

	validator := &ValkeyValidator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := newValkey("my-valkey", "my-team", tt.version)

			warnings, err := validator.ValidateCreate(context.Background(), obj)
			requireNoError(t, err)
			if got := len(warnings) > 0; got != tt.wantWarning {
				t.Fatalf("warnings present = %v, want %v (%v)", got, tt.wantWarning, warnings)
			}
			if tt.wantWarning && !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "end-of-life") }) {
				t.Errorf("warning does not mention end-of-life: %v", warnings)
			}
		})
	}
}

func TestValkeyValidatorValidateUpdate(t *testing.T) {
	t.Run("allows unrelated spec changes", func(t *testing.T) {
		oldObj := newValkey("my-valkey", "my-team", ValkeyVersionV9_1)
		newObj := newValkey("my-valkey", "my-team", ValkeyVersionV9_1)
		newObj.Spec.Tier = ValkeyTierHighAvailability
		newObj.Spec.Memory = ValkeyMemory8GB

		_, err := (&ValkeyValidator{}).ValidateUpdate(context.Background(), oldObj, newObj)
		requireNoError(t, err)
	})

	// pgrator writes the finalizer through this webhook, and legacy objects have no version yet.
	t.Run("admits metadata-only writes without checking the version", func(t *testing.T) {
		oldObj := newValkey("my-valkey", "my-team", "")
		newObj := newValkey("my-valkey", "my-team", "")
		newObj.Finalizers = []string{"valkey.nais.io/finalizer"}

		_, err := (&ValkeyValidator{}).ValidateUpdate(context.Background(), oldObj, newObj)
		requireNoError(t, err)
	})

	t.Run("rejects a spec edit that leaves the version unset", func(t *testing.T) {
		oldObj := newValkey("my-valkey", "my-team", "")
		newObj := newValkey("my-valkey", "my-team", "")
		newObj.Spec.Memory = ValkeyMemory8GB

		_, err := (&ValkeyValidator{}).ValidateUpdate(context.Background(), oldObj, newObj)
		requireErrorContains(t, err, "spec.version is required")
	})

	validUpgrades := []struct {
		name       string
		oldVersion ValkeyVersion
		newVersion ValkeyVersion
	}{
		{name: "8.1 to 9.1", oldVersion: ValkeyVersionV8_1, newVersion: ValkeyVersionV9_1},
		{name: "9.0 to 9.1", oldVersion: ValkeyVersionV9_0, newVersion: ValkeyVersionV9_1},
		{name: "same version 8.1", oldVersion: ValkeyVersionV8_1, newVersion: ValkeyVersionV8_1},
		{name: "same version 9.0", oldVersion: ValkeyVersionV9_0, newVersion: ValkeyVersionV9_0},
		{name: "same version 9.1", oldVersion: ValkeyVersionV9_1, newVersion: ValkeyVersionV9_1},
		{name: "adopting a version for the first time", oldVersion: "", newVersion: ValkeyVersionV8_1},
	}

	validator := &ValkeyValidator{}
	for _, tt := range validUpgrades {
		t.Run("valid version upgrade/"+tt.name, func(t *testing.T) {
			oldObj := newValkey("my-valkey", "my-team", tt.oldVersion)
			newObj := newValkey("my-valkey", "my-team", tt.newVersion)

			_, err := validator.ValidateUpdate(context.Background(), oldObj, newObj)
			requireNoError(t, err)
		})
	}

	invalidUpgrades := []struct {
		name       string
		oldVersion ValkeyVersion
		newVersion ValkeyVersion
		wantError  string
	}{
		{name: "9.1 to 9.0 (downgrade)", oldVersion: ValkeyVersionV9_1, newVersion: ValkeyVersionV9_0, wantError: "validation failed: cannot change Valkey version from 9.1 to 9.0: no further upgrades available"},
		{name: "9.1 to 8.1 (downgrade)", oldVersion: ValkeyVersionV9_1, newVersion: ValkeyVersionV8_1, wantError: "validation failed: cannot change Valkey version from 9.1 to 8.1: no further upgrades available"},
		{name: "9.0 to 8.1 (downgrade)", oldVersion: ValkeyVersionV9_0, newVersion: ValkeyVersionV8_1, wantError: "validation failed: cannot change Valkey version from 9.0 to 8.1: new version must be one of [9.1]"},
		{name: "8.1 to 9.0 (end-of-life destination)", oldVersion: ValkeyVersionV8_1, newVersion: ValkeyVersionV9_0, wantError: "validation failed: cannot change Valkey version from 8.1 to 9.0: new version must be one of [9.1]"},
		{name: "unsetting an established version", oldVersion: ValkeyVersionV9_1, newVersion: "", wantError: "spec.version cannot be unset once set"},
	}

	for _, tt := range invalidUpgrades {
		t.Run("invalid version upgrade/"+tt.name, func(t *testing.T) {
			oldObj := newValkey("my-valkey", "my-team", tt.oldVersion)
			newObj := newValkey("my-valkey", "my-team", tt.newVersion)

			_, err := validator.ValidateUpdate(context.Background(), oldObj, newObj)
			requireErrorContains(t, err, tt.wantError)
		})
	}
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
			obj := newValkey("my-valkey", "my-team", ValkeyVersionV9_1)
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
