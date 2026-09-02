# Golden File Test Data

Controller tests compare reconciler actions with expected Kubernetes objects defined as YAML.

## Directory structure

```text
internal/controller/testdata/<resource-type>/
  <case-name>/
    object.yaml
    prepared_data.yaml       # optional
    config.yaml              # optional
    consists_of/             # exact action set
      <name>.yaml
    contains/                # required subset of actions
      <name>.yaml
    related_objects/         # optional reconciler inputs
      <name>.yaml
```

Use either `consists_of/` or `contains/`, not both. `consists_of/` requires the
complete action set to match; `contains/` permits additional actions.

## Expected action format

```yaml
---
action: create
matcher: Equal
object:
  apiVersion: v1
  kind: ConfigMap
  metadata:
    name: my-config
    namespace: my-ns
  data:
    key: value
```

`action` must match the concrete action implementation returned by the
reconciler. Current fixtures use `create`, `createOrUpdate`, `exclusiveCreate`,
and `exclusiveCreateOrUpdate`.

Matchers:

- `Equal` compares the complete object.
- `Subset` ignores zero-valued fields and missing map entries in the expected object; use it only when those omitted fields are not part of the contract.
- A string beginning with `regexp:` is interpreted as a regular expression.

The standard-library test runner and fixture loader live in
`internal/controller/suite_test.go`. Add a fixture directory to create another
table-driven subtest; no suite registration is required. Run all fixtures with
`mise run test`, or the controller package alone with `go test ./internal/controller`
when `KUBEBUILDER_ASSETS` is already configured.
