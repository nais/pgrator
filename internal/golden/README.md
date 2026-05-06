# Golden File Test Framework

A framework for testing reconciler output against expected Kubernetes objects defined as YAML files.

## Directory Structure

Each reconciler gets a test data directory containing test cases as subdirectories:

```
testdata/<resource-type>/
  <test-case-name>/
    object.yaml              # The input NaisObject to reconcile
    prepared_data.yaml       # Optional prepared data passed to the reconciler
    consists_of/             # Expected actions (exact match - all must be present, no extras allowed)
      <name>.yaml
    contains/                # Expected actions (subset match - additional actions are allowed)
      <name>.yaml
    related_objects/         # Optional related objects available during reconciliation
      <name>.yaml
```

Use either `consists_of/` or `contains/`, not both.
`consists_of/` asserts the reconciler produces exactly the listed actions.
`contains/` asserts the reconciler produces at least the listed actions.

## Expected Action YAML Format

Each file in `consists_of/` or `contains/` defines one expected action:

```yaml
---
action: create          # Action type name (e.g. "create", "update", "delete")
matcher: Equal          # "Equal" for exact match, "Subset" for partial match
object:
  apiVersion: v1
  kind: ConfigMap
  metadata:
    name: my-config
    namespace: my-ns
  data:
    key: value
```

### Matchers

- `Equal` — the produced object must match the expected object exactly (zero-valued fields in expected are ignored).
- `Subset` — fields present in the expected object must match; extra fields on the actual object are ignored.

### Regex in String Fields

Any string value prefixed with `regexp:` is treated as a regex pattern:

```yaml
metadata:
  name: "regexp: ^my-app-[a-z0-9]+$"
```

## Usage in Tests

```go
func TestControllers(t *testing.T) {
    g := golden.NewGolden(t, myReconciler, "testdata/myresource")
    g.DefineTests()

    RunSpecs(t, "Suite")
}

var _ = BeforeSuite(func() {
    // Register schemes...
    err := g.ParseData(scheme)
    Expect(err).NotTo(HaveOccurred())
})
```

`NewGolden` loads test data from disk.
`DefineTests` registers Ginkgo test cases (call before `RunSpecs`).
`ParseData` resolves YAML into typed objects using the scheme (call in `BeforeSuite` after scheme setup).
