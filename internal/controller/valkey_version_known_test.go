package controller

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// The enum decides what the API server stores, the known set decides what admission and the
// reconciler act on. A version in only one of them yields an object that is accepted and then
// permanently stuck: unknown to admission on every later edit, or adopted and impossible to record.
func TestValkeyCRDVersionEnumMatchesKnown(t *testing.T) {
	enum := valkeySpecVersionEnum(t)

	known := make([]string, 0, len(v1.KnownValkeyVersions()))
	for _, version := range v1.KnownValkeyVersions() {
		known = append(known, string(version))
	}

	slices.Sort(enum)
	slices.Sort(known)

	if !slices.Equal(enum, known) {
		t.Errorf("CRD spec.version enum is %v, known versions are %v", enum, known)
	}
}

func valkeySpecVersionEnum(t *testing.T) []string {
	t.Helper()

	path := filepath.Join("..", "..", "config", "crd", "bases", "nais.io_valkeys.yaml")

	crd := &apiext.CustomResourceDefinition{}
	mustUnmarshalYAMLFile(t, path, crd)

	if len(crd.Spec.Versions) != 1 {
		t.Fatalf("expected exactly one CRD version in %s, got %d", path, len(crd.Spec.Versions))
	}

	schema := crd.Spec.Versions[0].Schema
	if schema == nil || schema.OpenAPIV3Schema == nil {
		t.Fatalf("no schema in %s", path)
	}

	version, ok := schema.OpenAPIV3Schema.Properties["spec"].Properties["version"]
	if !ok {
		t.Fatalf("no spec.version property in %s", path)
	}

	values := make([]string, 0, len(version.Enum))
	for _, raw := range version.Enum {
		var value string
		requireNoError(t, json.Unmarshal(raw.Raw, &value))
		values = append(values, value)
	}
	return values
}
