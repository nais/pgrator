# docgen

The docgen tool automatically:
- Discovers all CRDs in the specified API directory via `+kubebuilder:object:root=true` markers
- Auto-detects the group and version from the package's `GroupVersion` variable
- Generates markdown documentation for each CRD that has a registered example function

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
