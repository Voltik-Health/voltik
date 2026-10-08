# 0004 — Dois ambientes no servidor: staging e produção

- **Estado:** aceite
- **Data:** 2026-10-08

## Contexto
Somos dois a desenvolver e queremos testar no servidor sem pedir licença a ninguém, mas sem nunca pôr em produção código que não foi revisto. Correr backend e base de dados em cada Mac obriga a manter tudo igual nas duas máquinas.

## Decisão
- Ramo `staging`: push livre, sem aprovação. O Jenkins publica automaticamente em staging.
- Ramo `main`: protegido. Só entra por PR aberto a partir do ramo da tarefa (nunca a partir de `staging`), com 1 aprovação. O Jenkins pede confirmação manual antes de publicar em produção.
- Localmente só correm os frontends (web, Android, iOS), ligados à API de staging.
- Staging e produção correm na mesma VM Oracle, como projetos Docker Compose separados, cada um com a sua base de dados, volume e `.env`.
- Só as portas 80/443 estão abertas. Bases de dados e Jenkins só por túnel SSH.

## Consequências
Qualquer um testa no servidor em minutos e produção só recebe código aprovado. Staging é partilhado: uma migração errada afeta os dois, por isso existe `repor-staging.sh`. Staging nunca tem dados reais.
