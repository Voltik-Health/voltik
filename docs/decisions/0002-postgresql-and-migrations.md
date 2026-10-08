# 0002 — PostgreSQL with versioned migrations

- **Status:** accepted
- **Date:** 2026-10-08

## Context
Highly relational data (patients, caregivers, professionals, companies) and health data that demands integrity.

## Decision
PostgreSQL 17 in Docker. Schema managed with golang-migrate. Three roles with least privilege. Instants stored as `TIMESTAMPTZ`.

## Consequences
The schema is identical in every environment and every change is reviewed. It requires discipline: the schema is never changed by hand.
