# 0001 — A single repository for the whole project

- **Status:** accepted
- **Date:** 2026-10-08

## Context
Voltik has Android and iOS apps, a web portal, an API, a prediction service and a database, all sharing the same API contract.

## Decision
Monorepo, with one folder per component. Jenkins uses path filters to build only what changed.

## Consequences
A change to the contract and to its clients lands in a single pull request. The CI configuration is slightly more involved.
