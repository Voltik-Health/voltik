# Backend (Go)

API REST do Voltik. O contrato está em [`../api/openapi.yaml`](../api/openapi.yaml).

## Arranque

```bash
cd backend
go mod init github.com/<organizacao>/voltik/backend
```

## Organização em três camadas

| Pasta | Responsabilidade |
|---|---|
| `cmd/api/` | `main.go`: arranca o servidor e liga as peças |
| `internal/handler/` | Recebe o pedido HTTP, lê o JSON, chama o serviço. **Sem regras de negócio.** |
| `internal/service/` | Lógica de negócio: leituras, alertas, permissões, relatórios |
| `internal/repository/` | Acesso ao PostgreSQL |

Com a lógica separada do transporte, acrescentar gRPC no futuro é só criar novos handlers sobre os mesmos serviços.

A API liga-se à base de dados com o papel **`voltik_api`**, nunca com `postgres` nem `voltik_migracoes`.
