#!/bin/bash
# =============================================================================
# Creates the file pair for a new migration, with date and time in the name
# (so two people never create migrations with the same number).
#   ./scripts/new-migration.sh add_reports_index
# =============================================================================
set -euo pipefail
NAME="${1:?usage: new-migration.sh name_in_lowercase}"
TS="$(date -u +%Y%m%d%H%M%S)"
DIR="$(cd "$(dirname "$0")/.." && pwd)/database/migrations"
printf -- "-- %s: %s\n\n" "$TS" "$NAME" > "$DIR/${TS}_${NAME}.up.sql"
printf -- "-- Reverts %s_%s\n\n" "$TS" "$NAME" > "$DIR/${TS}_${NAME}.down.sql"
echo "Created:"; ls "$DIR" | grep "$TS"
