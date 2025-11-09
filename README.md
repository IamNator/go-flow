# go-flow

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://golang.org)

A powerful CLI tool for writing and executing end-to-end (E2E) tests using YAML-based flow definitions. `go-flow` supports HTTP requests, SQL queries, MongoDB operations, and gRPC calls, making it ideal for API testing, integration testing, and automated workflows.

## TL;DR – ship a flow in 60 seconds

```bash
go install github.com/IamNator/go-flow@latest
go-flow new smoke-test      # scaffold
go-flow run --file flow/002_smoke-test.yaml
```

Want a richer sample? `go-flow run --file examples/basic_http.yaml` and peek inside `examples/complex_checkout_flow.yaml`.

## Features

**Protocols & data sources**
- HTTP/REST, GraphQL, gRPC (reflection or protos)
- SQL (Postgres) + MongoDB driver operations

**Flow ergonomics**
- YAML templates with rich random-data helpers
- Save/export variables, reuse across steps
- Assertions on HTTP status, DB rows, and more

**Productivity boosts**
- Colored CLI output, per-step timeouts, optional skips
- Examples directory plus `go-flow new` scaffolding
- Organized flows by directory prefix

## Installation

### Using Go Install

```bash
go install github.com/IamNator/go-flow@latest
```

### Build from Source

```bash
git clone https://github.com/IamNator/go-flow.git
cd go-flow
go build -o flow
```

## Quick Start

1. **Create a new flow:**

```bash
go-flow new my-first-test
```

This creates a file `flow/002_my-first-test.yaml` with a basic template.

2. **Edit the flow file:**

```yaml
vars:
  base: http://localhost:8080/api/v1

steps:
  - name: create-user
    method: POST
    url: "{{.base}}/users"
    headers:
      Content-Type: application/json
    body: |
      {
        "email": "{{randomEmail}}",
        "name": "{{randomName}}"
      }
    expect_status: 201
    save:
      user_id: data.id
      user_email: data.email
```

3. **Run the flow:**

```bash
go-flow run
```

## Examples

Explore the `examples/` directory for ready-to-run flows showcasing HTTP, SQL, MongoDB, and gRPC steps. Execute any sample directly:

```bash
go-flow run --file examples/basic_http.yaml
```

See `examples/README.md` for a quick overview of what each file demonstrates.

## Usage

### Commands

#### `go-flow run`

Execute flow files.

```bash
# Run all flows in the default 'flow' directory
go-flow run

# Run a specific flow by name
go-flow run --flow my-test

# Run a specific flow file
go-flow run --file /path/to/flow.yaml

# Run flows from a different directory
go-flow run --dir tests/flows

# Override variables
go-flow run --var base=http://localhost:3000 --var api_key=secret123
```

**Options:**
- `-f, --file` - Explicit path to a flow file
- `-d, --dir` - Directory containing flow files (default: `flow`)
- `-n, --flow` - Flow name (file name without extension)
- `-v, --var` - Override flow variable (format: `key=value`)

#### `go-flow new`

Create a new flow file with a basic template.

```bash
# Create a new flow in the default directory
go-flow new user-registration

# Create in a custom directory
go-flow new signup-test --dir tests/e2e
```

**Options:**
- `-d, --dir` - Directory to create the flow file in (default: `flow`)

#### `go-flow list`

List all available flows in a directory.

```bash
# List flows in default directory
go-flow list

# List flows in custom directory
go-flow list --dir tests/e2e
```

## Flow File Structure

### Basic Structure

```yaml
vars:
  key: value

steps:
  - name: step-name
    # HTTP, SQL, Mongo, or gRPC step fields
```

### Variables

Define reusable variables in the `vars` section:

```yaml
vars:
  base_url: http://localhost:8080
  api_version: v1
  api_key: your-secret-key
```

Variables can be:
- Defined in the flow file
- Overridden via CLI flags (`--var key=value`)
- Referenced using Go template syntax: `{{.variable_name}}`

### HTTP Steps

Execute HTTP requests:

```yaml
steps:
  - name: get-users
    method: GET
    url: "{{.base_url}}/users"
    headers:
      Authorization: "Bearer {{.api_key}}"
      Content-Type: application/json
    timeout_seconds: 30
    expect_status: 200
    save:
      first_user_id: data.0.id
```

**HTTP Step Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Step identifier |
| `skip` | bool | No | Skip this step during execution (default: false) |
| `method` | string | Yes | HTTP method (GET, POST, PUT, DELETE, etc.) |
| `url` | string | Yes | Request URL (supports templates) |
| `headers` | map | No | HTTP headers |
| `body` | string | No | Request body (supports templates) |
| `timeout_seconds` | int | No | Timeout in seconds (default: 10) |
| `expect_status` | int | No | Expected HTTP status code |
| `save` | map | No | Save response values (key: JSON path) |

### SQL Steps

Execute SQL queries:

```yaml
steps:
  - name: query-user
    sql: |
      SELECT id, name, email 
      FROM users 
      WHERE email = '{{.user_email}}';
    database_url: "{{.database_url}}"
    timeout_seconds: 10
    expect_affected_rows: 1
    save:
      db_user_id: id
      db_user_name: name
```

**SQL Step Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Step identifier |
| `skip` | bool | No | Skip this step during execution (default: false) |
| `sql` | string | Yes | SQL query (supports templates) |
| `database_url` | string | No* | PostgreSQL connection string |
| `timeout_seconds` | int | No | Timeout in seconds (default: 10) |
| `expect_affected_rows` | int | No | Expected number of affected/returned rows |
| `save` | map | No | Save column values (key: column name) |

*If not provided, uses `database_url` variable or `DATABASE_URL` environment variable.

### MongoDB Steps

Interact with Mongo collections or run database commands. Each step uses the official MongoDB driver underneath, so you can reuse templated filters/documents and capture Extended JSON responses for later steps.

```yaml
steps:
  - name: load-user
    mongo:
      uri: mongodb://localhost:27017
      database: app
      collection: users
      operation: findOne
      filter: |
        {"email": "{{.user_email}}"}
    save:
      mongo_user_id: _id.$oid

  - name: deactivate-user
    mongo:
      database: app
      collection: users
      operation: updateOne
      filter: |
        {"_id": {"$oid": "{{.mongo_user_id}}"}}
      update: |
        {"$set": {"active": false}}
    expect_affected_rows: 1
```

Supported operations: `findOne` (default), `find`, `aggregate`, `insertOne`, `updateOne`, `deleteOne`, and `command`.

**Mongo Step Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `uri` | string | No* | MongoDB connection string (`mongo_uri` var or `MONGO_URI` env as fallback) |
| `database` | string | Yes | Database name (`mongo_database` var fallback) |
| `collection` | string | Yes† | Collection name for collection-scoped operations (`mongo_collection` var fallback) |
| `operation` | string | No | One of the supported operations (default: `findOne`) |
| `filter` | string | No | JSON filter document (used by find/update/delete) |
| `document` | string | No | JSON document for `insertOne` |
| `update` | string | No | JSON update document for `updateOne` |
| `pipeline` | string | No | JSON array pipeline for `aggregate` |
| `command` | string | No | JSON command document (required when `operation: command`) |
| `limit` | int | No | Max documents to return for `find` |

*If omitted, `mongo_uri` flow var or `MONGO_URI` environment variable is required.  
†Not used for `operation: command`.

Responses are serialized to MongoDB Extended JSON, so `save` paths can reference fields such as `_id.$oid` or `inserted_id.$oid`. Use `expect_affected_rows` to assert the number of matched/modified/returned documents, just like SQL steps.

### gRPC Steps

Invoke gRPC services directly from a flow. `go-flow` uses [`grpcurl`](https://github.com/fullstorydev/grpcurl) so you can hit any RPC by relying on server reflection or by supplying descriptors.

```yaml
steps:
  - name: greet-user
    grpc:
      target: localhost:50051
      method: helloworld.Greeter/SayHello
      request: |
        {"name": "{{randomName}}"}
      metadata:
        authorization: "Bearer {{.api_key}}"
      expect_code: OK
    save:
      greeting: message
```

**gRPC Step Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `target` | string | Yes | gRPC server address (`host:port` or unix socket) |
| `method` | string | Yes | Fully-qualified RPC name (`package.Service/Method` or `package.Service.Method`) |
| `request` | string | No | Request body (JSON by default, supports templates) |
| `format` | string | No | Payload format: `json` (default) or `text` |
| `metadata` | map | No | Metadata headers to send with the RPC |
| `reflection_metadata` | map | No | Headers sent only when talking to the reflection service |
| `use_tls` | bool | No | Dial using TLS (default: false) |
| `skip_tls_verify` | bool | No | Skip TLS certificate verification (dev/test only) |
| `ca_cert` | string | No | Path to CA bundle used to trust the server |
| `client_cert` / `client_key` | string | No | Paths to client cert/key for mutual TLS |
| `server_name` | string | No | Override the TLS server name (SNI) |
| `proto_sets` | []string | No | FileDescriptorSet (`.protoset`) files to load descriptors from |
| `proto_files` | []string | No | `.proto` files to load (mutually exclusive with `proto_sets`) |
| `proto_paths` | []string | No | Additional import paths for resolving `proto_files` |
| `use_reflection` | bool | No | Enable/disable server reflection (default: true) |
| `expect_code` | string | No | Expected gRPC status (name like `OK` or numeric code) |

> Responses are serialized to JSON before saving. If the RPC streams multiple messages they are captured as a JSON array so you can still reference fields via `save`.

### Skipping Steps

You can skip individual steps by setting `skip: true`. This is useful for:
- Temporarily disabling steps during development
- Conditionally running steps based on environment
- Debugging specific parts of a flow

```yaml
steps:
  - name: optional-cleanup
    skip: true
    sql: "DELETE FROM temp_data WHERE created_at < NOW() - INTERVAL '1 day';"
    
  - name: main-test
    method: GET
    url: "{{.base}}/api/endpoint"
    expect_status: 200
```

When a step is skipped, it will be logged in the output but not executed.

### Saving Values

#### From HTTP Responses (JSON)

Use [gjson syntax](https://github.com/tidwall/gjson) to extract values:

```yaml
save:
  user_id: data.id                    # Extract data.id
  first_name: data.user.firstName     # Nested field
  email: data.users.0.email           # Array element
  token: meta.token                   # Different path
```

#### From SQL Results

Save values from the first row:

```yaml
save:
  user_id: id           # Save 'id' column to 'user_id' variable
  email: email          # Save 'email' column to 'email' variable
  user_name: name       # Save 'name' column to 'user_name' variable
```

If the save key matches the column name, you can use shorthand:

```yaml
save:
  id: id       # Or just reference by column name
```

## Template Functions

go-flow provides built-in template functions for generating random test data:

### Available Functions

| Function | Description | Example Output |
|----------|-------------|----------------|
| `{{randomUUID}}` | Generate a random UUID | `550e8400-e29b-41d4-a716-446655440000` |
| `{{randomEmail}}` | Generate a random email | `abc12345@xyz12.com` |
| `{{randomPhone}}` | Generate a random phone number | `+12345678901234` |
| `{{randomName}}` | Generate a random full name | `Alex Smith` |
| `{{randomInt 1 100}}` | Generate random integer in range | `42` |
| `{{randString 10}}` | Generate random alphanumeric string | `aB3xY9mK2p` |

### Usage in Flows

```yaml
steps:
  - name: create-user
    method: POST
    url: "{{.base}}/users"
    body: |
      {
        "id": "{{randomUUID}}",
        "email": "{{randomEmail}}",
        "phone": "{{randomPhone}}",
        "name": "{{randomName}}",
        "age": {{randomInt 18 65}},
        "token": "{{randString 32}}"
      }
```

## Examples

### Example 1: User Registration Flow

```yaml
vars:
  api_base: http://localhost:8080/api

steps:
  - name: register-user
    method: POST
    url: "{{.api_base}}/auth/register"
    headers:
      Content-Type: application/json
    body: |
      {
        "email": "{{randomEmail}}",
        "password": "Test123!",
        "name": "{{randomName}}"
      }
    expect_status: 201
    save:
      user_id: data.user.id
      auth_token: data.token

  - name: verify-user-created
    method: GET
    url: "{{.api_base}}/users/{{.user_id}}"
    headers:
      Authorization: "Bearer {{.auth_token}}"
    expect_status: 200
```

### Example 2: E2E Test with SQL Verification

```yaml
vars:
  base: http://localhost:3000/api
  database_url: postgres://user:pass@localhost:5432/testdb

steps:
  - name: create-product
    method: POST
    url: "{{.base}}/products"
    headers:
      Content-Type: application/json
    body: |
      {
        "name": "Test Product {{randomInt 1000 9999}}",
        "price": {{randomInt 10 100}}
      }
    expect_status: 201
    save:
      product_id: data.id

  - name: verify-in-database
    sql: |
      SELECT id, name, price 
      FROM products 
      WHERE id = '{{.product_id}}';
    expect_affected_rows: 1
    save:
      db_product_name: name

  - name: update-product
    sql: |
      UPDATE products 
      SET price = 99.99 
      WHERE id = '{{.product_id}}';
    expect_affected_rows: 1

  - name: verify-update
    method: GET
    url: "{{.base}}/products/{{.product_id}}"
    expect_status: 200
```

### Example 3: Complex Flow with Multiple Services

```yaml
vars:
  auth_api: http://localhost:8080
  user_api: http://localhost:8081
  order_api: http://localhost:8082

steps:
  - name: login
    method: POST
    url: "{{.auth_api}}/login"
    body: |
      {
        "username": "test@example.com",
        "password": "password123"
      }
    expect_status: 200
    save:
      access_token: token

  - name: get-user-profile
    method: GET
    url: "{{.user_api}}/profile"
    headers:
      Authorization: "Bearer {{.access_token}}"
    expect_status: 200
    save:
      user_id: id

  - name: create-order
    method: POST
    url: "{{.order_api}}/orders"
    headers:
      Authorization: "Bearer {{.access_token}}"
      Content-Type: application/json
    body: |
      {
        "user_id": "{{.user_id}}",
        "items": [
          {
            "product_id": "{{randomUUID}}",
            "quantity": {{randomInt 1 5}}
          }
        ]
      }
    expect_status: 201
    save:
      order_id: data.id

  - name: verify-order
    method: GET
    url: "{{.order_api}}/orders/{{.order_id}}"
    headers:
      Authorization: "Bearer {{.access_token}}"
    expect_status: 200
```

## Configuration

### Database Connection

For SQL steps, specify the database connection string in one of three ways (in order of precedence):

1. **Step-level** - In the step's `database_url` field
2. **Variable** - As a `database_url` variable in the flow
3. **Environment** - As the `DATABASE_URL` environment variable

Example:

```bash
export DATABASE_URL="postgres://user:password@localhost:5432/mydb?sslmode=disable"
go-flow run
```

### Timeout Configuration

Default timeout is 10 seconds per step. Override per step:

```yaml
steps:
  - name: long-running-query
    sql: "SELECT * FROM large_table;"
    timeout_seconds: 60
```

## Best Practices

1. **Organize Flows** - Use numbered prefixes for execution order (e.g., `001_setup.yaml`, `002_test.yaml`)
2. **Use Variables** - Keep configurations flexible with variables
3. **Save Important Values** - Use `save` to pass data between steps
4. **Validate Responses** - Always use `expect_status` and `expect_affected_rows`
5. **Meaningful Names** - Use descriptive step names for clarity
6. **Template Functions** - Use random data generators for test isolation
7. **SQL for Verification** - Use SQL steps to verify HTTP operations
8. **Skip for Development** - Use `skip: true` to temporarily disable steps during debugging

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Author

**Nator Verinumbe** ([@IamNator](https://github.com/IamNator))

## Support

If you encounter any issues or have questions, please [open an issue](https://github.com/IamNator/go-flow/issues) on GitHub.
