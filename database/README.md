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

## Migrations so far

| Version | What it does |
|---|---|
| `000001_initial_schema` | The first 29 tables. |
| `000002_refined_data_model` | The data model of the SRS (chapters 6 and 7, annex B): 48 tables, 64 foreign keys, 10 business-rule triggers. Converts existing rows. |

## Creating a new migration

1. `migrate create -ext sql -dir database/migrations -seq add_reports_index`
   creates `000003_add_reports_index.up.sql` and `.down.sql` (the next free number).
   If both developers take the same number, whoever merges second renumbers theirs.
2. `.up.sql` makes the change; `.down.sql` undoes exactly that change. Wrap each in `BEGIN; ... COMMIT;`.
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

- `UUID` identifiers with `gen_random_uuid()`; readings, heart rate samples, predictions and the audit log use `BIGSERIAL`.
- Instants are `TIMESTAMPTZ` (stored in UTC) and end in `_at`; calendar dates are `DATE` and end in `_date`.
- Booleans start with `is_` (or `auto_` / `notify_`).
- Lists the business can extend are **enumeration tables** (`genders`, `diabetes_types`, `therapy_types`, `device_types`,
  `meal_types`, `insulin_types`, `medical_specialties`, `identity_providers`, `subscription_plans`):
  `SMALLINT` id, stable `code` in lower snake_case, `label` in Portuguese, `is_active`. A new value is a new row, not a migration;
  a value in use is hidden with `is_active = false`, never deleted. The API exposes the `code`.
- Fixed state machines (statuses, channels) use `CHECK (... IN (...))` with lower snake_case values.
- Text columns never accept blank text (`CHECK (btrim(col) <> '')`); optional text is `NULL`, not `''`.
- Constraint names: single-column checks are `<table>_<column>_check`; rules over several columns are `ck_*`,
  unique constraints `uq_*`, composite foreign keys `fk_*`, triggers `trg_<table>_<purpose>`.
- Every foreign key has an index, unless a `UNIQUE` constraint already starts with that column.
- **No passwords.** Sign-in is external only (OpenID Connect); a user is identified by
  `external_identities (provider_id, provider_subject)`. The email is optional, because some providers do not share it.
- Roles come from the data, not from a profile table: patient = live row in `patients`, caregiver = active `caregiver_links`,
  clinician = live row in `health_professionals`, company manager = `organization_managers`, admin = `users.is_platform_admin`.

## Integrity rules enforced by the database

- Emails are unique ignoring case among live users (`uq_users_email_lower`).
- Composite foreign keys keep related rows on the same patient: a reading's or activity's device, an insulin dose's meal,
  a report's follow-up, and a company code's manager (who must manage that company).
- Conversations and teleconsultations hang from a follow-up (`patient_professional_assignments`) and/or a caregiver link.
  There is one conversation per channel, and a caregiver-doctor conversation is about one patient.
- Neither a clinician nor a patient has two scheduled consultations at the same time.
- An alert has exactly one source, and it matches the type: critical alerts come from a reading, predicted alerts from a
  prediction, sensor alerts from a device. Each recipient and channel has its own delivery row (`alert_recipients`).
- A company issues and activates codes only while it is active and within its seats; a subscription covers at most the
  number of patients its plan allows, and a patient is covered by one subscription at a time.
- Nobody is their own caregiver or clinician. Only composite dishes have ingredients.
- An emergency contact's priority is unique per patient, checked at commit so two contacts can swap places.
- Status columns agree with their timestamps (an eaten meal has `eaten_at`, an acknowledged alert has `acknowledged_at`,
  a used code has `used_at`, a revoked link has `revoked_at`).
- Report percentages are between 0 and 100, and TIR + TBR + TAR add up to 100 (±0.2).
- The audit log is never changed: `voltik_api` has no `UPDATE`/`DELETE` on it, and a trigger blocks everyone else.

The triggers raise `SQLSTATE 23514` (`check_violation`) with a constraint name the API maps to an error code:
`seat_limit_reached`, `organization_inactive`, `beneficiary_limit`, `self_link`, `slot_taken`, `sender_not_in_channel`,
`cross_patient_attachment`, `cross_patient_conversation`, `not_composite_dish`, `audit_log_immutable`.

## Deletion rules

- Deleting an account is a hard delete (SRS UC016/UC018): it cascades to the patient and all their clinical data.
  Audit log rows stay, with the deleted user or patient set to `NULL`.
- Health professionals are never deleted, only soft-deleted (`deleted_at`): their user and their follow-ups reference
  them with `ON DELETE RESTRICT`, so the clinical history is kept.
- Enumeration values and legal documents are never deleted (`ON DELETE RESTRICT`); they are deactivated or superseded
  by a new version.
