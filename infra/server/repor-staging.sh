#!/bin/bash
# =============================================================================
# Repõe o staging do zero: APAGA a base de dados de staging, volta a criar as
# tabelas a partir das migrações e carrega os dados fictícios.
# Usar quando o staging ficar "sujo" (ex.: migrações de ramos abandonados).
# NUNCA existe um equivalente para produção.
# =============================================================================
set -euo pipefail
RAIZ="$(cd "$(dirname "$0")/../.." && pwd)"
export AMBIENTE=staging VOLTIK_DIR=/opt/voltik/staging
export VERSAO="$(docker image ls voltik-api --format '{{.Tag}}' | head -1)"
DC=(docker compose -p voltik-staging -f "$RAIZ/infra/server/compose.yml" --env-file "$VOLTIK_DIR/.env")

read -r -p "Isto apaga TODOS os dados de staging. Escreva 'staging' para confirmar: " ok
[ "$ok" = "staging" ] || { echo "Cancelado."; exit 1; }

"${DC[@]}" down -v
"${DC[@]}" up -d --wait db
"${DC[@]}" run --rm migrate up
"${DC[@]}" exec -T db psql -U voltik_migracoes -d voltik < "$VOLTIK_DIR/database/seeds/dev_dados_exemplo.sql"
"${DC[@]}" up -d --wait api
echo "Staging reposto."
