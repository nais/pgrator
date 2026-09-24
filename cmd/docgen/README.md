# docgen

The docgen tool automatically:
- Discovers all CRDs in the specified API directory via `+kubebuilder:object:root=true` markers
- Auto-detects the group and version from the package's `GroupVersion` variable
- Generates markdown documentation for each CRD that has a registered example function
- Optionally generates JSON Schemas for public native Nais manifests, for CLI and editor validation

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

When `--openapi-output <dir>` is set, docgen writes schemas for the public native manifest kinds
listed in `NativeKinds` in [docgen.go](docgen.go) (currently Postgres, Valkey and OpenSearch). CRD-only and
internal kinds such as PostgresAccess and PostgresBinding are not published. Files are named:

```
<group>_<version>_<Kind>.json
```

e.g. `nais.io_v1_Valkey.json`. The schemas are published to `gs://nais-schemas/` and served
at `https://schemas.nais.io/`.

Each schema describes `version: v1`, `type`, `name`, optional `labels`, and `spec` — never the
Kubernetes `apiVersion`/`kind`/`metadata` envelope. The spec fields are derived from the CRD
schema where they match the native format; Valkey's CRD-only `spec.version` and currently ignored
`spec.persistence` are omitted. Object
fields reject unknown properties unless the schema explicitly allows additional properties.
Documentation examples and schemas are generated independently.

### Aggregate schema

An `all.json` file is also written to `<dir>`, referencing every generated schema file via `$ref`:

```json
{"oneOf": [{"$ref": "nais.io_v1_OpenSearch.json"}, {"$ref": "nais.io_v1_Postgres.json"}, {"$ref": "nais.io_v1_Valkey.json"}]}
```

The refs are plain relative filenames, which resolve correctly once the files are served from the same
bucket/path — this is how the `nais` CLI is expected to consume the full set of schemas.
