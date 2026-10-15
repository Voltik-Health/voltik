# API contract

[`openapi.yaml`](openapi.yaml) is the contract between the Voltik apps (Android and web) and the Go API, in OpenAPI 3.1.
The apps are built against a mock server that answers with the examples in the file, and the backend implements the same contract.
The conventions (authentication, field names, errors, pagination, batches) are at the top of the file, in `info.description`.

## Run the mock server

From the repository root (needs Node):

```bash
npx @stoplight/prism-cli@5 mock -h 0.0.0.0 api/openapi.yaml
```

It reloads by itself when `openapi.yaml` changes.

With Docker instead (on the Macs, run `colima start` first):

```bash
docker run --rm -p 4010:4010 -v "$PWD/api:/api" --platform linux/amd64 \
  stoplight/prism:5 mock -h 0.0.0.0 -m false /api/openapi.yaml
```

- `-m false` is required. Without it, the image crashes on start (`Cannot read properties of undefined (reading 'isPrimary')`).
- The image is only published for amd64, so on Apple Silicon it runs emulated. `--platform` silences the warning.
- The container does not notice changes to the file. Restart it after editing the contract.

| Client | Mock URL |
| --- | --- |
| Web (Vite dev server) and curl | `http://localhost:4010` |
| Android emulator | `http://10.0.2.2:4010` |
| Phone on the same Wi-Fi | `http://<your Mac's IP>:4010` |

## Using the mock

- Send `Authorization: Bearer <anything>` to every endpoint except `GET /v1/health` and the sign-in endpoints (`POST /v1/auth/...`). Without it the mock answers 401, like the real API.
- Each endpoint answers with the example of its success response. The mock has no state: a `POST` does not change what the next `GET` returns.
- When a request does not match the schema, the mock answers with the endpoint's 400 example. The mock's log shows the real reason, e.g. `Request query must have required property 'from'`.
- To build error and empty states, ask for another response with the `Prefer` header:

```bash
curl -H "Authorization: Bearer x" http://localhost:4010/v1/me
curl -H "Authorization: Bearer x" -H "Prefer: code=404" http://localhost:4010/v1/me/patient          # onboarding not done
curl -H "Authorization: Bearer x" -H "Prefer: code=204" http://localhost:4010/v1/me/heart-rate/latest # no watch data yet
```

Where the mock differs from the real API:

| Case | Mock | Real API |
| --- | --- | --- |
| Batch upload with an invalid item | 400, nothing stored | 200: valid items stored, invalid ones listed in `rejected` |
| `POST /v1/me/devices`, `/push-tokens` and `/consents` | Always 200 | 201 when created, 200 when it already existed |
| Paginated lists | One page with `next_cursor: null` | As many pages as needed |

## Notes for client code

- Ignore unknown fields in responses, because new fields are added without notice. With kotlinx.serialization, use `Json { ignoreUnknownKeys = true }`.
- Branch on the error `code`, never on `message`. Treat an unknown `code` (or enum value) as the generic case.
- TypeScript types: `npx openapi-typescript@7 api/openapi.yaml -o <file>.d.ts`.
- Kotlin models: `openapi-generator` (`-g kotlin`, `serializationLibrary=kotlinx_serialization`) works with this file. Its OpenAPI 3.1 support is still marked beta, so check the generated nullability.

## Lint

```bash
npx @redocly/cli@2 lint api/openapi.yaml
```

The rules live at the repository root: `redocly.yaml` (the `recommended` ruleset) and `.redocly.lint-ignore.yaml`.
The linter also checks every example against its schema. The CI pipeline (DVT-77) runs it on every pull request.

## Changing the contract

- Every change is a pull request approved by both developers.
- Adding is safe: a new endpoint, a new optional request field, a new response field or a new error code.
- Renaming or removing a field, or changing its type, breaks the apps. Agree on it first.
- Field names follow the database (`database/migrations/`).
- Keep the examples up to date, because the mock returns them.
- Groups 2 (diary, history, care team) and 3 (Iris, telemedicine, Dexcom) of DVT-76 come in later pull requests, before the tickets that use them start.
