#!/bin/bash
# =============================================================================
# Envia o ramo atual para STAGING (sem aprovação), para testar no servidor.
#   ./scripts/para-staging.sh
# O Jenkins publica automaticamente em staging-api.
# =============================================================================
set -euo pipefail

RAMO="$(git branch --show-current)"
case "$RAMO" in
  main|staging) echo "Corra isto a partir do ramo da sua tarefa (ex.: VOLT-42-...)."; exit 1;;
esac
git diff --quiet && git diff --cached --quiet || { echo "Tem alterações por fazer commit."; exit 1; }

git push -u origin "$RAMO"
git fetch origin
git switch staging
git reset --hard origin/staging
git merge --no-edit "$RAMO"
git push origin staging
git switch "$RAMO"
echo "Enviado para staging. Acompanhe no Jenkins."
