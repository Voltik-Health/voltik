#!/bin/bash
# =============================================================================
# Rebuilds staging from scratch: DELETES the staging database, recreates the
# tables from the migrations and loads the fictitious sample data.
# Use it when staging gets "dirty" (e.g. migrations from abandoned branches).
# There is NEVER an equivalent for production.
# =============================================================================
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
export ENVIRONMENT=staging VOLTIK_DIR=/opt/voltik/staging
export VERSION="$(docker image ls voltik-api --format '{{.Tag}}' | head -1)"
DC=(docker compose -p voltik-staging -f "$ROOT/infra/server/compose.yml" --env-file "$VOLTIK_DIR/.env")

read -r -p "This deletes ALL staging data. Type 'staging' to confirm: " ok
[ "$ok" = "staging" ] || { echo "Cancelled."; exit 1; }

"${DC[@]}" down -v
"${DC[@]}" up -d --wait db
"${DC[@]}" run --rm migrate up
"${DC[@]}" exec -T db psql -U voltik_migrations -d voltik < "$VOLTIK_DIR/database/seeds/dev_sample_data.sql"
"${DC[@]}" up -d --wait api
echo "Staging rebuilt."
