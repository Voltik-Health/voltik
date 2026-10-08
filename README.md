# Voltik

Plataforma de monitorização preditiva da glicemia, com a assistente **Iris**.
Avisa as pessoas com diabetes **antes** de uma hipoglicemia, cruzando o sensor de glicemia contínua com a atividade física do relógio.

Projeto de Rui Passos e Afonso Carvalho · CTeSP TPSI · IPMAIA · 2026

**Para começar:** [`docs/guias/00-guia-de-arranque.md`](docs/guias/00-guia-de-arranque.md)

## Ambientes

| Ambiente | Ramo | Como lá chega o código | Dados |
|---|---|---|---|
| **Staging** | `staging` | Push livre, sem aprovação (`./scripts/para-staging.sh`). O Jenkins publica sozinho. | Fictícios |
| **Produção** | `main` | Pull request com aprovação do outro elemento + confirmação no Jenkins | Reais |
| Local | qualquer | Só os frontends correm no Mac, ligados à API de **staging** | — |

## Estrutura do repositório

| Pasta | Conteúdo | Tecnologia |
|---|---|---|
| `android/` | App Android | Kotlin + Jetpack Compose (Android Studio) |
| `ios/` | App iOS | Swift + SwiftUI (Xcode) |
| `web/` | Portal web | Vue 3 |
| `backend/` | API | Go (REST, documentada em OpenAPI) |
| `ml/` | Serviço de previsão | Python + FastAPI |
| `api/` | Contrato da API, partilhado por todos os clientes | OpenAPI 3 |
| `database/` | Migrações, papéis e dados de exemplo | PostgreSQL 17 + golang-migrate |
| `infra/` | Servidor, proxy HTTPS e scripts de publicação | Oracle Cloud, Docker, Caddy |
| `scripts/` | Ajudas do dia a dia (enviar para staging, nova migração) | Bash |
| `docs/` | Guias, entregas, diagramas e decisões técnicas | |
| `Jenkinsfile` | Pipeline: testes → staging → produção | Jenkins |

## Regras da equipa

- `main` é produção: só recebe código por pull request aprovada pelo outro elemento.
- `staging` é o ambiente de testes partilhado: push livre, a qualquer momento.
- Cada tarefa num ramo próprio, criado a partir de `main`, com a chave do Jira: `VOLT-42-registo-refeicoes`.
- A pull request para `main` é sempre do **ramo da tarefa**, nunca do ramo `staging`.
- O esquema da base de dados só muda com uma nova migração (`./scripts/nova-migracao.sh`). Nunca à mão.
- Segredos nunca entram no Git. Dados reais nunca entram em staging.
