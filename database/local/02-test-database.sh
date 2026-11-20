#!/bin/bash
# =============================================================================
# VOLTIK · LOCAL ONLY (root docker-compose.yml). Creates voltik_test, the
# database of the backend integration tests, with the same roles and
# permissions as voltik. The tests empty it on every run.
# Runs once, after 01-roles.sh, while the data volume is still empty.
# =============================================================================
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<'EOSQL'
CREATE DATABASE voltik_test OWNER voltik_migrations;
REVOKE ALL ON DATABASE voltik_test FROM PUBLIC;
GRANT CONNECT ON DATABASE voltik_test TO voltik_api, voltik_readonly;
EOSQL

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname voltik_test <<'EOSQL'
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO voltik_api, voltik_readonly;

ALTER DEFAULT PRIVILEGES FOR ROLE voltik_migrations IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO voltik_api;
ALTER DEFAULT PRIVILEGES FOR ROLE voltik_migrations IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO voltik_api;
ALTER DEFAULT PRIVILEGES FOR ROLE voltik_migrations IN SCHEMA public
  GRANT SELECT ON TABLES TO voltik_readonly;
EOSQL

echo "Voltik: database 'voltik_test' created."
