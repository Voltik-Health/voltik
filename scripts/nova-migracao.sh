#!/bin/bash
# =============================================================================
# Cria o par de ficheiros de uma nova migração, com data e hora no nome
# (evita que duas pessoas criem migrações com o mesmo número).
#   ./scripts/nova-migracao.sh adicionar_indice_relatorios
# =============================================================================
set -euo pipefail
NOME="${1:?uso: nova-migracao.sh nome_em_minusculas}"
TS="$(date -u +%Y%m%d%H%M%S)"
DIR="$(cd "$(dirname "$0")/.." && pwd)/database/migrations"
printf -- "-- %s: %s\n\n" "$TS" "$NOME" > "$DIR/${TS}_${NOME}.up.sql"
printf -- "-- Reverte %s_%s\n\n" "$TS" "$NOME" > "$DIR/${TS}_${NOME}.down.sql"
echo "Criados:"; ls "$DIR" | grep "$TS"
