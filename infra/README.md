# Infrastructure

- **Server:** Oracle Cloud, Ampere instance (ARM64), Ubuntu Server LTS, EU region.
- **Services:** Docker containers (PostgreSQL, Go API, prediction service, Caddy proxy). Jenkins runs natively on the host.
- **Network:** only ports 22, 80 and 443 open in the Security List. **Never 5432/5433/5434 or 8080.**
- **Images:** built on the server itself, so they are already `linux/arm64`.

| Folder | Contents |
|---|---|
| `server/` | Per-environment Compose file, deployment, staging reset, backups, one-time server setup |
| `proxy/` | Caddy HTTPS reverse proxy shared by both environments |
| `jenkins/` | Notes on the Jenkins setup |

## Backups

- Daily `pg_dump` of production (`server/backup-production.sh`), 14 days kept on the server.
- Still to do: copy each backup off the server (Oracle Object Storage) and test a restore at least once a month.
