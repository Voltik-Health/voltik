# 0003 — REST with JSON and OpenAPI instead of gRPC

- **Status:** accepted
- **Date:** 2026-10-08

## Context
The clients include a web portal, and browsers cannot speak gRPC directly.

## Decision
REST with JSON, contract in `api/openapi.yaml`, from which the clients are generated.

## Consequences
Fewer moving parts. Because the backend keeps business logic in its own layer, gRPC can be added later (for example between the API and the prediction service) without rewriting the system.
