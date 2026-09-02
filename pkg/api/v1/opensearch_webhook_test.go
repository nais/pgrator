package v1

import (
	"context"
	"strings"
	"testing"

	"github.com/nais/pgrator/pkg/api"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newOpenSearch(name, namespace string, tier OpenSearchTier, memory OpenSearchMemory, version OpenSearchVersion, storageGB int) *OpenSearch {
	return &OpenSearch{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: OpenSearchSpec{
			Tier:      tier,
			Memory:    memory,
			Version:   version,
			StorageGB: storageGB,
		},
	}
}

func TestOpenSearchValidatorValidateCreate(t *testing.T) {
	validConfigurations := []struct {
		name       string
		objectName string
		namespace  string
		tier       OpenSearchTier
		memory     OpenSearchMemory
		version    OpenSearchVersion
		storageGB  int
	}{
		{name: "SingleNode 4GB with valid storage", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierSingleNode, memory: OpenSearchMemory4GB, version: OpenSearchVersionV2, storageGB: 80},
		{name: "HighAvailability 8GB with valid storage", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierHighAvailability, memory: OpenSearchMemory8GB, version: OpenSearchVersionV2_19, storageGB: 525},
		{name: "hobbyist plan with exact storage", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierSingleNode, memory: OpenSearchMemory2GB, version: OpenSearchVersionV1, storageGB: 16},
		{name: "storage at boundary (min) for HA 16GB", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierHighAvailability, memory: OpenSearchMemory16GB, version: OpenSearchVersionV3_3, storageGB: 1050},
		{name: "storage at boundary (max) for HA 16GB", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierHighAvailability, memory: OpenSearchMemory16GB, version: OpenSearchVersionV3_3, storageGB: 5250},
		{name: "SingleNode 8GB storage with increment", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierSingleNode, memory: OpenSearchMemory8GB, version: OpenSearchVersionV2, storageGB: 185},
		{name: "HighAvailability storage with 30GB increment", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierHighAvailability, memory: OpenSearchMemory4GB, version: OpenSearchVersionV2, storageGB: 270},
		{name: "name at max generated-service length", objectName: strings.Repeat("a", 44), namespace: "my-team", tier: OpenSearchTierSingleNode, memory: OpenSearchMemory4GB, version: OpenSearchVersionV2, storageGB: 80},
	}

	validator := &OpenSearchValidator{}
	for _, tt := range validConfigurations {
		t.Run("valid/"+tt.name, func(t *testing.T) {
			obj := newOpenSearch(tt.objectName, tt.namespace, tt.tier, tt.memory, tt.version, tt.storageGB)
			_, err := validator.ValidateCreate(context.Background(), obj)
			requireNoError(t, err)
		})
	}

	invalidConfigurations := []struct {
		name       string
		objectName string
		namespace  string
		tier       OpenSearchTier
		memory     OpenSearchMemory
		version    OpenSearchVersion
		storageGB  int
		wantError  string
	}{
		{name: "HighAvailability with 2GB memory (not supported)", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierHighAvailability, memory: OpenSearchMemory2GB, version: OpenSearchVersionV2, storageGB: 100, wantError: "invalid tier/memory combination"},
		{name: "storage below minimum for SingleNode 4GB", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierSingleNode, memory: OpenSearchMemory4GB, version: OpenSearchVersionV2, storageGB: 50, wantError: "storage must be at least 80GB"},
		{name: "storage above maximum for SingleNode 4GB", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierSingleNode, memory: OpenSearchMemory4GB, version: OpenSearchVersionV2, storageGB: 500, wantError: "storage must be at most 400GB"},
		{name: "storage not in valid increments for SingleNode 4GB", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierSingleNode, memory: OpenSearchMemory4GB, version: OpenSearchVersionV2, storageGB: 85, wantError: "storage must be in increments of 10GB"},
		{name: "hobbyist plan with wrong storage", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierSingleNode, memory: OpenSearchMemory2GB, version: OpenSearchVersionV1, storageGB: 32, wantError: "storage must be at most 16GB"},
		{name: "name too long for generated service name", objectName: "this-is-a-very-long-opensearch-instance-name-that-exceeds-limit", namespace: "my-team", tier: OpenSearchTierSingleNode, memory: OpenSearchMemory4GB, version: OpenSearchVersionV2, storageGB: 80, wantError: "metadata.name is too long"},
		{name: "HighAvailability storage not in 30GB increments", objectName: "my-opensearch", namespace: "my-team", tier: OpenSearchTierHighAvailability, memory: OpenSearchMemory4GB, version: OpenSearchVersionV2, storageGB: 250, wantError: "storage must be in increments of 30GB"},
		{name: "name exceeds max generated-service length", objectName: strings.Repeat("a", 45), namespace: "my-team", tier: OpenSearchTierSingleNode, memory: OpenSearchMemory4GB, version: OpenSearchVersionV2, storageGB: 80, wantError: "metadata.name is too long; max length is 44 characters"},
		{name: "namespace leaves no room for generated service name", objectName: "my-opensearch", namespace: strings.Repeat("n", 60), tier: OpenSearchTierSingleNode, memory: OpenSearchMemory4GB, version: OpenSearchVersionV2, storageGB: 80, wantError: "metadata.namespace is too long"},
	}

	for _, tt := range invalidConfigurations {
		t.Run("invalid/"+tt.name, func(t *testing.T) {
			obj := newOpenSearch(tt.objectName, tt.namespace, tt.tier, tt.memory, tt.version, tt.storageGB)
			_, err := validator.ValidateCreate(context.Background(), obj)
			requireErrorContains(t, err, tt.wantError)
		})
	}
}

func TestOpenSearchValidatorValidateCreateMaxContentLength(t *testing.T) {
	validQuantities := []string{"100Mi", "1Gi", "1", "2147483647", "2G", "2047Mi"}
	validator := &OpenSearchValidator{}

	for _, quantity := range validQuantities {
		t.Run("valid/"+quantity, func(t *testing.T) {
			q := resource.MustParse(quantity)
			obj := newOpenSearch("my-opensearch", "my-team", OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 80)
			obj.Spec.Http = &OpenSearchHttp{MaxContentLength: &q}

			_, err := validator.ValidateCreate(context.Background(), obj)
			requireNoError(t, err)
		})
	}

	invalidQuantities := []struct {
		name      string
		quantity  string
		wantError string
	}{
		{name: "zero bytes", quantity: "0", wantError: "http.maxContentLength must be at least 1 byte"},
		{name: "exceeds max", quantity: "3Gi", wantError: "http.maxContentLength must be at most 2147483647 bytes"},
		{name: "exactly 2Gi exceeds int32 max", quantity: "2Gi", wantError: "http.maxContentLength must be at most 2147483647 bytes"},
	}

	for _, tt := range invalidQuantities {
		t.Run("invalid/"+tt.name, func(t *testing.T) {
			q := resource.MustParse(tt.quantity)
			obj := newOpenSearch("my-opensearch", "my-team", OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 80)
			obj.Spec.Http = &OpenSearchHttp{MaxContentLength: &q}

			_, err := validator.ValidateCreate(context.Background(), obj)
			requireErrorContains(t, err, tt.wantError)
		})
	}
}

func TestOpenSearchValidatorValidateUpdate(t *testing.T) {
	validator := &OpenSearchValidator{}

	t.Run("allows valid storage update", func(t *testing.T) {
		oldObj := newOpenSearch("my-opensearch", "my-team", OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 80)
		newObj := newOpenSearch("my-opensearch", "my-team", OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 90)

		_, err := validator.ValidateUpdate(context.Background(), oldObj, newObj)
		requireNoError(t, err)
	})

	validUpgrades := []struct {
		name       string
		oldVersion OpenSearchVersion
		newVersion OpenSearchVersion
	}{
		{name: "V1 to V2", oldVersion: OpenSearchVersionV1, newVersion: OpenSearchVersionV2},
		{name: "V1 to V2.19", oldVersion: OpenSearchVersionV1, newVersion: OpenSearchVersionV2_19},
		{name: "V2 to V2.19", oldVersion: OpenSearchVersionV2, newVersion: OpenSearchVersionV2_19},
		{name: "V2.19 to V3.3", oldVersion: OpenSearchVersionV2_19, newVersion: OpenSearchVersionV3_3},
		{name: "same version V1", oldVersion: OpenSearchVersionV1, newVersion: OpenSearchVersionV1},
		{name: "same version V2", oldVersion: OpenSearchVersionV2, newVersion: OpenSearchVersionV2},
		{name: "same version V2.19", oldVersion: OpenSearchVersionV2_19, newVersion: OpenSearchVersionV2_19},
		{name: "same version V3.3", oldVersion: OpenSearchVersionV3_3, newVersion: OpenSearchVersionV3_3},
	}

	for _, tt := range validUpgrades {
		t.Run("valid version upgrade/"+tt.name, func(t *testing.T) {
			oldObj := newOpenSearch("my-opensearch", "my-team", OpenSearchTierSingleNode, OpenSearchMemory4GB, tt.oldVersion, 80)
			newObj := newOpenSearch("my-opensearch", "my-team", OpenSearchTierSingleNode, OpenSearchMemory4GB, tt.newVersion, 80)

			_, err := validator.ValidateUpdate(context.Background(), oldObj, newObj)
			requireNoError(t, err)
		})
	}

	invalidUpgrades := []struct {
		name       string
		oldVersion OpenSearchVersion
		newVersion OpenSearchVersion
		wantError  string
	}{
		{name: "V1 to V3.3 (skipping versions)", oldVersion: OpenSearchVersionV1, newVersion: OpenSearchVersionV3_3, wantError: "validation failed: cannot change OpenSearch version from 1 to 3.3: new version must be one of [2, 2.19]"},
		{name: "V2 to V3.3 (skipping V2.19)", oldVersion: OpenSearchVersionV2, newVersion: OpenSearchVersionV3_3, wantError: "validation failed: cannot change OpenSearch version from 2 to 3.3: new version must be one of [2.19]"},
		{name: "V3.3 to V2 (downgrade)", oldVersion: OpenSearchVersionV3_3, newVersion: OpenSearchVersionV2, wantError: "validation failed: cannot change OpenSearch version from 3.3 to 2: no further upgrades available"},
		{name: "V3.3 to V1 (downgrade)", oldVersion: OpenSearchVersionV3_3, newVersion: OpenSearchVersionV1, wantError: "validation failed: cannot change OpenSearch version from 3.3 to 1: no further upgrades available"},
		{name: "V2.19 to V1 (downgrade)", oldVersion: OpenSearchVersionV2_19, newVersion: OpenSearchVersionV1, wantError: "validation failed: cannot change OpenSearch version from 2.19 to 1: new version must be one of [3.3]"},
		{name: "V2 to V1 (downgrade)", oldVersion: OpenSearchVersionV2, newVersion: OpenSearchVersionV1, wantError: "validation failed: cannot change OpenSearch version from 2 to 1: new version must be one of [2.19]"},
	}

	for _, tt := range invalidUpgrades {
		t.Run("invalid version upgrade/"+tt.name, func(t *testing.T) {
			oldObj := newOpenSearch("my-opensearch", "my-team", OpenSearchTierSingleNode, OpenSearchMemory4GB, tt.oldVersion, 80)
			newObj := newOpenSearch("my-opensearch", "my-team", OpenSearchTierSingleNode, OpenSearchMemory4GB, tt.newVersion, 80)

			_, err := validator.ValidateUpdate(context.Background(), oldObj, newObj)
			requireErrorEqual(t, err, tt.wantError)
		})
	}
}

func TestOpenSearchValidatorValidateDelete(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantError   string
	}{
		{
			name:        "allows deletion when annotation is present and true",
			annotations: map[string]string{api.AllowDeletionAnnotation: "true"},
		},
		{
			name:      "refuses deletion when annotation is missing",
			wantError: "nais.io/allowDeletion",
		},
		{
			name:        "refuses deletion when annotation is set to false",
			annotations: map[string]string{api.AllowDeletionAnnotation: "false"},
			wantError:   "nais.io/allowDeletion",
		},
	}

	validator := &OpenSearchValidator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := newOpenSearch("my-opensearch", "my-team", OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 80)
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
