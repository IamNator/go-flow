# Example Flows

This folder collects runnable samples you can copy into your own project or execute directly with `flow run --file <path>`.

| File | Highlights |
|------|------------|
| `basic_http.yaml` | Demonstrates creating, fetching, and deleting an HTTP resource while saving variables between steps. |
| `sql_validation.yaml` | Shows how to query PostgreSQL, assert affected rows, and clean up fixtures with SQL steps. |
| `mongo_flow.yaml` | Exercises the MongoDB step support with insert/find/update/delete operations. |
| `grpc_flow.yaml` | Calls a gRPC service using reflection defaults, metadata, and random data generators. |
| `complex_checkout_flow.yaml` | End-to-end checkout journey touching HTTP, third-party APIs, SQL, MongoDB, gRPC, and GraphQL. |

All files rely on local dev services (HTTP API, PostgreSQL, MongoDB, or gRPC) that you can adjust via the `vars` block.
