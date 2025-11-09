# go-flow LLM Guide

Copy/paste this file into your automation agent so it instantly understands how to scaffold, edit, and run flows with `go-flow`.

## Purpose
`go-flow` is a YAML-driven CLI for end-to-end API tests that can send HTTP requests, run Postgres SQL, execute MongoDB operations, and invoke gRPC methods while sharing variables across steps.

## Quick Install
Assumes the `go-flow` binary is already on `$PATH`. Reinstall only if needed:

```bash
go install github.com/IamNator/go-flow@latest
# or build from source
git clone https://github.com/IamNator/go-flow.git
cd go-flow
go build -o go-flow ./...
```

## Core Commands
| Command | Purpose | Key Flags / Notes |
|---------|---------|------------------|
| `go-flow new <flow-name>` | Scaffold `flow/<NNN>_<flow-name>.yaml` (increments by 2). | Put flags **before** `<flow-name>`: `go-flow new --dir tests/e2e signup`. |
| `go-flow run` | Execute one or more flows. | `--file PATH`, `--dir DIR`, `--flow NAME`, `--var key=value`, `--export_file exported_vars.json`. |
| `go-flow list` | List discoverable flows. | `--dir DIR` (defaults to `flow`). |

## Workflow (LLM Checklist)
1. **Discover inputs**: `go-flow list --dir DIR` or inspect `flow/*.yaml` / `examples/*.yaml`.
2. **Scaffold (if needed)**: `go-flow new --dir DIR flow-name` and then edit the generated YAML.
3. **Edit flow**: modify `vars` and `steps`; remember each step is HTTP *or* SQL *or* Mongo *or* gRPC.
4. **Run**: `go-flow run` (all flows), `go-flow run --flow NAME`, or `--file PATH` for a single YAML.
5. **Override**: add `--var key=value` for dynamic substitutions and `--export_file path.json` to persist saved vars.
6. **Interpret output**: ✅ / ❌ lines indicate pass/fail; failures print response payloads for quick debugging.

## Flow Structure
```yaml
vars:
  base: http://localhost:8080/api/v1

steps:
  - name: create-user
    method: POST              # HTTP step
    url: "{{.base}}/users"
    headers:
      Content-Type: application/json
    body: |
      {"email": "{{randomEmail}}"}
    expect_status: 201
    save:
      user_id: data.id
```
- `wait: "5s"` pauses before the step (templated duration).
- `timeout_seconds` defaults to 10 if omitted.
- `export: true` + `save` pushes captured vars into `--export_file`.

## Step Reference
- **HTTP**: require `method` + `url`; optional `headers`, `body`, `expect_status`, `save` (GJSON paths).
- **SQL (Postgres)**: set `sql`, optional `database_url` (falls back to `vars.database_url` → `DATABASE_URL`). `save` captures first row; `expect_affected_rows` asserts row count.
- **MongoDB**: use `mongo` block with `uri`, `database`, `collection`, `operation` (`findone`, `find`, `aggregate`, `insertone`, `updateone`, `deleteone`, `command`), plus relevant payload fields (`filter`, `document`, `update`, `pipeline`, `command`).
- **gRPC**: supply `grpc` block with `target`, `method`, `request`, optional `format` (`json` default), TLS fields (`use_tls`, `skip_tls_verify`, `ca_cert`, `client_cert`, `client_key`, `server_name`), descriptor inputs (`proto_sets`, `proto_files`, `proto_paths`), and `expect_code`.

## Template Helpers (Selected)
| Function | Description |
|----------|-------------|
| `randomString n` | Lowercase alphanumeric string. |
| `randomEmail` | Email with randomized local+domain portions. |
| `randomPhone` | Phone number assembled from random segments. |
| `randomName`, `randomCompany`, `randomJobTitle` | Human-friendly fixtures. |
| `randomUUID` | `uuid.NewString()`. |
| `randomInt min max` | Inclusive random integer. |

Helpers live in `template_funcs.go`; consult it before relying on additional behavior.

## Tips & Reminders
- Always mention the binary as `go-flow` (never `flow`).
- Flags must appear before positional args due to Go’s `flag` parsing.
- Saved values go into an in-memory map and can be exported via `--export_file`.
- Runner stops on first failing step; rerun after fixing the underlying issue.
- Keep YAML as ASCII; template renders use Go’s `text/template` with `missingkey=zero`.
