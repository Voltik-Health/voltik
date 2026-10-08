# 0002 — PostgreSQL com migrações versionadas

- **Estado:** aceite
- **Data:** 2026-10-08

## Contexto
Dados muito relacionais (pacientes, cuidadores, profissionais, empresas) e dados de saúde que exigem integridade.

## Decisão
PostgreSQL 17 em Docker. Esquema gerido com golang-migrate. Três papéis com permissões mínimas. Datas em `TIMESTAMPTZ`.

## Consequências
O esquema é igual em todos os ambientes e qualquer alteração é revista. Exige disciplina: nunca alterar o esquema à mão.
