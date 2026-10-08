# 0005 — External authentication only (no stored passwords)

- **Status:** accepted
- **Date:** 2026-10-08

## Context
Storing passwords means owning hashing, resets, brute-force protection and breach risk. The course supervisor considers in-house authentication bad practice and does not allow it in this project.

## Decision
Users sign in only through OpenID Connect providers: Google, Microsoft, Apple and Facebook.
The API validates the provider's ID token and finds the user through `external_identities (provider, provider_subject)`.
The `users` table has no password column.

## Consequences
No password data to protect or leak, and sign-in is familiar to users. Every user needs an account with at least one supported provider, which matters for elderly users and children (a caregiver may need to help create it). Account linking (one person signing in with two providers) relies on the provider email and must be handled by the API.
