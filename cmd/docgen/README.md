# docgen

The docgen tool automatically:
- Discovers all CRDs in the specified API directory via `+kubebuilder:object:root=true` markers
- Auto-detects the group and version from the package's `GroupVersion` variable
- Generates markdown documentation for each CRD that has a registered example function
- Optionally generates standalone JSON Schemas for the CRDs, suitable for publishing and
  consumption by `nais validate`/`nais apply` and editors (yaml-language-server)

## Command Line Usage

```shell
go run cmd/docgen/docgen.go \
    --api-dir ./pkg/api/...      \  # Directory containing CRD type definitions
    --output-dir doc/output      \  # Output directory for generated docs
    --template-dir doc/templates \  # Directory containing templates
    --openapi-output ./schemas      # (Optional) Generate JSON schema
```

## Directory Structure

Templates and output follow the directory structure `<group>/<version>/<kind>/`.

For example, the `Postgres` CRD (group `nais.io`, version `v1`) with the above flags uses:
- Templates: `doc/templates/nais.io/v1/postgres/`
- Output: `doc/output/nais.io/v1/postgres/`

## Register example functions

Register the example function in `ExampleRegistry` in [docgen.go](docgen.go):

```go
var ExampleRegistry = map[schema.GroupVersionKind]func() object.NaisObject{
   {Group: "nais.io", Version: "v1", Kind: "Postgres"}: v1.ExamplePostgresForDocumentation,
}
```

## JSON Schema output

When `--openapi-output <dir>` is set, docgen additionally writes one standalone JSON Schema
file per CRD directly into `<dir>` (flat, no subdirectories), named:

```
<group>_<version>_<Kind>.json
```

e.g. `nais.io_v1_Postgres.json`. This matches the naming convention used by nais/liberator for
its own published schemas, since both sets of files are published to the same GCS bucket
(`gs://nais-json-schema-2c91`, served at `https://storage.googleapis.com/nais-json-schema-2c91/`).

Each schema:
- Is a standard JSON Schema (draft-04-ish, as emitted by controller-tools), with `additionalProperties: false`
  enforced recursively.
- Requires only `kind`, `apiVersion`, and `metadata` at the root, and only `metadata.name` — matching what the
  Kubernetes apiserver actually enforces (the CRDs in `config/crd/bases/*.yaml` do not require
  `metadata.labels.team` or `metadata.namespace`), so the published schema is never stricter than reality.
- Does **not** contain the generator's internal `example` field, which is hijacked by `docgen.go` to carry
  documentation metadata (`Doc`/`ApplyToSchema`) and is stripped before publishing (`clearExamples`). The
  in-memory schema used to render markdown docs is left untouched (schemas are deep-copied first).

### Native (stripped) manifest support

`nais apply`/`nais validate` accept two forms for some kinds:

1. The CRD form: `apiVersion: nais.io/v1`, `kind: ...`, `metadata: {name, labels, ...}`, `spec: ...`.
2. A stripped, flat "native" form used by `nais apply` for `Valkey` and `OpenSearch` only (no native
   Postgres, PostgresBinding, or PostgresAccess support in the CLI yet). This mirrors nais/cli's
   `ParseManifest`, which requires `version`, `type`, `name` (in that order), allows an optional `labels`
   map, and rejects any other top-level field:
   `version: v1`, `type: ...`, `name: ...`, `labels: {...}`, `spec: ...`. Note there is no `kind` or
   `metadata` wrapper in this form.

The spec vocabulary is identical in both forms. Kinds that support the native form are registered in the
`NativeKinds` map in [docgen.go](docgen.go), mapping their `schema.GroupVersionKind` to the accepted
`version` string. For those kinds, the published root schema is `oneOf: [<CRD envelope>, <native envelope>]`;
for all other kinds, the root schema is just the plain CRD envelope. The root
`x-kubernetes-group-version-kind` extension is present in both cases.

### Aggregate schema

An `all.json` file is also written to `<dir>`, referencing every generated schema file via `$ref`:

```json
{"oneOf": [{"$ref": "nais.io_v1_OpenSearch.json"}, {"$ref": "nais.io_v1_Postgres.json"}, ...]}
```

The refs are plain relative filenames, which resolve correctly once the files are served from the same
bucket/path — this is how the `nais` CLI is expected to consume the full set of schemas.
