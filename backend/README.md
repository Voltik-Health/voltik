# Backend

The Voltik REST API, in Go. It implements the contract in [`api/openapi.yaml`](../api/openapi.yaml) and talks to PostgreSQL as the `voltik_api` role.

## Layout

Clean Architecture: each layer only calls the one below it.

| Folder | What it does |
|---|---|
| `cmd/api/` | Entry point: reads the configuration, wires the layers, serves HTTP and runs the retention job. |
| `internal/api/` | Types and server interface **generated** from the contract. Never edit `api.gen.go` by hand. |
| `internal/handler/` | HTTP: turns requests into service calls and results or errors into the contract's responses. Middleware: request id, JSON logs, CORS, caller IP. |
| `internal/service/` | Business rules: sign-in, account, patient profile, consents. No HTTP, no SQL. |
| `internal/repository/` | All the SQL, with pgx. Every query hides soft-deleted rows (`deleted_at IS NULL`). |
| `internal/domain/` | Business types (`User`, `Patient`, ...), validation rules and errors. Depends on nothing. |
| `internal/auth/` | Session tokens (JWT), the authentication middleware and the identity providers (Google, Microsoft, Apple, Facebook). |
| `internal/retention/` | Daily job that erases the accounts deleted more than 30 days ago ([ADR 0006](../docs/adr/0006-account-deletion-and-data-retention.md)). |
| `internal/config/` | Environment variables. |
| `internal/database/` | Connection pool and transactions; `dbtest/` prepares the database of the integration tests. |

## Run it locally

Needs Go 1.24+ and Docker (on the Macs, run `colima start` first). From the repository root:

```bash
cp .env.example .env                                         # fill in the passwords and JWT_SECRET
docker compose up -d db                                      # PostgreSQL with the voltik and voltik_test databases
docker compose --profile tools run --rm migrate up           # apply the migrations
docker compose exec -T db psql -U voltik_migrations -d voltik < database/seeds/dev_sample_data.sql

cd backend
set -a && . ../.env && set +a                                # load .env into the shell
go run ./cmd/api                                             # http://localhost:8080/v1/health
```

Without the provider variables, every sign-in answers 503 `provider_unavailable`. The apps can keep using the mock server (`api/README.md`) until then.

## Configuration

| Variable | Required | Meaning |
|---|---|---|
| `ENVIRONMENT` | | `local` (default), `staging` or `production` |
| `PORT` | | HTTP port, default `8080` |
| `DATABASE_URL` | yes | Connection URL with the `voltik_api` role |
| `JWT_SECRET` | yes | Signs the session tokens. At least 32 characters (`openssl rand -hex 32`), different in every environment |
| `CORS_ORIGINS` | | Comma-separated frontend origins allowed to call the API |
| `GOOGLE_CLIENT_IDS` | | Comma-separated OAuth client IDs (web and Android) |
| `MICROSOFT_CLIENT_ID` | | Application (client) ID of the Microsoft Entra app registration |
| `APPLE_CLIENT_IDS` | | Services ID and bundle ID. Apple also stays off while `identity_providers.is_active` is false |
| `FACEBOOK_APP_ID`, `FACEBOOK_APP_SECRET` | | Meta app with Facebook Login. The secret only lives on the server |

The values for staging and production live in each server's `.env` (voltik-infra), never in Git.

## Tests

```bash
cd backend
go test ./...          # unit tests: no database needed
go test -race ./...    # the same, checking for data races
```

The integration tests run the SQL against a real PostgreSQL 17 as `voltik_api`, so they also check the permissions. They apply the migrations themselves and empty every table before each test. Without these two variables they are skipped:

```bash
export VOLTIK_TEST_DATABASE_URL='postgres://voltik_migrations:<password>@localhost:5432/voltik_test?sslmode=disable'
export VOLTIK_TEST_API_DATABASE_URL='postgres://voltik_api:<password>@localhost:5432/voltik_test?sslmode=disable'
go test ./...
```

They refuse any database whose name does not end in `_test`. The local `db` container creates `voltik_test` on its first start (`database/local/02-test-database.sh`).
If your volume is older than that script, create it once with
`docker compose exec -T -e POSTGRES_USER=postgres db bash < database/local/02-test-database.sh`.

## Changing the contract

After a change to `api/openapi.yaml` is merged, regenerate the Go code and commit it:

```bash
cd backend
go generate ./...      # runs oapi-codegen v2.8.2 with internal/api/oapi-codegen.yaml
```

`oapi-codegen.yaml` lists the tags that are generated (`include-tags`). A ticket that implements a new group of endpoints adds its tag there.

## Rules worth knowing

- **Who is calling.** The session token only carries the user id. On every request the middleware reads the account and works out its roles from the data, so a deleted or suspended account stops working at once.
- **Patient data** is always the caller's own: `/v1/me/...` endpoints never take a patient id.
- **Errors** always have the shape `{code, message, details}`. `handler/errors.go` maps each domain error to its code.
- **Logs** are one JSON line per request (method, path, status, duration, request id). Bodies, tokens, emails and user ids are never logged.
- **Audit log.** Consent changes and deletion requests write a row to `data_access_audit_log` in the same transaction. The caller's IP is the `X-Real-IP` header set by Nginx, trusted only when the request comes from a private address.
- **Emails** are only kept when the provider verified them. An account can have no email.
