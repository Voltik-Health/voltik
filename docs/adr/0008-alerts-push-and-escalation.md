# 0008 — Alerts, push notifications and escalation

- **Status:** accepted
- **Date:** 2026-11-20

## Context
A critical reading must reach the patient and their caregivers in under a minute (RNF08), even when Firebase is slow or down. If nobody reacts, the SRS asks for SMS to the caregivers and then to the emergency contacts (UC114–UC121). The API may run as more than one instance.

## Decision
**Alerts are written first and sent afterwards.** In the same transaction as the alert, the API creates the rows of `alert_recipients`:
- one `push` row (pending) and one `in_app` row (sent) per person: the patient, and the caregivers with an active link;
- a pending alert of the same type in the last 30 minutes is updated with the latest value instead of repeated;
- readings older than 2 hours raise nothing.

The pending push rows are the **outbox**:
- a worker inside the API claims them with `FOR UPDATE SKIP LOCKED` and a 2-minute lease, and sends them through the FCM HTTP v1 API;
- it uses a service-account token from `golang.org/x/oauth2/jwt`, not the Firebase Admin SDK, which needs Go 1.25 in its recent versions;
- a token that FCM no longer knows is deactivated (UC009); a temporary failure is retried after 1, 2, 4 and 8 seconds;
- the worker is woken as soon as an alert is raised.

**Escalation (UC117)** is another worker, every 30 seconds:
- a critical alert still pending 3 minutes later texts the caregivers that have a phone;
- every 3 minutes after that, it texts the next emergency contact by priority, until someone acknowledges or the contacts run out;
- a push that could not be sent brings the escalation forward (UC116 A1);
- every text is a row of `sms_messages`, the technical log the SRS asks for, because emergency contacts have no account and so no recipient row;
- the texts carry the patient's name, the value and the time, never a diagnosis or a dose.

**SMS provider.**
- `SMS_PROVIDER=log` (the default) writes the texts to the log only, with the number masked. The escalation runs in full, without a provider account.
- `SMS_PROVIDER=twilio` sends them through Twilio. Its status callbacks (`POST /v1/webhooks/sms`, with the `X-Twilio-Signature` checked) record the delivery (UC121).

## Consequences
- **Nothing is lost on a restart or a Firebase outage:** the rows wait in the database.
- **Several instances never send the same push twice:** the row lease guarantees it.
- **Push is optional.** Without Firebase credentials every push fails at once and the escalation takes over. In staging that means texts in the log.
- **Real SMS cost money** (about €0.05 each to Portugal). Twilio is US-based: for production with real patients, choose an EU-hosted provider (SRS 2.2) and add its sender next to `sms.Twilio`.
- **Email** (`channel = email`) is in the model but not sent yet.
