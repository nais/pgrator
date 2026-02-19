This directory contains external CRDs not managed by pgrator, but used in tests to verify compatibility with other operators.

These CRDs are copied from their respective repositories to ensure that tests are run against known versions of the CRDs.

The Aiven CRDs are copied from `aiven-operator` at the latest version running in our environments, which at the time of writing is 0.35.0:
https://github.com/aiven/aiven-operator/tree/v0.35.0/config/crd/bases.
