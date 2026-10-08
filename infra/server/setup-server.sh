#!/bin/bash
# =============================================================================
# One-time setup of the Ubuntu server (Oracle Cloud). Run once, with sudo.
# =============================================================================
set -euo pipefail

echo "==> Docker (official repository)"
apt-get update
apt-get install -y ca-certificates curl gnupg
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  > /etc/apt/sources.list.d/docker.list
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

echo "==> Environment folders"
mkdir -p /opt/voltik/{staging,production,backups}
chmod 700 /opt/voltik/backups

echo "==> Network shared by the proxy and the APIs"
docker network inspect voltik-proxy >/dev/null 2>&1 || docker network create voltik-proxy

echo "==> Oracle's Ubuntu firewall (blocks 80/443 by default)"
apt-get install -y iptables-persistent
iptables -C INPUT -p tcp --dport 80  -j ACCEPT 2>/dev/null || iptables -I INPUT 6 -p tcp --dport 80  -j ACCEPT
iptables -C INPUT -p tcp --dport 443 -j ACCEPT 2>/dev/null || iptables -I INPUT 6 -p tcp --dport 443 -j ACCEPT
netfilter-persistent save

echo "Done. Still to do: open 80 and 443 in the Oracle Security List (web console)."
