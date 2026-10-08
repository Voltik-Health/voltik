# Pipelines Jenkins

Pipeline prevista: GitHub → testes → imagens Docker (arm64) → migrações → publicação na Oracle Cloud.
As credenciais (base de dados, SSH) ficam nas *Credentials* do Jenkins, nunca no repositório.
