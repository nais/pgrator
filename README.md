# pgrator

Postgres operator for nais, reconciles nais Postgres resource into a set of resources to get a Postgres cluster for an application.
Uses [Zalando postgres-operator](https://github.com/zalando/postgres-operator) for the heavy lifting.

## Description
// TODO(user): An in-depth paragraph about your project and overview of use

## Getting Started

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v1-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

