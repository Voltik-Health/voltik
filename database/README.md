# Base de dados

PostgreSQL 17 em Docker. O esquema é gerido por **migrações** com o
[golang-migrate](https://github.com/golang-migrate/migrate): cada alteração é um par de ficheiros numerados em `migrations/`.

## Pastas

| Pasta | Para quê |
|---|---|
| `migrations/` | Alterações ao esquema, por ordem. `NNNNNN_nome.up.sql` aplica, `.down.sql` reverte. |
| `init/` | Corre uma única vez, no primeiro arranque: cria a base `voltik` e os três papéis. |
| `seeds/` | Dados **fictícios** para desenvolvimento. |

## Papéis (utilizadores da base de dados)

| Papel | Pode | Usado por |
|---|---|---|
| `postgres` | Tudo (superutilizador) | Só administração. Nunca na aplicação. |
| `voltik_migracoes` | Criar e alterar tabelas | golang-migrate |
| `voltik_api` | Ler e escrever dados; na auditoria só pode inserir | API em Go |
| `voltik_leitura` | Só ler | DBeaver no dia a dia, análises |

## Onde corre

| Ambiente | Onde | Porta para o túnel SSH |
|---|---|---|
| Staging | Servidor, projeto Docker `voltik-staging` | `5433` |
| Produção | Servidor, projeto Docker `voltik-producao` | `5434` |
| Local (opcional) | Mac, `docker-compose.yml` da raiz | `5432` |

As migrações são aplicadas **pelo Jenkins** em cada publicação (`infra/server/publicar.sh`).
Os comandos abaixo são para a base de dados local opcional.

## Comandos (local, opcional)

```bash
# Arrancar / parar
docker compose up -d db
docker compose stop db

# Migrações
docker compose --profile ferramentas run --rm migrate up        # aplica tudo o que falta
docker compose --profile ferramentas run --rm migrate down 1    # reverte a última
docker compose --profile ferramentas run --rm migrate version   # versão atual

# Dados de exemplo
docker compose exec -T db psql -U voltik_migracoes -d voltik < database/seeds/dev_dados_exemplo.sql

# Consola SQL
docker compose exec db psql -U voltik_leitura -d voltik

# Recomeçar do zero (APAGA TUDO o que está no volume local)
docker compose down -v
```

## Criar uma nova migração

1. `./scripts/nova-migracao.sh adicionar_indice_relatorios`
   Cria `AAAAMMDDHHMMSS_adicionar_indice_relatorios.up.sql` e `.down.sql`.
   O nome leva data e hora para que os dois não criem migrações com o mesmo número.
2. O `.up.sql` faz a alteração; o `.down.sql` desfaz exatamente essa alteração.
3. (Recomendado) Testar `up`, `down 1` e `up` na base de dados local, para não partir o staging ao outro.
4. `./scripts/para-staging.sh`: o Jenkins aplica-a em staging.
5. Pull request para `main`: depois de aprovada e confirmada, o Jenkins aplica-a em produção.

**Nunca editar uma migração que já foi aplicada em staging ou produção.** Corrige-se sempre com uma migração nova.
Se o staging ficar com migrações de ramos abandonados: `infra/server/repor-staging.sh` no servidor.

## Ligar com o DBeaver

- **No Mac (desenvolvimento):** host `localhost`, porta `5432`, base `voltik`, utilizador `voltik_leitura`.
- **Staging / produção:** host `localhost`, porta `5433` (staging) ou `5434` (produção), base `voltik`, utilizador `voltik_leitura`.
  No separador **SSH**: ativar o túnel com o IP do servidor, utilizador `ubuntu` e a vossa chave privada.
  Estas portas nunca são abertas na Oracle: só existem dentro do servidor.
- Em produção, usar **sempre** `voltik_leitura`. Alterações a dados de produção à mão: nunca.

## Convenções do esquema

- Identificadores `UUID` com `gen_random_uuid()`; leituras, previsões e auditoria usam `BIGSERIAL`.
- Datas e horas sempre em `TIMESTAMPTZ`, guardadas em UTC.
- Toda a chave estrangeira usada em pesquisas tem índice (o PostgreSQL não os cria sozinho).
- Valores fechados com `CHECK (... IN (...))`; confirmar que cabem no `VARCHAR` da coluna.

## Decisão em aberto

`ON DELETE CASCADE` em `pacientes` e `utilizadores` apaga todo o histórico clínico quando se apaga uma conta.
Decidir se é esse o comportamento, ou se se desativa e anonimiza a conta (preferível para dados de saúde).
Registar a decisão em `docs/decisoes/`.
