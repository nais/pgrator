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

For example, the `Postgres` CRD (group `data.nais.io`, version `v1`) with the above flags uses:
- Templates: `doc/templates/data.nais.io/v1/postgres/`
- Output: `doc/output/data.nais.io/v1/postgres/`

## Register example functions

Register the example function in `ExampleRegistry` in [docgen.go](docgen.go):

```go
var ExampleRegistry = map[schema.GroupVersionKind]func() object.NaisObject{
   {Group: "data.nais.io", Version: "v1", Kind: "Postgres"}: datav1.ExamplePostgresForDocumentation,
}
```
