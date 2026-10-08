# Database

PostgreSQL 17 in Docker. The schema is managed with **migrations** using
[golang-migrate](https://github.com/golang-migrate/migrate): every change is a pair of files in `migrations/`.

## Folders

| Folder | Purpose |
|---|---|
| `migrations/` | Schema changes, in order. `<version>_<name>.up.sql` applies, `.down.sql` reverts. |
| `init/` | Runs once, on first start: creates the `voltik` database and the three roles. |
| `seeds/` | **Fictitious** data for development and staging. |

## Roles (database users)

| Role | Can | Used by |
|---|---|---|
| `postgres` | Everything (superuser) | Administration only. Never by the application. |
| `voltik_migrations` | Create and alter tables | golang-migrate |
| `voltik_api` | Read and write data; insert-only on the audit log | Go API |
| `voltik_readonly` | Read only | Day-to-day DBeaver, analysis |

## Where it runs

| Environment | Where | SSH tunnel port |
|---|---|---|
| Staging | Server, Docker project `voltik-staging` | `5433` |
| Production | Server, Docker project `voltik-production` | `5434` |
| Local (optional) | Mac, root `docker-compose.yml` | `5432` |

Migrations are applied **by Jenkins** on every deployment (`infra/server/deploy.sh`).
The commands below are for the optional local database.

## Commands (local, optional)

```bash
# Start / stop
docker compose up -d db
docker compose stop db

# Migrations
docker compose --profile tools run --rm migrate up        # apply everything pending
docker compose --profile tools run --rm migrate down 1    # revert the last one
docker compose --profile tools run --rm migrate version   # current version

# Sample data
docker compose exec -T db psql -U voltik_migrations -d voltik < database/seeds/dev_sample_data.sql

# SQL console
docker compose exec db psql -U voltik_readonly -d voltik

# Start over (DELETES EVERYTHING in the local volume)
docker compose down -v
```

## Creating a new migration

1. `./scripts/new-migration.sh add_reports_index`
   creates `YYYYMMDDHHMMSS_add_reports_index.up.sql` and `.down.sql`.
   The timestamp in the name stops two people from creating migrations with the same number.
2. `.up.sql` makes the change; `.down.sql` undoes exactly that change.
3. (Recommended) Test `up`, `down 1` and `up` on the local database, so staging doesn't break for the other developer.
4. `./scripts/to-staging.sh`: Jenkins applies it on staging.
5. Pull request to `main`: once approved and confirmed, Jenkins applies it in production.

**Never edit a migration that has already run on staging or production.** Always fix it with a new migration.
If staging is left with migrations from abandoned branches: run `infra/server/reset-staging.sh` on the server.

## Connecting with DBeaver

- **Staging / production:** host `localhost`, port `5433` (staging) or `5434` (production), database `voltik`, user `voltik_readonly`.
  In the **SSH** tab, enable the tunnel with the server IP, user `ubuntu` and your private key.
  These ports are never opened on Oracle: they only exist inside the server.
- **Local (optional):** host `localhost`, port `5432`, database `voltik`, user `voltik_readonly`.
- In production, **always** use `voltik_readonly`. Never change production data by hand.

## Schema conventions

- `UUID` identifiers with `gen_random_uuid()`; readings, predictions and the audit log use `BIGSERIAL`.
- Instants are `TIMESTAMPTZ` (stored in UTC) and end in `_at`; calendar dates are `DATE` and end in `_date`.
- Booleans start with `is_` (or `auto_` / `notify_`).
- Closed value sets use `CHECK (... IN (...))` with lower snake_case values; make sure they fit the column's `VARCHAR`.
- Every foreign key has an index, unless a `UNIQUE` constraint already starts with that column.
- **No passwords.** Sign-in is external only (OpenID Connect); a user is identified by `external_identities (provider, provider_subject)`.

## Integrity rules enforced by the database

- Emails are unique ignoring case (`uq_users_email_lower`).
- A patient has at most one **active** subscription.
- A conversation's caregiver must be linked to the patient, and its professional assigned to the patient; there is one conversation per pair.
- A teleconsultation is only possible with a professional assigned to the patient.
- Status columns agree with their timestamps (an eaten meal has `eaten_at`, an acknowledged alert has `acknowledged_at`, a used invite has `used_at`).
- Report percentages are between 0 and 100 and TIR + TBR + TAR add up to 100.
- Revoking a caregiver or ending an assignment changes its `status`; re-linking reactivates the same row.

## Deletion rules

- Deleting a patient or user cascades to their clinical data (`ON DELETE CASCADE`).
- Health professionals are never deleted, only deactivated (`is_active = false`):
  their user, teleconsultations and conversations reference them with `ON DELETE RESTRICT`.

## Open decision

`ON DELETE CASCADE` on `patients` and `users` erases the whole clinical history when an account is deleted.
Decide whether that is the intended behaviour, or whether accounts should be deactivated and anonymised (preferable for health data).
Record the decision in `docs/decisions/`.
