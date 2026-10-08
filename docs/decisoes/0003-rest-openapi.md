# 0003 — REST com JSON e OpenAPI, em vez de gRPC

- **Estado:** aceite
- **Data:** 2026-10-08

## Contexto
Os clientes incluem um portal web, e o browser não fala gRPC diretamente.

## Decisão
REST com JSON, contrato em `api/openapi.yaml`, a partir do qual se geram os clientes.

## Consequências
Menos peças a manter. Com a lógica separada em camadas no backend, é possível acrescentar gRPC mais tarde (por exemplo entre a API e o serviço de previsão) sem reescrever o sistema.
