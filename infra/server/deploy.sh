#!/bin/bash
# =============================================================================
# Deploys a version to an environment. Called by Jenkins:
#   infra/server/deploy.sh staging    <version>
#   infra/server/deploy.sh production <version>
# The voltik-api:<version> image must already exist (built in the previous stage).
# =============================================================================
set -euo pipefail

ENVIRONMENT="${1:?usage: deploy.sh staging|production <version>}"
VERSION="${2:?missing version}"
case "$ENVIRONMENT" in staging|production) ;; *) echo "Invalid environment: $ENVIRONMENT"; exit 1;; esac

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
VOLTIK_DIR="/opt/voltik/$ENVIRONMENT"
[ -f "$VOLTIK_DIR/.env" ] || { echo "Missing $VOLTIK_DIR/.env"; exit 1; }

# Stable copy of the migrations and roles script (independent of the Jenkins workspace)
rm -rf "$VOLTIK_DIR/database.new"
cp -r "$ROOT/database" "$VOLTIK_DIR/database.new"
rm -rf "$VOLTIK_DIR/database"
mv "$VOLTIK_DIR/database.new" "$VOLTIK_DIR/database"

export ENVIRONMENT VERSION VOLTIK_DIR
DC=(docker compose -p "voltik-$ENVIRONMENT" -f "$ROOT/infra/server/compose.yml" --env-file "$VOLTIK_DIR/.env")

echo "==> [$ENVIRONMENT] database"
"${DC[@]}" up -d --wait db

echo "==> [$ENVIRONMENT] migrations"
"${DC[@]}" run --rm migrate up

echo "==> [$ENVIRONMENT] API $VERSION"
"${DC[@]}" up -d --wait api

echo "==> [$ENVIRONMENT] health check"
"${DC[@]}" exec -T api wget -qO- http://127.0.0.1:8080/v1/health
echo
echo "==> [$ENVIRONMENT] deployed: $VERSION"
