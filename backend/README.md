# Backend (Go)

Voltik REST API. The contract lives in [`../api/openapi.yaml`](../api/openapi.yaml).

## Run locally (optional)

```bash
cd backend
go test ./...
go run ./cmd/api        # http://localhost:8080/v1/health
```

Day-to-day testing happens on staging (`./scripts/to-staging.sh`).

## Three layers

| Folder | Responsibility |
|---|---|
| `cmd/api/` | `main.go`: starts the server and wires everything together |
| `internal/handler/` | Receives the HTTP request, parses JSON, calls a service. **No business rules.** |
| `internal/service/` | Business logic: readings, alerts, permissions, reports |
| `internal/repository/` | PostgreSQL access |

With business logic separated from transport, adding gRPC later only means writing new handlers on top of the same services.

## Authentication

External sign-in only (OpenID Connect): Google, Microsoft, Apple and Facebook.
The API validates the provider's ID token and never stores passwords.
Users are matched through `external_identities (provider, provider_subject)`.

## Database access

The API connects with the **`voltik_api`** role, never with `postgres` or `voltik_migrations`.
