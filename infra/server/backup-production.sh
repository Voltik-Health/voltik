#!/bin/bash
# =============================================================================
# Daily backup of the PRODUCTION database.
# Schedule it on the server (crontab -e):
#   30 3 * * * /opt/voltik/repo/infra/server/backup-production.sh >> /opt/voltik/backups/backup.log 2>&1
# =============================================================================
set -euo pipefail
TARGET=/opt/voltik/backups
mkdir -p "$TARGET"
FILE="$TARGET/production-$(date -u +%Y%m%dT%H%M%SZ).dump"

docker compose -p voltik-production exec -T db pg_dump -U voltik_migrations -d voltik -Fc > "$FILE"
chmod 600 "$FILE"
echo "Backup created: $FILE ($(du -h "$FILE" | cut -f1))"

# Keep only the last 14 days on this server
find "$TARGET" -name 'production-*.dump' -mtime +14 -delete

# MISSING STEP: copy the backup off the server (Oracle Object Storage), e.g.:
# oci os object put --bucket-name voltik-backups --file "$FILE"
