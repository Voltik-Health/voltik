#!/bin/bash
# =============================================================================
# Preparação única do servidor Ubuntu (Oracle Cloud). Correr com sudo, uma vez.
# =============================================================================
set -euo pipefail

echo "==> Docker (repositório oficial)"
apt-get update
apt-get install -y ca-certificates curl gnupg
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  > /etc/apt/sources.list.d/docker.list
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

echo "==> Pastas dos ambientes"
mkdir -p /opt/voltik/{staging,producao,backups}
chmod 700 /opt/voltik/backups

echo "==> Rede partilhada entre o proxy e as APIs"
docker network inspect voltik-proxy >/dev/null 2>&1 || docker network create voltik-proxy

echo "==> Firewall interna do Ubuntu da Oracle (bloqueia 80/443 por omissão)"
apt-get install -y iptables-persistent
iptables -C INPUT -p tcp --dport 80  -j ACCEPT 2>/dev/null || iptables -I INPUT 6 -p tcp --dport 80  -j ACCEPT
iptables -C INPUT -p tcp --dport 443 -j ACCEPT 2>/dev/null || iptables -I INPUT 6 -p tcp --dport 443 -j ACCEPT
netfilter-persistent save

echo "Pronto. Falta: abrir 80 e 443 na Security List da Oracle (consola web)."
