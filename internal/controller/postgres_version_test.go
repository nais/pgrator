package controller

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestPostgresCRDMajorVersions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		filename   string
		properties []string
		want       []string
	}{
		{
			name:       "legacy",
			filename:   "data.nais.io_postgres.yaml",
			properties: []string{"spec", "cluster", "majorVersion"},
			want:       []string{"16", "17", "18"},
		},
		{
			name:       "cnpg",
			filename:   "nais.io_postgres.yaml",
			properties: []string{"spec", "majorVersion"},
			want:       []string{"18"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, dir := range []string{
				filepath.Join("..", "..", "config", "crd", "bases"),
				filepath.Join("..", "..", "charts", "pgrator", "templates", "crd"),
			} {
				t.Run(dir, func(t *testing.T) {
					path := filepath.Join(dir, tc.filename)
					crd := &apiext.CustomResourceDefinition{}
					mustUnmarshalYAMLFile(t, path, crd)
					if len(crd.Spec.Versions) != 1 {
						t.Fatalf("expected exactly one CRD version in %s, got %d", path, len(crd.Spec.Versions))
					}
					schema := crd.Spec.Versions[0].Schema
					if schema == nil || schema.OpenAPIV3Schema == nil {
						t.Fatalf("no schema in %s", path)
					}
					property := *schema.OpenAPIV3Schema
					for _, name := range tc.properties {
						next, ok := property.Properties[name]
						if !ok {
							t.Fatalf("no property %s in %s", name, path)
						}
						property = next
					}
					values := make([]string, 0, len(property.Enum))
					for _, raw := range property.Enum {
						var value string
						requireNoError(t, json.Unmarshal(raw.Raw, &value))
						values = append(values, value)
					}
					slices.Sort(values)
					if !slices.Equal(values, tc.want) {
						t.Errorf("majorVersion enum is %v, want %v", values, tc.want)
					}
				})
			}
		})
	}
}
