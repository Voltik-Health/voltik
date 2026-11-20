# 0007 — Meal photos and report PDFs in PostgreSQL

- **Status:** accepted
- **Date:** 2026-11-20

## Context
DVT-79 adds the first files: photos of meals (SRS UC103) and the PDF of clinical reports (UC148). The SRS talks about "armazenamento de objetos" (object storage). The servers have none yet: an OCI Object Storage bucket, or a MinIO container, would need new credentials, a client library and its own backups in voltik-infra. These files are health data, so they must be erased with the account (ADR 0006).

## Decision
The files live in the database, in `stored_files` (migration 000003):
- one row per file, with the patient, what it is for (`meal_photo` or `report_pdf`), the content type, the size, the SHA-256 and the bytes (at most 10 MB);
- `meal_photos.file_id` and `clinical_reports.pdf_file_id` point to it.

The API serves them at `GET /v1/files/{file_id}`, only through signed links that work for 15 minutes:
- the link carries `expires` and an HMAC-SHA256 `signature`, so a PDF viewer or an image loader can open it without the session token;
- the signing key is derived from `JWT_SECRET`, so there is no new secret;
- a link that is old, or changed in any way, answers 403 `link_expired`.

Photos are decoded and saved again as JPEG, at most 1600 px. This drops the metadata the app may have left (RNF06) and keeps each photo at a few hundred KB.

## Consequences
- **No new infrastructure.** The database backups already include the files.
- **Erasure is automatic.** `stored_files.patient_id` is `ON DELETE CASCADE`, so the retention job of ADR 0006 erases them with the account. Deleting a report also erases its PDF (UC150).
- **Size.** The database grows with the files. For the MVP that is a few MB per patient. Past thousands of patients, move them to object storage: only `GetFile` and `insertFile` in the repository change, and the links and the apps stay the same.
- **Links.** A link cannot be revoked before it expires, so 15 minutes is the most a copied link keeps working.
