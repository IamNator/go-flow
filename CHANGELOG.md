# Changelog

All notable changes to `go-flow` will be documented in this file.

The format loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2025-11-09

### Highlights
- **Multi-protocol flow runner**: Execute HTTP/REST, GraphQL, gRPC (reflection or descriptors), PostgreSQL SQL, and MongoDB operations from a single YAML file.
- **Rich templating & data generation**: Built-in helpers for random emails, UUIDs, sentences, colors, etc. (see `template_funcs.go`) along with full Go `text/template` support.
- **Stateful flows**: Capture values from responses or result sets via `save`, reuse them in later steps, and optionally export them as timestamped JSON artifacts (`--export_path`).
- **Operator ergonomics**: Colored CLI output, per-step timeouts, `wait`/`skip` controls, `--var key=value` overrides, directory-based flow discovery, and scaffolding via `go-flow new`.
- **Examples & docs**: Starter flows in `examples/`, plus an LLM integration playbook in `docs/LLM_GUIDE.md`.

### Commands
- `go-flow run` — execute one or many flows with support for explicit files, directories, or name-based selection.
- `go-flow new` — scaffold a numbered YAML flow with HTTP + SQL templates.
- `go-flow list` — enumerate the flows discovered in a directory.

### Reliability & Tests
- Added unit coverage for deterministic helpers: flow discovery, export path resolution, var overrides, gRPC formatting/code parsing, header rendering, response saving, trimming, and affected-row validation (`main_test.go`).
- Simulated HTTP client ensures override variables actually affect outgoing requests before hitting real services.

### Tooling
- `go.mod` pins Go 1.24+, grpcurl, Mongo/Postgres drivers, gofakeit, urfave/cli, and other supporting libraries.
- Default binary builds cleanly via `go build`, and the CLI can be installed with `go install github.com/IamNator/go-flow@latest`.

