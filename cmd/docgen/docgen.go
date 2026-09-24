package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/imdario/mergo"
	"github.com/nais/pgrator/pkg/api"
	datav1 "github.com/nais/pgrator/pkg/api/datav1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-tools/pkg/crd"
	crd_markers "sigs.k8s.io/controller-tools/pkg/crd/markers"
	"sigs.k8s.io/controller-tools/pkg/loader"
	"sigs.k8s.io/controller-tools/pkg/markers"
)

// Generate documentation for Nais CRDs

// ExampleRegistry maps CRD GroupVersionKind to functions that return example resources.
// Add new CRD examples here when adding new CRDs to the project.
var ExampleRegistry = map[schema.GroupVersionKind]func() api.NaisObject{
	{
		Group:   v1.GroupVersion.Group,
		Version: v1.GroupVersion.Version,
		Kind:    "Postgres",
	}: v1.ExamplePostgresForDocumentation,
	{
		Group:   v1.GroupVersion.Group,
		Version: v1.GroupVersion.Version,
		Kind:    "PostgresBinding",
	}: v1.ExamplePostgresBindingForDocumentation,
	{
		Group:   v1.GroupVersion.Group,
		Version: v1.GroupVersion.Version,
		Kind:    "PostgresAccess",
	}: v1.ExamplePostgresAccessForDocumentation,
	{
		Group:   v1.GroupVersion.Group,
		Version: v1.GroupVersion.Version,
		Kind:    "Valkey",
	}: v1.ExampleValkeyForDocumentation,
	{
		Group:   v1.GroupVersion.Group,
		Version: v1.GroupVersion.Version,
		Kind:    "OpenSearch",
	}: v1.ExampleOpenSearchForDocumentation,
}

// NativeKinds are kinds that nais apply/validate also accept in the stripped,
// flat native manifest form (version/type/name/labels/spec, no kind/metadata
// wrapper), mapped to the accepted version.
var NativeKinds = map[schema.GroupVersionKind]string{
	{Group: v1.GroupVersion.Group, Version: v1.GroupVersion.Version, Kind: "Valkey"}:     "v1",
	{Group: v1.GroupVersion.Group, Version: v1.GroupVersion.Version, Kind: "OpenSearch"}: "v1",
}

// ExcludedKinds are API types that must not appear in generated documentation.
var ExcludedKinds = map[schema.GroupVersionKind]struct{}{
	{
		Group:   datav1.GroupVersion.Group,
		Version: datav1.GroupVersion.Version,
		Kind:    "Postgres",
	}: {},
	{
		Group:   v1.GroupVersion.Group,
		Version: v1.GroupVersion.Version,
		Kind:    "PostgresInstance",
	}: {},
}

type Renderer func(w io.Writer, level int, jsonpath string, key string, parent, node apiext.JSONSchemaProps)

type Config struct {
	// APIDir is the directory containing the CRD type definitions
	APIDir string
	// OutputDir is the directory where generated documentation will be written
	OutputDir string
	// TemplateDir is the directory containing templates for each kind
	TemplateDir string
	// JSONSchema is the directory for OpenAPI JSON schema output (optional)
	JSONSchema string
}

type Doc struct {
	// Which cluster(s) or environments the feature is available in
	Availability string `marker:"Availability,optional"`
	// Adds Default values to documentation
	Default string `marker:"Default,optional"`
	// Deprecated declares the field obsolete
	Deprecated bool `marker:"Deprecated,optional"`
	// Experimental declares the field as subject to instability, change, or removal
	Experimental bool `marker:"Experimental,optional"`
	// Hidden declares the field as hidden from reference and example documentation
	Hidden bool `marker:"Hidden,optional"`
	// Immutable declares the field as immutable
	Immutable bool `marker:"Immutable,optional"`
	// Links to documentation or other information
	// Use semicolons to separate multiple marker values.
	Link []string `marker:"Link,optional"`
	// Tenants declares which tenants the field is available for.
	// Empty means all tenants.
	// Use semicolons to separate multiple marker values.
	Tenants []string `marker:"Tenants,optional"`
}

type ExtDoc struct {
	Availability string
	Default      string
	Deprecated   bool
	Description  string
	Enum         []string
	Experimental bool
	Hidden       bool
	Immutable    bool
	Level        int
	Link         []string
	Maximum      *float64
	Minimum      *float64
	Path         string
	Pattern      string
	Required     bool
	Tenants      []string
	Title        string
	Type         string
}

// Hijack the "example" field for custom documentation fields
func (m Doc) ApplyToSchema(props *apiext.JSONSchemaProps) error {
	d := &Doc{}
	if props.Example != nil {
		err := json.Unmarshal(props.Example.Raw, d)
		if err != nil {
			return err
		}
	}
	err := mergo.Merge(d, m)
	if err != nil {
		return err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	props.Example = &apiext.JSON{Raw: b}
	return nil
}

func main() {
	err := run()
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg := &Config{}
	pflag.StringVar(
		&cfg.APIDir,
		"api-dir",
		cfg.APIDir,
		"directory containing CRD type definitions",
	)
	pflag.StringVar(
		&cfg.OutputDir,
		"output-dir",
		cfg.OutputDir,
		"directory for generated documentation output",
	)
	pflag.StringVar(
		&cfg.TemplateDir,
		"template-dir",
		cfg.TemplateDir,
		"directory containing templates for each kind",
	)
	pflag.StringVar(
		&cfg.JSONSchema,
		"openapi-output",
		cfg.JSONSchema,
		"if set, generate json schema to the provided directory",
	)
	pflag.Parse()

	return runWithConfig(cfg)
}

func runWithConfig(cfg *Config) error {
	if cfg.APIDir == "" {
		return fmt.Errorf("--api-dir is required")
	}
	if cfg.OutputDir == "" {
		return fmt.Errorf("--output-dir is required")
	}
	if cfg.TemplateDir == "" {
		return fmt.Errorf("--template-dir is required")
	}

	packages, err := loader.LoadRoots(cfg.APIDir)
	if err != nil {
		return err
	}

	registry := &markers.Registry{}
	collector := &markers.Collector{
		Registry: registry,
	}

	err = crd_markers.Register(registry)
	if err != nil {
		return err
	}

	err = registry.Define("nais:doc", markers.DescribesField, Doc{})
	if err != nil {
		return fmt.Errorf("register marker: %w", err)
	}

	typechecker := &loader.TypeChecker{}
	pars := &crd.Parser{
		Collector: collector,
		Checker:   typechecker,
	}

	registerPackageOverrides(pars)

	for _, pkg := range packages {
		pars.NeedPackage(pkg)
	}

	metav1Pkg := crd.FindMetav1(packages)
	if metav1Pkg == nil {
		return fmt.Errorf("no objects in the roots, since nothing imported metav1")
	}

	kubeKinds := crd.FindKubeKinds(pars, metav1Pkg)
	if len(kubeKinds) == 0 {
		return fmt.Errorf("no objects in the roots")
	}

	var schemaFiles []string

	for _, gk := range kubeKinds {
		pars.NeedCRDFor(gk, nil)

		// Find all packages for this GroupKind (there may be multiple versions)
		var matchingPackages []*loader.Package
		for p, gv := range pars.GroupVersions {
			if gv.Group == gk.Group {
				matchingPackages = append(matchingPackages, p)
			}
		}
		if len(matchingPackages) == 0 {
			return fmt.Errorf("no package found for kind %s", gk.Kind)
		}

		// Process each version
		for _, pkg := range matchingPackages {
			filename, generated, err := processKindVersion(cfg, pars, gk, pkg)
			if err != nil {
				return err
			}
			if !generated {
				continue
			}
			if filename != "" {
				schemaFiles = append(schemaFiles, filename)
			}
		}
	}

	if cfg.JSONSchema != "" {
		if err := writeAllSchema(cfg.JSONSchema, schemaFiles); err != nil {
			return err
		}
	}

	return nil
}

// registerPackageOverrides configures crd.Parser overrides for well-known
// Kubernetes types that don't carry their own validation markers.
func registerPackageOverrides(pars *crd.Parser) {
	pars.PackageOverrides = make(map[string]crd.PackageOverride)

	intstr := "k8s.io/apimachinery/pkg/util/intstr"
	if override, ok := crd.KnownPackages[intstr]; ok {
		pars.PackageOverrides[intstr] = override
	}

	quantity := "k8s.io/apimachinery/pkg/api/resource"
	if override, ok := crd.KnownPackages[quantity]; ok {
		pars.PackageOverrides[quantity] = override
	}

	// Only override the Time-ish types with their well-known string encodings;
	// unlike crd.KnownPackages, leave ObjectMeta to be parsed normally so its
	// full field set (name, namespace, labels, ...) is still available for the
	// published JSON schema.
	metav1Package := "k8s.io/apimachinery/pkg/apis/meta/v1"
	pars.PackageOverrides[metav1Package] = func(p *crd.Parser, pkg *loader.Package) {
		p.Schemata[crd.TypeIdent{Name: "Time", Package: pkg}] = apiext.JSONSchemaProps{
			Type:   "string",
			Format: "date-time",
		}
		p.Schemata[crd.TypeIdent{Name: "MicroTime", Package: pkg}] = apiext.JSONSchemaProps{
			Type:   "string",
			Format: "date-time",
		}
		p.Schemata[crd.TypeIdent{Name: "Duration", Package: pkg}] = apiext.JSONSchemaProps{
			Type: "string",
		}
		p.AddPackage(pkg)
	}
}

// processKindVersion renders markdown docs and (optionally) the JSON schema
// for a single Kind/version. It returns the generated schema filename (empty
// if JSON schema output is disabled) and whether anything was generated at
// all (false for excluded kinds).
func processKindVersion(
	cfg *Config,
	pars *crd.Parser,
	gk schema.GroupKind,
	pkg *loader.Package,
) (filename string, generated bool, err error) {
	gv := pars.GroupVersions[pkg]
	log := slog.With("kind", gk.Kind, "group", gk.Group, "version", gv.Version)

	gvk := schema.GroupVersionKind{
		Group:   gk.Group,
		Version: gv.Version,
		Kind:    gk.Kind,
	}
	exampleFunc, ok := ExampleRegistry[gvk]
	if !ok {
		if _, excluded := ExcludedKinds[gvk]; excluded {
			return "", false, nil
		}
		return "", false, fmt.Errorf(
			"'%s/%s/%s' is not supported; "+
				"must be registered in ExampleRegistry config in docgen.go",
			gk.Group, gv.Version, gk.Kind,
		)
	}

	schemata, ok := pars.FlattenedSchemata[crd.TypeIdent{Package: pkg, Name: gk.Kind}]
	if !ok {
		return "", false, fmt.Errorf(
			"schema generation failed for %s/%s/%s; "+
				"double check the syntax of doctags (+nais:* and +kubebuilder:*)",
			gk.Group, gv.Version, gk.Kind,
		)
	}

	// rawExample is the CR decoded to map[string]any; getStructSubPath walks it with reflection.
	var rawExample any
	if err := marshalToInterface(&rawExample, exampleFunc()); err != nil {
		return "", false, err
	}
	// manifestExample is the naisified full manifest (version:/type:/name:/spec:) rendered in example.md.
	manifestExample := naisifyManifest(rawExample)

	// Use group/version/kind directory structure
	kindLower := strings.ToLower(gk.Kind)
	subDir := filepath.Join(gk.Group, gv.Version, kindLower)

	outputDir := filepath.Join(cfg.OutputDir, subDir)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", false, fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	templateDir := filepath.Join(cfg.TemplateDir, subDir)

	referenceTemplate := filepath.Join(templateDir, "reference.md")
	referenceOutput := filepath.Join(outputDir, "reference.md")
	referenceRenderer := referenceRenderer{example: rawExample}
	err = Write(referenceRenderer.render, referenceTemplate, referenceOutput, schemata.Properties["spec"])
	if err != nil {
		return "", false, fmt.Errorf("failed to write reference doc for %s: %w", gk.Kind, err)
	}

	exampleTemplate := filepath.Join(templateDir, "example.md")
	exampleOutput := filepath.Join(outputDir, "example.md")
	exampleRenderer := exampleRenderer{manifest: manifestExample}
	if err := Write(exampleRenderer.render, exampleTemplate, exampleOutput, schemata); err != nil {
		return "", false, fmt.Errorf("failed to write example doc for %s: %w", gk.Kind, err)
	}

	if cfg.JSONSchema != "" {
		filename, err = writeJSONSchema(cfg.JSONSchema, gvk, schemata)
		if err != nil {
			return "", false, err
		}
	}

	log.Info("Generated documentation", "output", outputDir)

	return filename, true, nil
}

// writeJSONSchema renders the published JSON Schema for the given kind into a
// flat file named "<group>_<version>_<Kind>.json" in outputDir, and returns
// the file's basename (for use in the aggregate all.json). The schema used
// for markdown rendering (schemata) is deep-copied first so that publishing
// never mutates it.
func writeJSONSchema(outputDir string, gvk schema.GroupVersionKind, schemata apiext.JSONSchemaProps) (string, error) {
	group, version, kind := gvk.Group, gvk.Version, gvk.Kind

	published, err := deepCopySchema(schemata)
	if err != nil {
		return "", err
	}

	// The doc generator hijacks "example" to carry doc metadata (see Doc.ApplyToSchema).
	// Strip it so the published schema is clean, standard JSON Schema.
	clearExamples(&published)

	// Make some changes to the schema to make it even more useful for validation etc.
	published = setJSONSchemaEnum(published, "kind", strconv.Quote(kind))
	published = setJSONSchemaEnum(published, "apiVersion", strconv.Quote(group+"/"+version))

	published = setJSONSchemaRequired(published, ".", "kind", "metadata", "apiVersion")
	published = setJSONSchemaRequired(published, "metadata", "name")

	additionalPropertiesFalse(&published)

	crdSchema, err := schemaToMap(published)
	if err != nil {
		return "", err
	}

	var doc map[string]any
	if nativeVersion, ok := NativeKinds[gvk]; ok {
		properties, _ := crdSchema["properties"].(map[string]any)
		specSchema := properties["spec"]
		doc = map[string]any{
			"oneOf": []any{crdSchema, nativeEnvelope(kind, nativeVersion, specSchema)},
		}
	} else {
		doc = crdSchema
	}

	doc["$schema"] = "http://json-schema.org/schema#"
	doc["x-kubernetes-group-version-kind"] = []map[string]string{
		{
			"group":   group,
			"kind":    kind,
			"version": version,
		},
	}

	filename := fmt.Sprintf("%s_%s_%s.json", group, version, kind)
	if err := writeIndentedJSON(filepath.Join(outputDir, filename), doc); err != nil {
		return "", err
	}

	return filename, nil
}

// nativeEnvelope builds the flat, stripped native manifest envelope
// (version/type/name/labels/spec) accepted by nais apply/validate for kinds
// in NativeKinds. This mirrors nais/cli's ParseManifest, which requires
// version/type/name, allows an optional labels map, and rejects any other
// top-level field (including "kind"/"metadata", which are CRD-envelope-only).
// specSchema is the already-published spec schema (with additionalProperties:false
// applied recursively), shared as-is.
func nativeEnvelope(kind, version string, specSchema any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"version", "type", "name", "spec"},
		"properties": map[string]any{
			"version": map[string]any{"type": "string", "enum": []string{version}},
			"type":    map[string]any{"type": "string", "enum": []string{kind}},
			"name":    map[string]any{"type": "string"},
			"labels":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"spec":    specSchema,
		},
	}
}

// writeAllSchema writes the aggregate all.json, which oneOf-references every
// generated schema file by its relative filename. filenames need not be
// pre-sorted.
func writeAllSchema(outputDir string, filenames []string) error {
	sorted := slices.Clone(filenames)
	slices.Sort(sorted)

	refs := make([]map[string]string, 0, len(sorted))
	for _, name := range sorted {
		refs = append(refs, map[string]string{"$ref": name})
	}

	doc := map[string]any{"oneOf": refs}
	return writeIndentedJSON(filepath.Join(outputDir, "all.json"), doc)
}

func writeIndentedJSON(path string, doc any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create schema directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// schemaToMap marshals a JSONSchemaProps to a generic map, ready for
// arbitrary composition (e.g. embedding in a oneOf).
func schemaToMap(schemata apiext.JSONSchemaProps) (map[string]any, error) {
	b, err := json.Marshal(schemata)
	if err != nil {
		return nil, err
	}
	inter := make(map[string]any)
	if err := json.Unmarshal(b, &inter); err != nil {
		return nil, err
	}
	return inter, nil
}

// deepCopySchema returns an independent copy of the given schema via a JSON
// round-trip, so callers can mutate the copy without affecting the original
// (e.g. the schema used for markdown rendering).
func deepCopySchema(in apiext.JSONSchemaProps) (apiext.JSONSchemaProps, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return apiext.JSONSchemaProps{}, err
	}
	var out apiext.JSONSchemaProps
	if err := json.Unmarshal(b, &out); err != nil {
		return apiext.JSONSchemaProps{}, err
	}
	return out, nil
}

// clearExamples recursively clears the Example field, which the doc
// generator hijacks to carry documentation metadata (see Doc.ApplyToSchema).
// It must not leak into the published JSON Schema.
func clearExamples(props *apiext.JSONSchemaProps) {
	if props == nil {
		return
	}
	props.Example = nil

	for k, prop := range props.Properties {
		clearExamples(&prop)
		props.Properties[k] = prop
	}
	for k, prop := range props.PatternProperties {
		clearExamples(&prop)
		props.PatternProperties[k] = prop
	}
	if props.Items != nil {
		clearExamples(props.Items.Schema)
		for i := range props.Items.JSONSchemas {
			clearExamples(&props.Items.JSONSchemas[i])
		}
	}
	if props.AdditionalProperties != nil {
		clearExamples(props.AdditionalProperties.Schema)
	}
	for i := range props.AllOf {
		clearExamples(&props.AllOf[i])
	}
	for i := range props.OneOf {
		clearExamples(&props.OneOf[i])
	}
	for i := range props.AnyOf {
		clearExamples(&props.AnyOf[i])
	}
	clearExamples(props.Not)
	for k, prop := range props.Definitions {
		clearExamples(&prop)
		props.Definitions[k] = prop
	}
}

func additionalPropertiesFalse(props *apiext.JSONSchemaProps) {
	if props == nil {
		return
	}
	if props.Type == "object" && props.AdditionalProperties == nil {
		props.AdditionalProperties = &apiext.JSONSchemaPropsOrBool{Allows: false}
	}
	for k, prop := range props.Properties {
		additionalPropertiesFalse(&prop)
		props.Properties[k] = prop
	}
	for k, prop := range props.PatternProperties {
		additionalPropertiesFalse(&prop)
		props.PatternProperties[k] = prop
	}
	if props.Items != nil {
		additionalPropertiesFalse(props.Items.Schema)
		for i := range props.Items.JSONSchemas {
			additionalPropertiesFalse(&props.Items.JSONSchemas[i])
		}
	}
	if props.AdditionalProperties != nil {
		additionalPropertiesFalse(props.AdditionalProperties.Schema)
	}
	if props.AdditionalItems != nil {
		additionalPropertiesFalse(props.AdditionalItems.Schema)
	}
	for i := range props.AllOf {
		additionalPropertiesFalse(&props.AllOf[i])
	}
	for i := range props.OneOf {
		additionalPropertiesFalse(&props.OneOf[i])
	}
	for i := range props.AnyOf {
		additionalPropertiesFalse(&props.AnyOf[i])
	}
	additionalPropertiesFalse(props.Not)
	for k, prop := range props.Dependencies {
		additionalPropertiesFalse(prop.Schema)
		props.Dependencies[k] = prop
	}
	for k, prop := range props.Definitions {
		additionalPropertiesFalse(&prop)
		props.Definitions[k] = prop
	}
}

func marshalToInterface(dst, src any) error {
	data, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func Write(renderer Renderer, tpl string, outFile string, base apiext.JSONSchemaProps) error {
	var err error
	w := os.Stdout
	if len(outFile) > 0 {
		w, err = os.OpenFile(outFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
	}
	mw := &multiwriter{w: w}

	templateEngine, err := template.ParseFiles(tpl)
	if err != nil {
		return err
	}

	err = templateEngine.Execute(mw, nil)
	if err != nil {
		return err
	}

	renderer(mw, 1, "", "", base, base)

	return mw.Error()
}

type multiwriter struct {
	w   io.Writer
	err error
}

func (m *multiwriter) Write(p []byte) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	n, err := m.w.Write(p)
	if err != nil {
		m.err = err
	}
	return n, err
}

func (m *multiwriter) Error() error {
	return m.err
}

func linefmt(format string, args ...any) string {
	format = fmt.Sprintf(format, args...)
	if len(format) == 0 {
		format = "_no value_"
	}
	format = strings.ReplaceAll(format, "``", "_no value_")
	return format + "<br />\n"
}

func floatfmt(f *float64) string {
	if f == nil {
		return "+Inf"
	}
	return strconv.FormatFloat(*f, 'f', 0, 64)
}

func writeList(w io.Writer, list []string) {
	sort.Strings(list)
	max := len(list) - 1
	for i, item := range list {
		if len(item) > 0 {
			_, _ = io.WriteString(w, fmt.Sprintf("`%s`", item))
		} else {
			_, _ = io.WriteString(w, "_(empty string)_")
		}
		if i != max {
			_, _ = io.WriteString(w, ", ")
		}
	}
	_, _ = io.WriteString(w, "<br />\n")
}

func (m ExtDoc) formatStraight(w io.Writer) {
	_, _ = io.WriteString(w, fmt.Sprintf("%s %s", strings.Repeat("#", m.Level), strings.TrimLeft(m.Path, ".")))
	_, _ = io.WriteString(w, "\n")
	if len(m.Description) > 0 {
		_, _ = io.WriteString(w, m.Description)
		_, _ = io.WriteString(w, "\n\n")
	}
	if m.Experimental {
		_, _ = io.WriteString(w, "!!! warning \"Experimental feature\"\n    "+
			"This feature has not undergone much testing, and is subject to API change, instability, or removal.\n\n")
	}
	if m.Deprecated {
		_, _ = io.WriteString(w, "!!! failure \"Deprecated\"\n    "+
			"This feature is deprecated, preserved only for backwards compatibility.\n\n")
	}
	if len(m.Link) > 0 {
		_, _ = io.WriteString(w, "Relevant information:\n\n")
		for _, link := range m.Link {
			u, err := url.Parse(link)
			if err == nil {
				if u.Host == "doc.nais.io" || u.Host == "docs.nais.io" {
					u.Host = "doc.<<tenant()>>.cloud.nais.io"
					link = u.String()
				}
			}
			_, _ = io.WriteString(w, fmt.Sprintf("* [%s](%s)\n", link, link))
		}
		_, _ = io.WriteString(w, "\n")
	}

	if types := strings.Split(m.Type, ","); len(types) > 1 {
		_, _ = io.WriteString(w, linefmt("Type: `%s`", strings.Join(types, "` or `")))
	} else {
		_, _ = io.WriteString(w, linefmt("Type: `%s`", m.Type))
	}
	_, _ = io.WriteString(w, linefmt("Required: `%s`", strconv.FormatBool(m.Required)))
	if m.Immutable {
		_, _ = io.WriteString(w, linefmt("Immutable: `%v`", m.Immutable))
	}
	if len(m.Default) > 0 {
		_, _ = io.WriteString(w, linefmt("Default value: `%v`", m.Default))
	}
	if len(m.Availability) > 0 {
		_, _ = io.WriteString(w, linefmt("Availability: %s", m.Availability))
	}
	if len(m.Pattern) > 0 {
		_, _ = io.WriteString(w, linefmt("Pattern: `%s`", m.Pattern))
	}
	if m.Minimum != m.Maximum {
		min := floatfmt(m.Minimum)
		max := floatfmt(m.Maximum)
		switch {
		case m.Minimum == nil:
			_, _ = io.WriteString(w, linefmt("Maximum value: `%s`", max))
		case m.Maximum == nil:
			_, _ = io.WriteString(w, linefmt("Minimum value: `%s`", min))
		default:
			_, _ = io.WriteString(w, linefmt("Value range: `%s`-`%s`", min, max))
		}
	}
	if len(m.Enum) > 0 {
		_, _ = io.WriteString(w, "Allowed values: ")
		writeList(w, m.Enum)
	}
	_, _ = io.WriteString(w, "\n")
}

func hasRequired(node apiext.JSONSchemaProps, key string) bool {
	if slices.Contains(node.Required, key) {
		return true
	}

	if node.Items == nil {
		return false
	}

	return slices.Contains(node.Items.Schema.Required, key)
}

type exampleRenderer struct {
	manifest any
}

func (r exampleRenderer) render(w io.Writer, level int, jsonpath, key string, parent, node apiext.JSONSchemaProps) {
	buf := bytes.NewBuffer(nil)
	enc := yaml.NewEncoder(buf)
	enc.SetIndent(2)
	_ = enc.Encode(r.manifest)
	_ = enc.Close()

	_, _ = io.WriteString(w, "``` yaml\n")
	_, _ = io.Writer.Write(w, buf.Bytes())
	_, _ = io.WriteString(w, "```\n")
}

type referenceRenderer struct {
	example any
}

func (r referenceRenderer) render(w io.Writer, level int, jsonpath, key string, parent, node apiext.JSONSchemaProps) {
	if jsonpath == ".metadata" || jsonpath == ".status" {
		return
	}

	if len(node.Enum) > 0 {
		node.Type = "enum"
	}

	entry := &ExtDoc{
		Description: strings.TrimSpace(node.Description),
		Level:       level,
		Maximum:     node.Maximum,
		Minimum:     node.Minimum,
		Path:        jsonpath,
		Pattern:     node.Pattern,
		Required:    hasRequired(parent, key),
		Title:       key,
		Type:        node.Type,
	}

	// Override children when encountering an array
	if node.Type == "array" {
		node.Properties = node.Items.Schema.Properties
		jsonpath += "[]"
	}

	if node.XIntOrString {
		t := make([]string, len(node.AnyOf))
		for i, v := range node.AnyOf {
			t[i] = v.Type
		}

		entry.Type = strings.Join(t, ",")
	}

	if len(node.Enum) > 0 {
		entry.Enum = make([]string, 0, len(entry.Enum))
		for _, v := range node.Enum {
			s := ""
			err := json.Unmarshal(v.Raw, &s)
			if err != nil {
				s = string(v.Raw)
			}
			entry.Enum = append(entry.Enum, s)
		}
	}

	if node.Example != nil {
		d := &Doc{}
		err := json.Unmarshal(node.Example.Raw, d)
		if err == nil {
			entry.Availability = d.Availability
			entry.Default = d.Default
			entry.Deprecated = d.Deprecated
			entry.Experimental = d.Experimental
			entry.Hidden = d.Hidden
			entry.Immutable = d.Immutable
			entry.Link = d.Link
			entry.Tenants = d.Tenants
		} else {
			slog.Error("unable to merge structs", "error", err)
		}
	}

	if entry.Hidden {
		return
	}

	isTenantSpecific := len(entry.Tenants) > 0
	if isTenantSpecific {
		if len(entry.Tenants) == 1 {
			tenant := entry.Tenants[0]
			_, _ = io.WriteString(w, "{%- if tenant() == \""+tenant+"\" %}")
		} else {
			tenants := strings.Join(entry.Tenants, "\", \"")
			_, _ = io.WriteString(w, "{%- if tenant() in (\""+tenants+"\") %}")
		}
		_, _ = io.WriteString(w, "\n")
	}

	if len(jsonpath) > 0 {
		entry.formatStraight(w)

		example, err := getStructSubPath("spec"+jsonpath, r.example)
		if err == nil {
			_, _ = io.WriteString(w, "??? example\n")
			_, _ = io.WriteString(w, "    ``` yaml\n")
			buf := bytes.NewBuffer(nil)
			enc := yaml.NewEncoder(buf)
			enc.SetIndent(2)
			_ = enc.Encode(example)
			scan := bufio.NewScanner(buf)
			for scan.Scan() {
				_, _ = io.WriteString(w, "    "+scan.Text()+"\n")
			}
			_, _ = io.WriteString(w, "    ```\n\n")
		}
	}

	if len(node.Properties) == 0 {
		if isTenantSpecific {
			_, _ = io.WriteString(w, "{%- endif %}\n")
		}
		return
	}

	keys := make([]string, 0)
	for k := range node.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		r.render(w, level+1, jsonpath+"."+k, k, node, node.Properties[k])
	}

	if isTenantSpecific {
		_, _ = io.WriteString(w, "{%- endif %}\n")
	}
}

func getStructSubPath(keyWithDots string, obj any) (any, error) {
	structure := make(map[string]any)
	var leaf any = structure

	keySlice := strings.Split(keyWithDots, ".")
	v := reflect.ValueOf(obj)

	resolve := func(v reflect.Value) reflect.Value {
		if v.Kind() == reflect.Pointer {
			return v.Elem()
		}
		return v
	}

	max := len(keySlice) - 1
	for i, key := range keySlice {
		key = strings.TrimRight(key, "[]")

		if len(key) == 0 {
			break
		}

		v = resolve(v)

		var added any

		switch v.Kind() {
		case reflect.Map:
			drilldown := func() error {
				for _, k := range v.MapKeys() {
					if k.String() == key {
						v = v.MapIndex(k).Elem()
						return nil
					}
				}
				return fmt.Errorf("key not found")
			}

			err := drilldown()
			if err != nil {
				return nil, err
			}
		}

		v = resolve(v)

		switch {
		case v.Kind() == reflect.Slice:
			fallthrough
		case i == max:
			added = resolve(v).Interface()

		case v.Kind() == reflect.Map:
			added = make(map[string]any)
		}

		switch typedleaf := leaf.(type) {
		case map[string]any:
			typedleaf[key] = added
		case []any:
			typedleaf[0] = added
		}

		leaf = added
		if v.Kind() == reflect.Slice {
			break
		}
	}

	return structure, nil
}

func runOnJSONSchemaProperty(
	root apiext.JSONSchemaProps,
	path string,
	f func(*apiext.JSONSchemaProps),
) apiext.JSONSchemaProps {
	if path == "." {
		f(&root)
		return root
	}

	p := strings.Split(path, ".")
	obj := root.Properties[p[0]]
	if len(p) == 1 {
		f(&obj)
	} else {
		runOnJSONSchemaProperty(obj, strings.Join(p[1:], "."), f)
	}
	root.Properties[p[0]] = obj
	return root
}

func setJSONSchemaEnum(root apiext.JSONSchemaProps, path string, value string) apiext.JSONSchemaProps {
	return runOnJSONSchemaProperty(root, path, func(obj *apiext.JSONSchemaProps) {
		obj.Enum = append(obj.Enum, apiext.JSON{
			Raw: []byte(value),
		})
	})
}

func setJSONSchemaRequired(root apiext.JSONSchemaProps, path string, values ...string) apiext.JSONSchemaProps {
	return runOnJSONSchemaProperty(root, path, func(obj *apiext.JSONSchemaProps) {
		if obj.Properties == nil {
			obj.Properties = make(map[string]apiext.JSONSchemaProps)
		}

		for _, val := range values {
			if _, ok := obj.Properties[val]; !ok {
				obj.Properties[val] = apiext.JSONSchemaProps{
					Type: "string",
				}
			}
			obj.Required = append(obj.Required, val)
		}
	})
}

func naisifyManifest(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	var old map[string]any
	err = json.Unmarshal(b, &old)
	if err != nil {
		panic(err)
	}

	if old["kind"] != "OpenSearch" && old["kind"] != "Valkey" {
		return old
	}

	ret := struct {
		Version string         `yaml:"version"`
		Type    string         `yaml:"type"`
		Name    string         `yaml:"name"`
		Labels  map[string]any `yaml:"labels,omitempty"`
		Spec    any            `yaml:"spec"`
	}{}
	for k, v := range old {
		switch k {
		case "apiVersion":
			ret.Version = strings.TrimPrefix(v.(string), "nais.io/")
		case "kind":
			ret.Type = v.(string)
		case "metadata":
			if m, ok := v.(map[string]any); ok {
				ret.Name = m["name"].(string)
				if labels, ok := m["labels"].(map[string]any); ok {
					ret.Labels = labels
				}
			}
		case "spec":
			ret.Spec = v
		}
	}

	if ret.Labels != nil {
		delete(ret.Labels, "team")
	}
	return ret
}
