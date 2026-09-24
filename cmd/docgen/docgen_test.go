package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
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

// TestExamplesValidateAgainstGeneratedSchemas is the key contract test: every
// example in ExampleRegistry must validate against its own generated JSON
// schema, in both the CRD form and (where applicable) the native form used by
// nais apply/validate.
func TestExamplesValidateAgainstGeneratedSchemas(t *testing.T) {
	schemaDir := generateSchemas(t)

	for gvk, exampleFunc := range ExampleRegistry {
		t.Run(gvk.Kind, func(t *testing.T) {
			sch := loadSchema(t, schemaDir, schemaFilename(gvk))

			var rawExample any
			if err := marshalToInterface(&rawExample, exampleFunc()); err != nil {
				t.Fatalf("marshalToInterface: %v", err)
			}

			mustValidate(t, sch, rawExample, true, "CRD form")

			nativeVersion, isNative := NativeKinds[gvk]
			if !isNative {
				return
			}

			m, ok := rawExample.(map[string]any)
			if !ok {
				t.Fatalf("example is not an object: %T", rawExample)
			}
			metadata, _ := m["metadata"].(map[string]any)

			native := map[string]any{
				"version": nativeVersion,
				"type":    gvk.Kind,
				"name":    metadata["name"],
				"spec":    m["spec"],
			}
			mustValidate(t, sch, native, true, "native form")

			// An unknown top-level field in the native form must fail
			// (additionalProperties: false).
			invalid := map[string]any{
				"version":      nativeVersion,
				"type":         gvk.Kind,
				"name":         metadata["name"],
				"spec":         m["spec"],
				"unknownField": "boom",
			}
			mustValidate(t, sch, invalid, false, "native form with unknown field")
		})
	}
}

// TestUnknownSpecFieldFailsValidation ensures the generated Postgres schema
// actually enforces additionalProperties:false in the spec, i.e. it is a
// meaningful contract and not just permissive boilerplate. Postgres has no
// native form, so this only exercises the CRD envelope.
func TestUnknownSpecFieldFailsValidation(t *testing.T) {
	schemaDir := generateSchemas(t)

	gvk := schema.GroupVersionKind{Group: v1.GroupVersion.Group, Version: v1.GroupVersion.Version, Kind: "Postgres"}
	sch := loadSchema(t, schemaDir, schemaFilename(gvk))

	var rawExample any
	if err := marshalToInterface(&rawExample, v1.ExamplePostgresForDocumentation()); err != nil {
		t.Fatalf("marshalToInterface: %v", err)
	}

	m, ok := rawExample.(map[string]any)
	if !ok {
		t.Fatalf("example is not an object: %T", rawExample)
	}
	spec, ok := m["spec"].(map[string]any)
	if !ok {
		t.Fatalf("example spec is not an object: %T", m["spec"])
	}
	spec["unknownSpecField"] = "boom"

	mustValidate(t, sch, m, false, "Postgres example with unknown spec field")
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

	if len(doc.OneOf) != len(ExampleRegistry) {
		t.Fatalf("all.json has %d entries, want %d (one per ExampleRegistry kind)", len(doc.OneOf), len(ExampleRegistry))
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

	sorted := append([]string(nil), refs...)
	sort.Strings(sorted)
	for i := range refs {
		if refs[i] != sorted[i] {
			t.Errorf("all.json refs are not sorted: %v", refs)
			break
		}
	}
}
