This directory contains external CRDs not managed by pgrator, but used in tests to verify compatibility with other operators.

These CRDs are copied from their respective repositories to ensure that tests are run against known versions of the CRDs.
In the case of config connector CRDs, these are extracted from a running nais cluster.

See the mise task `generate:external-crds` (which calls `generate:cnrm-crds` and `generate:other-crds`) for updating these files.
