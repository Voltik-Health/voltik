# Voltik

Predictive glucose monitoring platform with the AI assistant **Iris**.
It warns people with diabetes **before** a hypoglycaemia, combining the continuous glucose monitor with physical activity from their watch.

Rui Passos & Afonso Carvalho · CTeSP TPSI · IPMAIA · 2026

**Start here:** [`docs/guides/00-getting-started.md`](docs/guides/00-getting-started.md)

## Environments

| Environment | Branch | How code gets there | Data |
|---|---|---|---|
| **Staging** | `staging` | Free push, no approval (`./scripts/to-staging.sh`). Jenkins deploys automatically. | Fictitious |
| **Production** | `main` | Pull request approved by the other developer + confirmation in Jenkins | Real |
| Local | any | Only the frontends run on the Mac, pointing to the **staging** API | — |

## Repository layout

| Folder | Contents | Technology |
|---|---|---|
| `android/` | Android app | Kotlin + Jetpack Compose (Android Studio) |
| `ios/` | iOS app | Swift + SwiftUI (Xcode) |
| `web/` | Web portal | Vue 3 |
| `backend/` | API | Go (REST, documented with OpenAPI) |
| `ml/` | Prediction service | Python + FastAPI |
| `api/` | API contract shared by every client | OpenAPI 3 |
| `database/` | Migrations, roles and sample data | PostgreSQL 17 + golang-migrate |
| `infra/` | Server, HTTPS proxy and deployment scripts | Oracle Cloud, Docker, Caddy |
| `scripts/` | Day-to-day helpers (send to staging, new migration) | Bash |
| `docs/` | Guides, deliverables, diagrams and architecture decision records | |
| `Jenkinsfile` | Pipeline: tests → staging → production | Jenkins |

## Team rules

- `main` is production: it only receives code through a pull request approved by the other developer.
- `staging` is the shared test environment: free push, any time.
- Each task gets its own branch, created from `main`, named with the Jira key: `VOLT-42-meal-logging`.
- Pull requests to `main` always come from the **task branch**, never from `staging`.
- The database schema only changes through a new migration (`./scripts/new-migration.sh`). Never by hand.
- Sign-in is external only (Google, Microsoft, Apple, Facebook). Voltik never stores passwords.
- Secrets never go into Git. Real data never goes into staging.
- Everything in this repository (code, comments, commits, documentation) is written in English.
