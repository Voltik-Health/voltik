# Backend

The Voltik REST API, in Go. It implements the contract in [`api/openapi.yaml`](../api/openapi.yaml) and talks to PostgreSQL as the `voltik_api` role.

## Layout

Clean Architecture: each layer only calls the one below it.

| Folder | What it does |
|---|---|
| `cmd/api/` | Entry point: reads the configuration, wires the layers, serves HTTP and starts the background jobs. |
| `internal/api/` | Types and server interface **generated** from the contract. Never edit `api.gen.go` by hand. |
| `internal/handler/` | HTTP: turns requests into service calls and results or errors into the contract's responses. Middleware: request id, JSON logs, CORS, caller IP. |
| `internal/service/` | Business rules: sign-in, account, patient profile, consents, health data, diary, alerts, reports, signed file links. No HTTP, no SQL. |
| `internal/repository/` | All the SQL, with pgx. Every query hides soft-deleted rows (`deleted_at IS NULL`). |
| `internal/domain/` | Business types (`User`, `Patient`, ...), validation rules and errors. Depends on nothing. |
| `internal/auth/` | Session tokens (JWT), the authentication middleware and the identity providers (Google, Microsoft, Apple, Facebook). |
| `internal/retention/` | Daily job that erases the accounts deleted more than 30 days ago ([ADR 0006](../docs/adr/0006-account-deletion-and-data-retention.md)). |
| `internal/push/` | Firebase Cloud Messaging (HTTP v1) and the outbox of alert pushes ([ADR 0008](../docs/adr/0008-alerts-push-and-escalation.md)). |
| `internal/sms/` | Escalation texts: log only, or Twilio, and its delivery receipts. |
| `internal/simulator/` | CGM simulator of the demo sensors in staging, with the traces of `tools/cgm-traces` ([ADR 0009](../docs/adr/0009-cgm-data-sources.md)). |
| `internal/gemini/` | Gemini client: meal photos and report summaries. Iris (DVT-84) reuses it. |
| `internal/openfoodfacts/` | Packaged products by barcode. |
| `internal/reportpdf/` | PDF of a clinical report, with the AGP chart. |
| `internal/jobs/` | Runs the background jobs every few seconds or minutes. |
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
| `PUBLIC_URL` | in staging and production | Public address of the API, e.g. `https://voltik-cgm-staging.duckdns.org`. Goes in the signed file links and the Nightscout URL. Default `http://localhost:<PORT>` |
| `FIREBASE_CREDENTIALS_FILE` or `FIREBASE_CREDENTIALS_BASE64` | | Service-account key of the Firebase project (file path, or the JSON in base64). Without it, pushes fail and alerts go straight to the escalation |
| `GEMINI_API_KEY`, `GEMINI_MODEL` | | Meal photo analysis and report summaries (default model `gemini-2.5-flash`). Without the key, photos end as `failed` and reports have no summary |
| `OPENFOODFACTS_USER_AGENT` | | How the API identifies itself to Open Food Facts, with a contact email. Default `Voltik/1.0 (voltik.startup@gmail.com)` |
| `SMS_PROVIDER` | | `log` (default: texts only in the log) or `twilio` |
| `TWILIO_ACCOUNT_SID`, `TWILIO_AUTH_TOKEN`, `TWILIO_FROM` | with `twilio` | Twilio account and sender (number or alphanumeric id) |
| `SIMULATOR_ENABLED` | | `true` feeds the demo sensors (staging only; refused in production) |

The values for staging and production live in each server's `.env` (voltik-infra), never in Git.

## Background jobs

The API also runs these jobs. Each one is safe with several instances: the work is claimed with row locks (`SKIP LOCKED`), or made idempotent by unique keys.

| Job | Every | What it does |
|---|---|---|
| Push outbox | 2 s, and at once after an alert | Sends the pending alert pushes; retries; deactivates dead tokens |
| Escalation | 30 s | Texts caregivers and emergency contacts about critical alerts nobody acknowledged, then sends the texts |
| Sensor monitor | 5 min | Sensors without data for 20 minutes, about to expire or expired |
| Photo analysis | 15 s, and at once after an upload | Analyses meal photos with Gemini |
| Post-meal impact | 5 min | Glucose change in the 2 hours after each meal |
| Weekly reports | 10 min | On Mondays from 06:00 (Portugal), last week's report for each patient who has readings |
| Simulator | 1 min | Readings of the demo sensors (only with `SIMULATOR_ENABLED=true`) |
| Retention | daily, 03:00 UTC | Erases the accounts deleted 30 days ago |

## Testing with a real CGM sensor

1. Install [Juggluco](https://www.juggluco.nl/) (or xDrip+) on an Android phone and connect it to the sensor (FreeStyle Libre 2/3, Dexcom G7/ONE+, …).
2. Register the sensor in Voltik (`POST /v1/me/devices`, `device_type: cgm_sensor`), then call `POST /v1/me/devices/{device_id}/nightscout-secret`.
3. In Juggluco's Nightscout settings, put the `nightscout_url` and the `api_secret` of the answer, and turn the upload on.

Each reading reaches Voltik within a minute, with alerts and push. These apps are not certified medical devices: tests only ([ADR 0009](../docs/adr/0009-cgm-data-sources.md)).

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
- **Health data** is written once: batches are idempotent through `idempotency_key`, and only manual readings can be corrected. Days are calendar days in Portugal (`domain.Lisbon`).
- **Alerts never block the readings.** A failure to raise an alert, push or ask Iris is logged; the readings are already stored (RNF09).
