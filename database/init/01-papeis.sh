#!/bin/bash
# =============================================================================
# VOLTIK · criação da base de dados e dos papéis (corre UMA vez, no primeiro
# arranque do contentor, quando o volume de dados ainda está vazio)
#
# Papéis:
#   voltik_migracoes  dono da base de dados; o único que cria e altera tabelas
#   voltik_api        usado pela API em Go; só lê e escreve dados
#   voltik_leitura    só leitura (consultas, análises, DBeaver no dia a dia)
#
# As palavras-passe vêm do ficheiro .env (nunca do Git).
# =============================================================================
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
  -v pw_mig="$VOLTIK_MIGRACOES_PASSWORD" \
  -v pw_api="$VOLTIK_API_PASSWORD" \
  -v pw_ler="$VOLTIK_LEITURA_PASSWORD" <<'EOSQL'
CREATE ROLE voltik_migracoes LOGIN PASSWORD :'pw_mig';
CREATE ROLE voltik_api       LOGIN PASSWORD :'pw_api';
CREATE ROLE voltik_leitura   LOGIN PASSWORD :'pw_ler';

CREATE DATABASE voltik OWNER voltik_migracoes;
REVOKE ALL ON DATABASE voltik FROM PUBLIC;
GRANT CONNECT ON DATABASE voltik TO voltik_api, voltik_leitura;
EOSQL

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname voltik <<'EOSQL'
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO voltik_api, voltik_leitura;

-- Tudo o que o voltik_migracoes criar no futuro recebe estas permissões:
ALTER DEFAULT PRIVILEGES FOR ROLE voltik_migracoes IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO voltik_api;
ALTER DEFAULT PRIVILEGES FOR ROLE voltik_migracoes IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO voltik_api;
ALTER DEFAULT PRIVILEGES FOR ROLE voltik_migracoes IN SCHEMA public
  GRANT SELECT ON TABLES TO voltik_leitura;
EOSQL

echo "Voltik: base de dados 'voltik' e papéis criados."
