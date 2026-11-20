# 0004 — Two environments on the server: staging and production

- **Status:** accepted
- **Date:** 2026-10-08

## Context
Two developers want to test on the server without asking anyone for permission, but production must never receive unreviewed code. Running the backend and the database on each Mac means keeping both machines identical.

## Decision
- `staging` branch: free push, no approval. Jenkins deploys to staging automatically.
- `main` branch: protected. Code only gets in through a PR opened from the task branch (never from `staging`), with 1 approval. Jenkins asks for manual confirmation before deploying to production.
- Only the frontends (web, Android, iOS) run locally, pointing to the staging API.
- Staging and production run on the same Oracle VM as separate Docker Compose projects, each with its own database, volume and `.env`.
- Only ports 80/443 are open. Databases and Jenkins are reached through SSH tunnels only.

## Consequences
Anyone can test on the server within minutes, and production only receives approved code. Staging is shared: a broken migration affects both developers, which is why `reset-staging.sh` exists. Staging never holds real data.
