# Voltik

Predictive glucose monitoring platform with the AI assistant **Iris**.  
It warns people with diabetes **before** a hypoglycaemia, combining continuous glucose monitors (CGM) with physical activity from smartwatches.

Rui Passos & Afonso Carvalho · CTeSP TPSI · IPMAIA · 2026

---

## Environments

| Environment | Branch | Deployment | Data |
|---|---|---|---|
| **Local** | any | Run locally via `docker-compose.yml` | Fictitious / Seeds |
| **Staging** | `staging` | Direct push or PR merge; tested on staging server | Fictitious |
| **Production** | `main` | Pull request approved by peer developer | Real |

---

## Repository Layout

| Directory / File | Contents | Technology |
|---|---|---|
| `apps/android/` | Android client application | Kotlin + Jetpack Compose |
| `apps/ios/` | iOS client application | Swift + SwiftUI |
| `apps/web/` | Web portal (patient & clinical dashboard) | Vue 3 + Vite |
| `backend/` | Core REST API (Clean Architecture) | Go |
| `database/` | Migrations, roles initialization and seed data | PostgreSQL 17 + golang-migrate |
| `ml/` | Glucose prediction service & Iris assistant | Python + FastAPI / Google Gemini |
| `api/` | API specifications and shared contracts | OpenAPI 3 |
| `docs/` | Architecture decisions (ADRs), deliverables and diagrams | Markdown & PDF |
| `docker-compose.yml` | Local development environment orchestration | Docker Compose |
| `Jenkinsfile` | Continuous integration and deployment pipeline | Jenkins |
| `Makefile` | Common development shortcuts | Make |

---

## Team Rules

- `main` is production: it only receives code through a pull request approved by the other developer.
- `staging` is the shared test environment.
- Each task gets its own branch, created from `main`, named with the task description (e.g. `feat/meal-logging`).
- Pull requests to `main` always come from the **task branch**, never directly from `staging`.
- The database schema only changes through a new migration in `database/migrations/`. Never by hand.
- Sign-in is external only (Google, Microsoft, Apple, Facebook). Voltik never stores passwords.
- Secrets never go into Git. Real data never goes into staging.
- Everything in this repository (code, comments, commits, documentation) is written in English.
