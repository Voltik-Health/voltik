# Infraestrutura

- **Servidor:** Oracle Cloud, instância Ampere (ARM64), Ubuntu Server LTS.
- **Serviços:** contentores Docker (PostgreSQL, API em Go, serviço de previsão, Jenkins).
- **Rede:** só as portas 22, 80 e 443 abertas na security list. **A 5432 nunca.**
- **Imagens:** construir para `linux/arm64` (o servidor é ARM).

## Cópias de segurança (a configurar)

- `pg_dump` diário, cifrado, guardado fora do servidor (Object Storage).
- Testar o restauro pelo menos uma vez por mês.
