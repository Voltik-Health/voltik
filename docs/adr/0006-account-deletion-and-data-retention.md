# 0006 — Account deletion and data retention

- **Status:** accepted
- **Date:** 2026-11-02

## Context
Voltik stores health data, so the GDPR right to erasure (art. 17) applies: a deleted account must disappear without undue delay, at most one month after the request (art. 12(3)).
Erasing at once leaves no way back after a mistaken or impulsive request, and the SRS (UC016–UC018) asks for a grace period.
Two things must survive the erasure: the audit log (UC021), which proves who accessed which data, and the clinical history of clinicians, whose follow-ups and consultations reference them (`ON DELETE RESTRICT`).

## Decision
Deletion has two steps, 30 days apart.

**1. Request (`DELETE /v1/me`, UC016).** In one transaction:
- `users.account_status = 'deactivated'` and `users.deleted_at = now()`; the live patient profile also gets `deleted_at`;
- push tokens are deactivated and the subscriptions the user pays stop renewing;
- an `account_deletion_requested` row goes to the audit log.

It takes effect immediately. The session token carries only the user id, and the API reads the account and its roles on every request, so the token stops working (401) with no revocation list. Signing in answers 403 `account_pending_deletion` with the erasure date.
A clinician with active follow-ups gets 409 `professional_has_patients`: the administration ends them first.

**2. Erasure (UC018).** A job inside the API runs every day at 03:00 UTC:
- it erases the accounts with `deleted_at` older than 30 days, in batches of 100, each in its own transaction (`DELETE FROM users`);
- a PostgreSQL advisory lock lets one API instance run it at a time;
- an account that fails is logged and retried the next day, and the job carries on;
- the log only has counts, never ids.

What the foreign keys do with an erased account:

| Erased (`ON DELETE CASCADE`) | Kept, without the person (`ON DELETE SET NULL`) |
|---|---|
| user, identities, push tokens, consents | audit log rows (`actor_user_id`, `target_patient_id`) |
| patient profile and everything below it: devices, readings, heart rate, meals, insulin, alerts, Iris, reports, follow-ups, consultations, conversations | company codes used by the patient (`used_by_patient_id`), so the licence stays counted |
| caregiver links, care invites, subscriptions paid by the user | care invites accepted by the user (`used_by_user_id`) |

The row is erased, not anonymised: nothing needs it once the data hanging from it is gone.

**Clinicians are never erased.** `health_professionals.user_id` is `ON DELETE RESTRICT`. The job leaves out deleted accounts with a clinician profile and logs how many it kept; the administration handles them (UC041).

**Soft-deleted rows are invisible.** Every query of the API filters `deleted_at IS NULL`. The only exception is the sign-in lookup, which must see an account waiting for erasure to answer `account_pending_deletion`.

## Consequences
- Nothing is erased before the 30 days, so recovering an account (UC017, `POST /v1/me/restore`) can be added later without changing this design. It is not implemented yet.
- During the 30 days the person cannot create a new account with the same provider identity. The email is free at once: emails are unique only among live accounts.
- When files reach object storage (meal photos, report PDFs), the job must delete them too (UC018, step 5).
- Database backups keep erased accounts until they expire, so the backup retention set in voltik-infra limits how long erased data really lasts.
- The job runs in the API container: if no instance is up at 03:00 UTC, that day's run is skipped, and the next run erases everything that is due.
