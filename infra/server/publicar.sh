#!/bin/bash
# =============================================================================
# Publica uma versão num ambiente. Chamado pelo Jenkins:
#   infra/server/publicar.sh staging  <versao>
#   infra/server/publicar.sh producao <versao>
# A imagem voltik-api:<versao> já tem de existir (construída no passo anterior).
# =============================================================================
set -euo pipefail

AMBIENTE="${1:?uso: publicar.sh staging|producao <versao>}"
VERSAO="${2:?indique a versão}"
case "$AMBIENTE" in staging|producao) ;; *) echo "Ambiente inválido: $AMBIENTE"; exit 1;; esac

RAIZ="$(cd "$(dirname "$0")/../.." && pwd)"
VOLTIK_DIR="/opt/voltik/$AMBIENTE"
[ -f "$VOLTIK_DIR/.env" ] || { echo "Falta $VOLTIK_DIR/.env"; exit 1; }

# Cópia estável das migrações e do script de papéis (não depende do workspace do Jenkins)
rm -rf "$VOLTIK_DIR/database.novo"
cp -r "$RAIZ/database" "$VOLTIK_DIR/database.novo"
rm -rf "$VOLTIK_DIR/database"
mv "$VOLTIK_DIR/database.novo" "$VOLTIK_DIR/database"

export AMBIENTE VERSAO VOLTIK_DIR
DC=(docker compose -p "voltik-$AMBIENTE" -f "$RAIZ/infra/server/compose.yml" --env-file "$VOLTIK_DIR/.env")

echo "==> [$AMBIENTE] base de dados"
"${DC[@]}" up -d --wait db

echo "==> [$AMBIENTE] migrações"
"${DC[@]}" run --rm migrate up

echo "==> [$AMBIENTE] API $VERSAO"
"${DC[@]}" up -d --wait api

echo "==> [$AMBIENTE] verificação"
"${DC[@]}" exec -T api wget -qO- http://127.0.0.1:8080/v1/saude
echo
echo "==> [$AMBIENTE] publicado: $VERSAO"
