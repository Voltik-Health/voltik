# 0001 — Um único repositório para todo o projeto

- **Estado:** aceite
- **Data:** 2026-10-08

## Contexto
O Voltik tem apps Android e iOS, portal web, API, serviço de previsão e base de dados, que partilham o mesmo contrato de API.

## Decisão
Monorepo, com uma pasta por componente. O Jenkins usa filtros por caminho para só construir o que mudou.

## Consequências
Uma alteração ao contrato e aos clientes entra numa única pull request. A configuração do CI é ligeiramente mais elaborada.
