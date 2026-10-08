#!/bin/bash
# =============================================================================
# Cópia de segurança diária da base de dados de PRODUÇÃO.
# Agendar no servidor (crontab -e):
#   30 3 * * * /opt/voltik/repo/infra/server/backup-producao.sh >> /opt/voltik/backups/backup.log 2>&1
# =============================================================================
set -euo pipefail
DESTINO=/opt/voltik/backups
mkdir -p "$DESTINO"
FICHEIRO="$DESTINO/producao-$(date -u +%Y%m%dT%H%M%SZ).dump"

docker compose -p voltik-producao exec -T db pg_dump -U voltik_migracoes -d voltik -Fc > "$FICHEIRO"
chmod 600 "$FICHEIRO"
echo "Cópia criada: $FICHEIRO ($(du -h "$FICHEIRO" | cut -f1))"

# Guardar só os últimos 14 dias neste servidor
find "$DESTINO" -name 'producao-*.dump' -mtime +14 -delete

# PASSO EM FALTA: enviar a cópia para fora do servidor (Object Storage da Oracle), ex.:
# oci os object put --bucket-name voltik-backups --file "$FICHEIRO"
