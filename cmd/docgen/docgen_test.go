package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/xeipuuv/gojsonschema"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
			if gvk.Kind == valkeyKind {
				delete(spec, "version")     // Not accepted by the native Valkey mutation.
				delete(spec, "persistence") // Parsed but ignored by the CLI.
			}
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
				spec["version"] = "9.1"
				mustValidate(t, sch, manifest, false, "CRD-only Valkey version")
				delete(spec, "version")
				spec["persistence"] = map[string]any{"disabled": true}
				mustValidate(t, sch, manifest, false, "Valkey persistence ignored by CLI")
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

func TestNativeValkeyDocumentationOmitsCRDOnlyVersion(t *testing.T) {
	outputDir := t.TempDir()
	cfg := &Config{
		APIDir: "../../pkg/api/...", OutputDir: outputDir,
		TemplateDir: "../../doc/templates",
	}
	if err := runWithConfig(cfg); err != nil {
		t.Fatalf("runWithConfig: %v", err)
	}
	for filename, excludedFields := range map[string][]string{
		"reference.md": {"## version", "## persistence"},
		"example.md":   {"  version:", "  persistence:"},
	} {
		data, err := os.ReadFile(filepath.Join(outputDir, "nais.io/v1/valkey", filename))
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range excludedFields {
			if strings.Contains(string(data), field) {
				t.Errorf("%s describes unsupported native field %q", filename, field)
			}
		}
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
// every $ref points to a file that was actually generated.
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
