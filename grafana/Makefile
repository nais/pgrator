.PHONY: install fmt compile check

# Isolate dashboard tooling from the operator's mise config, Go tools and hooks.
TOOLS = mise --no-config exec aqua:google/go-jsonnet@0.21.0 aqua:jsonnet-bundler/jsonnet-bundler@0.6.0 --

install:
	$(TOOLS) jb install

fmt:
	$(TOOLS) jsonnetfmt -i src/*.libsonnet

compile:
	mkdir -p generated
	$(TOOLS) jsonnet -J vendor -m generated src/main.libsonnet

# Read-only: fail on formatting or generated drift, without fixing either.
check:
	$(TOOLS) jsonnetfmt --test src/*.libsonnet
	@set -e; tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
		$(TOOLS) jsonnet -J vendor -m "$$tmp" src/main.libsonnet; \
		diff -ru generated "$$tmp"
