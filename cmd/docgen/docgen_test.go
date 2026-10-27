package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/xeipuuv/gojsonschema"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

// generateSchemas runs the docgen pipeline once (markdown + JSON schemas) and
// returns the directory containing the generated JSON schema files.
func generateSchemas(t *testing.T) string {
	t.Helper()

	schemaDir := t.TempDir()
	cfg := &Config{
		APIDir:      "../../pkg/api/...",
		OutputDir:   t.TempDir(),
		TemplateDir: "../../doc/templates",
		JSONSchema:  schemaDir,
	}

	if err := runWithConfig(cfg); err != nil {
		t.Fatalf("runWithConfig: %v", err)
	}

	return schemaDir
}

func schemaFilename(gvk schema.GroupVersionKind) string {
	return gvk.Group + "_" + gvk.Version + "_" + gvk.Kind + ".json"
}

func loadSchema(t *testing.T, dir, filename string) *gojsonschema.Schema {
	t.Helper()

	path, err := filepath.Abs(filepath.Join(dir, filename))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	loader := gojsonschema.NewReferenceLoader("file://" + path)
	sch, err := gojsonschema.NewSchema(loader)
	if err != nil {
		t.Fatalf("failed to load schema %s: %v", path, err)
	}
	return sch
}

func mustValidate(t *testing.T, sch *gojsonschema.Schema, doc any, wantValid bool, label string) {
	t.Helper()

	result, err := sch.Validate(gojsonschema.NewGoLoader(doc))
	if err != nil {
		t.Fatalf("%s: validate error: %v", label, err)
	}
	if result.Valid() != wantValid {
		t.Errorf("%s: valid=%v (want %v); errors: %v", label, result.Valid(), wantValid, result.Errors())
	}
}

func TestPublishedSchemasValidateNativeManifests(t *testing.T) {
	const unknownValue = "boom"
	schemaDir := generateSchemas(t)
	aggregate := loadSchema(t, schemaDir, "all.json")

	for gvk, version := range NativeKinds {
		t.Run(gvk.Kind, func(t *testing.T) {
			sch := loadSchema(t, schemaDir, schemaFilename(gvk))
			var crd map[string]any
			if err := marshalToInterface(&crd, ExampleRegistry[gvk]()); err != nil {
				t.Fatalf("marshalToInterface: %v", err)
			}
			mustValidate(t, sch, crd, false, "Kubernetes CRD form")
			mustValidate(t, aggregate, crd, false, "Kubernetes CRD form via all.json")

			spec := crd["spec"].(map[string]any)
			manifest := map[string]any{
				"version": version,
				"type":    gvk.Kind,
				"name":    crd["metadata"].(map[string]any)["name"],
				"spec":    spec,
			}
			mustValidate(t, sch, manifest, true, "native manifest")
			mustValidate(t, aggregate, manifest, true, "native manifest via all.json")

			manifest["unknownField"] = unknownValue
			mustValidate(t, sch, manifest, false, "unknown envelope field")
			delete(manifest, "unknownField")

			spec["unknownField"] = unknownValue
			mustValidate(t, sch, manifest, false, "unknown spec field")
			delete(spec, "unknownField")

			switch gvk.Kind {
			case valkeyKind:
				for _, version := range []string{"8.1", "9.0", "9.1"} {
					spec["version"] = version
					mustValidate(t, sch, manifest, true, "supported Valkey version")
					mustValidate(t, aggregate, manifest, true, "supported Valkey version via all.json")
				}
				spec["version"] = "unsupported"
				mustValidate(t, sch, manifest, false, "unsupported Valkey version")
				mustValidate(t, aggregate, manifest, false, "unsupported Valkey version via all.json")
				delete(spec, "version")
				mustValidate(t, sch, manifest, false, "omitted Valkey version")
				mustValidate(t, aggregate, manifest, false, "omitted Valkey version via all.json")
				spec["version"] = "9.1"
				for _, disabled := range []bool{true, false} {
					spec["persistence"] = map[string]any{"disabled": disabled}
					mustValidate(t, sch, manifest, true, "Valkey persistence")
					mustValidate(t, aggregate, manifest, true, "Valkey persistence via all.json")
				}
				spec["persistence"] = map[string]any{"disabled": "true"}
				mustValidate(t, sch, manifest, false, "invalid Valkey persistence type")
				spec["persistence"] = map[string]any{"unknownField": unknownValue}
				mustValidate(t, sch, manifest, false, "unknown Valkey persistence field")
			case "OpenSearch":
				http := spec["http"].(map[string]any)
				http["unknownField"] = unknownValue
				mustValidate(t, sch, manifest, false, "unknown nested spec field")
			case "Postgres":
				extensions := spec["extensions"].([]any)
				extensions[0].(map[string]any)["unknownField"] = unknownValue
				mustValidate(t, sch, manifest, false, "unknown Postgres extension field")
			}
		})
	}
}

func TestPostgresManifestWithExtensionsAndResources(t *testing.T) {
	schemaDir := generateSchemas(t)
	for _, filename := range []string{"all.json", "editor.json"} {
		t.Run(filename, func(t *testing.T) {
			sch := loadSchema(t, schemaDir, filename)
			for _, tc := range []struct {
				name      string
				extension string
				extra     string
				valid     bool
			}{
				{name: "valid", extension: "vector", valid: true},
				{name: "PostGIS", extension: "postgis", valid: true},
				{name: "TimescaleDB", extension: "timescaledb", valid: true},
				{name: "catalog image name", extension: "timescaledb-oss"},
				{name: "unknown extension", extension: "unsupported"},
				{name: "unknown spec field", extension: "vector", extra: "  unknownField: true\n"},
				{name: "unknown resource field", extension: "vector", extra: "    unknownField: true\n"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var manifest map[string]any
					if err := yaml.Unmarshal([]byte(fmt.Sprintf(`version: v1
type: Postgres
name: ext
spec:
  extensions:
    - name: %s
  majorVersion: "18"
  resources:
    cpu: 100m
    diskSize: 10Gi
    memory: 512Mi
`, tc.extension)+tc.extra), &manifest); err != nil {
						t.Fatal(err)
					}
					mustValidate(t, sch, manifest, tc.valid, tc.name)
				})
			}
		})
	}
}

func TestEditorSchemaValidatesNativeManifestsOnly(t *testing.T) {
	schemaDir := generateSchemas(t)
	editor := loadSchema(t, schemaDir, "editor.json")

	for gvk, version := range NativeKinds {
		t.Run(gvk.Kind, func(t *testing.T) {
			var crd map[string]any
			if err := marshalToInterface(&crd, ExampleRegistry[gvk]()); err != nil {
				t.Fatalf("marshalToInterface: %v", err)
			}
			spec := crd["spec"].(map[string]any)
			manifest := map[string]any{
				"version": version,
				"type":    gvk.Kind,
				"name":    crd["metadata"].(map[string]any)["name"],
				"spec":    spec,
			}
			mustValidate(t, editor, manifest, true, "native manifest")
			if gvk.Kind == valkeyKind {
				delete(spec, "version")
				mustValidate(t, editor, manifest, false, "native Valkey without spec.version")
				spec["version"] = "9.1"
			}
			delete(manifest, "version")
			mustValidate(t, editor, manifest, false, "native manifest without version")
		})
	}

	for name, doc := range map[string]any{
		"legacy application":  map[string]any{"version": "v1", "type": "Application", "name": "app"},
		"Kubernetes resource": map[string]any{"apiVersion": "v1", "kind": "ConfigMap"},
		"no discriminator":    map[string]any{"version": "v1", "name": "unrecognized"},
	} {
		t.Run(name, func(t *testing.T) {
			mustValidate(t, editor, doc, true, name)
		})
	}
}

func TestNativeValkeyDocumentationIncludesVersionAndPersistence(t *testing.T) {
	outputDir := t.TempDir()
	cfg := &Config{
		APIDir: "../../pkg/api/...", OutputDir: outputDir,
		TemplateDir: "../../doc/templates",
	}
	if err := runWithConfig(cfg); err != nil {
		t.Fatalf("runWithConfig: %v", err)
	}
	for filename, includedFields := range map[string][]string{
		"reference.md": {"## version", "## persistence"},
		"example.md":   {"  version:", "  persistence:"},
	} {
		data, err := os.ReadFile(filepath.Join(outputDir, "nais.io/v1/valkey", filename))
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range includedFields {
			if !strings.Contains(string(data), field) {
				t.Errorf("%s missing native field %q", filename, field)
			}
		}
		if filename == "reference.md" {
			_, versionSection, found := strings.Cut(string(data), "## version")
			versionSection, _, _ = strings.Cut(versionSection, "\n## ")
			if !found || !strings.Contains(versionSection, "Required: `true`") {
				t.Error("Valkey reference does not mark version as required")
			}
		}
	}
}

func TestValkeyCRDVersionRemainsOptional(t *testing.T) {
	for _, filename := range []string{
		"../../config/crd/bases/nais.io_valkeys.yaml",
		"../../charts/pgrator/templates/crd/nais.io_valkeys.yaml",
	} {
		t.Run(filename, func(t *testing.T) {
			data, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			var crd apiext.CustomResourceDefinition
			if err := yaml.Unmarshal(data, &crd); err != nil {
				t.Fatal(err)
			}
			if len(crd.Spec.Versions) == 0 {
				t.Fatal("CRD has no versions")
			}
			for _, version := range crd.Spec.Versions {
				spec := version.Schema.OpenAPIV3Schema.Properties["spec"]
				if _, ok := spec.Properties["version"]; !ok {
					t.Fatal("CRD missing spec.version")
				}
				if slices.Contains(spec.Required, "version") {
					t.Error("CRD requires spec.version")
				}
			}
		})
	}
}

func TestNativeSpecSchemaRequiredMarkers(t *testing.T) {
	marked := apiext.JSONSchemaProps{Type: "string"}
	if err := (Doc{NativeRequired: true}).ApplyToSchema(nil, &marked); err != nil {
		t.Fatal(err)
	}
	optional := apiext.JSONSchemaProps{Type: "string"}
	if err := (Doc{NativeRequired: false}).ApplyToSchema(nil, &optional); err != nil {
		t.Fatal(err)
	}
	object := apiext.JSONSchemaProps{
		Type:       "object",
		Required:   []string{"existing"},
		Properties: map[string]apiext.JSONSchemaProps{"existing": marked, "field": marked, "optional": optional},
	}
	for _, tc := range []struct {
		name    string
		spec    apiext.JSONSchemaProps
		valid   any
		invalid any
	}{
		{
			name: "spec field", spec: object,
			valid:   map[string]any{"existing": "value", "field": "value"},
			invalid: map[string]any{"existing": "value"},
		},
		{
			name:    "nested object",
			spec:    apiext.JSONSchemaProps{Type: "object", Properties: map[string]apiext.JSONSchemaProps{"nested": object}},
			valid:   map[string]any{"nested": map[string]any{"existing": "value", "field": "value"}},
			invalid: map[string]any{"nested": map[string]any{"existing": "value"}},
		},
		{
			name: "array item",
			spec: apiext.JSONSchemaProps{
				Type: "object", Properties: map[string]apiext.JSONSchemaProps{
					"list": {Type: "array", Items: &apiext.JSONSchemaPropsOrArray{Schema: &object}},
				},
			},
			valid:   map[string]any{"list": []any{map[string]any{"existing": "value", "field": "value"}}},
			invalid: map[string]any{"list": []any{map[string]any{"existing": "value"}}},
		},
		{
			name: "map value",
			spec: apiext.JSONSchemaProps{
				Type: "object", AdditionalProperties: &apiext.JSONSchemaPropsOrBool{Allows: true, Schema: &object},
			},
			valid:   map[string]any{"entry": map[string]any{"existing": "value", "field": "value"}},
			invalid: map[string]any{"entry": map[string]any{"existing": "value"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := apiext.JSONSchemaProps{Properties: map[string]apiext.JSONSchemaProps{"spec": tc.spec}}
			original, err := deepCopySchema(source)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := nativeSpecSchema(source)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(source, original) {
				t.Error("native markers changed the source CRD schema")
			}
			reapplied, err := nativeSpecSchema(apiext.JSONSchemaProps{
				Properties: map[string]apiext.JSONSchemaProps{"spec": spec},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(spec, reapplied) {
				t.Error("reapplying native markers changed the schema")
			}
			clearExamples(&spec)
			doc, err := schemaToMap(spec)
			if err != nil {
				t.Fatal(err)
			}
			sch, err := gojsonschema.NewSchema(gojsonschema.NewGoLoader(doc))
			if err != nil {
				t.Fatal(err)
			}
			mustValidate(t, sch, tc.valid, true, "marked field present; unmarked and false-marked fields optional")
			mustValidate(t, sch, tc.invalid, false, "marked field missing")
		})
	}
}

func TestNativeSpecSchemaRejectsMalformedMarker(t *testing.T) {
	_, err := nativeSpecSchema(apiext.JSONSchemaProps{
		Properties: map[string]apiext.JSONSchemaProps{
			"spec": {
				Type: "object", Properties: map[string]apiext.JSONSchemaProps{
					"field": {Type: "string", Example: &apiext.JSON{Raw: []byte(`{"NativeRequired":"invalid"}`)}},
				},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "field") {
		t.Fatalf("expected contextual marker error, got %v", err)
	}
}

func TestReferenceDocumentationMapValues(t *testing.T) {
	version := apiext.JSONSchemaProps{Type: "string"}
	if err := (Doc{NativeRequired: true}).ApplyToSchema(nil, &version); err != nil {
		t.Fatal(err)
	}
	object := apiext.JSONSchemaProps{
		Type: "object", Properties: map[string]apiext.JSONSchemaProps{
			"version":  version,
			"optional": {Type: "string"},
		},
	}
	mapSchema := func(value apiext.JSONSchemaProps) apiext.JSONSchemaProps {
		return apiext.JSONSchemaProps{
			Type: "object", AdditionalProperties: &apiext.JSONSchemaPropsOrBool{Allows: true, Schema: &value},
		}
	}
	arraySchema := func(item apiext.JSONSchemaProps) apiext.JSONSchemaProps {
		return apiext.JSONSchemaProps{Type: "array", Items: &apiext.JSONSchemaPropsOrArray{Schema: &item}}
	}
	mixed := mapSchema(object)
	mixed.Properties = map[string]apiext.JSONSchemaProps{"fixed": {Type: "string"}}
	for _, tc := range []struct {
		name       string
		settings   apiext.JSONSchemaProps
		valuePath  string
		extraPaths []string
	}{
		{name: "object values", settings: mapSchema(object), valuePath: "settings[key]"},
		{
			name: "fixed and arbitrary keys", settings: mixed,
			valuePath: "settings[key]", extraPaths: []string{"settings.fixed"},
		},
		{name: "nested maps", settings: mapSchema(mapSchema(object)), valuePath: "settings[key][key]"},
		{name: "array of maps", settings: arraySchema(mapSchema(object)), valuePath: "settings[][key]"},
		{name: "map of arrays", settings: mapSchema(arraySchema(object)), valuePath: "settings[key][]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := nativeSpecSchema(apiext.JSONSchemaProps{
				Properties: map[string]apiext.JSONSchemaProps{
					"spec": {Type: "object", Properties: map[string]apiext.JSONSchemaProps{"settings": tc.settings}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			filename := filepath.Join(t.TempDir(), "reference.md")
			renderer := referenceRenderer{example: map[string]any{"spec": map[string]any{}}}
			if err := Write(renderer.render, "../../doc/templates/nais.io/v1/valkey/reference.md", filename, spec); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			reference := string(data)
			for _, field := range []struct {
				path     string
				required bool
			}{
				{path: tc.valuePath + ".version", required: true},
				{path: tc.valuePath + ".optional", required: false},
			} {
				_, section, found := strings.Cut(reference, " "+field.path+"\n")
				section, _, _ = strings.Cut(section, "\n#")
				if !found || !strings.Contains(section, fmt.Sprintf("Required: `%t`", field.required)) {
					t.Errorf("reference missing %s with required=%t:\n%s", field.path, field.required, reference)
				}
			}
			for _, path := range tc.extraPaths {
				if !strings.Contains(reference, " "+path+"\n") {
					t.Errorf("reference missing %s", path)
				}
			}
		})
	}
}

func TestPostgresDocumentationUsesNativeManifest(t *testing.T) {
	outputDir := t.TempDir()
	cfg := &Config{
		APIDir: "../../pkg/api/...", OutputDir: outputDir,
		TemplateDir: "../../doc/templates",
	}
	if err := runWithConfig(cfg); err != nil {
		t.Fatalf("runWithConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(outputDir, "nais.io/v1/postgres/example.md"))
	if err != nil {
		t.Fatal(err)
	}
	example := string(data)
	for _, field := range []string{"version: v1", "type: Postgres", "name: mypostgres"} {
		if !strings.Contains(example, field) {
			t.Errorf("Postgres example missing %q", field)
		}
	}
	for _, field := range []string{"apiVersion:", "kind: Postgres", "metadata:"} {
		if strings.Contains(example, field) {
			t.Errorf("Postgres example contains Kubernetes field %q", field)
		}
	}
}

// TestAllSchemaAggregatesEveryFile checks that all.json exists, parses, and
// every $ref points to an advertised schema file that was actually generated.
func TestAllSchemaAggregatesEveryFile(t *testing.T) {
	schemaDir := generateSchemas(t)

	b, err := os.ReadFile(filepath.Join(schemaDir, "all.json"))
	if err != nil {
		t.Fatalf("reading all.json: %v", err)
	}

	var doc struct {
		OneOf []struct {
			Ref string `json:"$ref"`
		} `json:"oneOf"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parsing all.json: %v", err)
	}

	if len(doc.OneOf) != len(NativeKinds) {
		t.Fatalf("all.json has %d entries, want %d native kinds", len(doc.OneOf), len(NativeKinds))
	}

	refs := make([]string, 0, len(doc.OneOf))
	for _, entry := range doc.OneOf {
		if entry.Ref == "" {
			t.Fatalf("empty $ref in all.json")
		}
		refs = append(refs, entry.Ref)

		if _, err := os.Stat(filepath.Join(schemaDir, entry.Ref)); err != nil {
			t.Errorf("all.json references %s, but it does not exist: %v", entry.Ref, err)
		}
	}
	for gvk := range ExampleRegistry {
		_, published := NativeKinds[gvk]
		_, err := os.Stat(filepath.Join(schemaDir, schemaFilename(gvk)))
		if published && err != nil {
			t.Errorf("missing native schema for %s: %v", gvk.Kind, err)
		}
		if !published && !os.IsNotExist(err) {
			t.Errorf("unexpected schema for %s: %v", gvk.Kind, err)
		}
	}

	sorted := append([]string(nil), refs...)
	sort.Strings(sorted)
	for i := range refs {
		if refs[i] != sorted[i] {
			t.Errorf("all.json refs are not sorted: %v", refs)
			break
		}
	}
}
