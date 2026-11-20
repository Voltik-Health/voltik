#!/bin/bash
# =============================================================================
# VOLTIK · creates the database and its roles. Runs ONCE, on the container's
# first start, while the data volume is still empty.
#
# Roles:
#   voltik_migrations  database owner; the only role that creates or alters tables
#   voltik_api         used by the Go API; reads and writes data only
#   voltik_readonly    read-only (queries, analysis, day-to-day DBeaver)
#
# Passwords come from the .env file (never from Git).
# =============================================================================
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
  -v pw_mig="$VOLTIK_MIGRATIONS_PASSWORD" \
  -v pw_api="$VOLTIK_API_PASSWORD" \
  -v pw_ro="$VOLTIK_READONLY_PASSWORD" <<'EOSQL'
CREATE ROLE voltik_migrations LOGIN PASSWORD :'pw_mig';
CREATE ROLE voltik_api        LOGIN PASSWORD :'pw_api';
CREATE ROLE voltik_readonly   LOGIN PASSWORD :'pw_ro';

CREATE DATABASE voltik OWNER voltik_migrations;
REVOKE ALL ON DATABASE voltik FROM PUBLIC;
GRANT CONNECT ON DATABASE voltik TO voltik_api, voltik_readonly;
EOSQL

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname voltik <<'EOSQL'
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO voltik_api, voltik_readonly;

-- Everything voltik_migrations creates from now on gets these permissions:
ALTER DEFAULT PRIVILEGES FOR ROLE voltik_migrations IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO voltik_api;
ALTER DEFAULT PRIVILEGES FOR ROLE voltik_migrations IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO voltik_api;
ALTER DEFAULT PRIVILEGES FOR ROLE voltik_migrations IN SCHEMA public
  GRANT SELECT ON TABLES TO voltik_readonly;
EOSQL

echo "Voltik: database 'voltik' and roles created."
