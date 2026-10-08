# Guia de arranque do Voltik

Passo a passo para pôr de pé, uma única vez, toda a organização do projeto:
GitHub, servidor, staging, produção, base de dados, Jenkins e os Macs da equipa.

**Como funciona no dia a dia, em resumo:**

```
ramo da tarefa ──push livre──▶ staging ──Jenkins──▶ staging-api   (testes, dados fictícios)
       │
       └──PR + aprovação──▶ main ──Jenkins + confirmação──▶ api   (produção, dados reais)

Mac: só os frontends (web, Android, iOS) correm localmente, ligados à API de STAGING.
```

| Quem faz | Partes |
|---|---|
| Os dois, cada um no seu Mac | 1 |
| Um dos dois, uma vez | 2, 3, 4, 5, 6 |
| Os dois, sempre | 7, 8, 9 |

---

## Parte 1 — Apps a instalar no Mac

1. **Homebrew** (gestor de pacotes), no Terminal:
   ```bash
   /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
   ```
2. **Ferramentas de linha de comandos:**
   ```bash
   xcode-select --install        # git, compiladores
   brew install go node uv       # Go (API), Node (portal Vue), uv (Python do serviço de previsão)
   ```
3. **Aplicações:**
   ```bash
   brew install --cask visual-studio-code android-studio dbeaver-community bruno bitwarden
   ```
   | App | Para quê |
   |---|---|
   | Visual Studio Code | Editor para Go, Vue e Python (ou GoLand, gratuito com a licença de estudante JetBrains) |
   | Android Studio | App Android em Kotlin |
   | Xcode (App Store) | App iOS em Swift |
   | DBeaver Community | Ver e consultar as bases de dados de staging e produção |
   | Bruno | Testar a API (alternativa gratuita e local ao Postman) |
   | Bitwarden | Partilhar palavras-passe entre os dois, de forma segura (nunca por WhatsApp) |
   | Docker Desktop | **Opcional.** Só para testar migrações localmente. Descarregar de docker.com (versão Apple Silicon) |

4. **Chave SSH** para o servidor (se ainda não tiverem):
   ```bash
   ssh-keygen -t ed25519 -C "o.vosso@email"
   cat ~/.ssh/id_ed25519.pub     # enviar esta linha a quem gere o servidor
   ```
5. **Configurar o Git:**
   ```bash
   git config --global user.name "Rui Passos"
   git config --global user.email "email@usado.no.github"
   git config --global pull.ff only
   ```

---

## Parte 2 — Repositório no GitHub

1. **Organização:** github.com → **+** → **New organization** → **Free**. Nome `voltik` (ou `voltik-app` se estiver ocupado). Convidar o outro elemento como **Owner**.
2. **Repositório:** **New repository** → nome `voltik` → **Private** → sem README.
3. **Enviar a estrutura inicial** (a partir do ZIP):
   ```bash
   cd ~/Projetos
   unzip ~/Downloads/voltik-repositorio-inicial.zip
   cd voltik
   git init -b main
   git add .
   git commit -m "Estrutura inicial do Voltik"
   git remote add origin https://github.com/ORGANIZACAO/voltik.git
   git push -u origin main
   ```
4. **Criar o ramo de staging**, a partir de `main`:
   ```bash
   git switch -c staging
   git push -u origin staging
   git switch main
   ```
5. **Proteger o `main`:** **Settings → Rules → Rulesets → New branch ruleset**
   - Nome `producao`, alvo `main`, **Enforcement: Active**.
   - ✅ **Require a pull request before merging**, com **1 approval**.
   - ✅ **Block force pushes** e ✅ **Restrict deletions**.
   - O ramo `staging` **não leva regras**: push livre, e pode ser reposto com `--force` quando for preciso.

   > Num repositório **privado** de uma organização gratuita, as regras podem não estar disponíveis.
   > Se for o caso: tornar o repositório público (não tem segredos nem dados reais) ou cumprir a regra por acordo.
6. **Nomes de utilizador no `CODEOWNERS`:** editar `.github/CODEOWNERS` (já com o fluxo novo, num ramo + PR).
7. **Jira:** instalar a app **GitHub for Jira** e ligar o repositório. Commits e ramos com `VOLT-42` aparecem no ticket.

---

## Parte 3 — Preparar o servidor (uma vez)

1. **Entrar no servidor:**
   ```bash
   ssh ubuntu@IP_DO_SERVIDOR
   ```
2. **Clonar o repositório** para `/opt/voltik/repo` (para correr os scripts):
   ```bash
   sudo mkdir -p /opt/voltik && sudo chown ubuntu: /opt/voltik
   git clone https://github.com/ORGANIZACAO/voltik.git /opt/voltik/repo
   ```
   Num repositório privado, o GitHub pede um **token** em vez da palavra-passe:
   **Settings (pessoal) → Developer settings → Fine-grained tokens**, só com leitura de *Contents* deste repositório.
3. **Instalar Docker, rede, pastas e firewall:**
   ```bash
   sudo /opt/voltik/repo/infra/server/preparar-servidor.sh
   sudo usermod -aG docker ubuntu     # sair e voltar a entrar no SSH depois disto
   ```
4. **Abrir 80 e 443 na Oracle:** consola web → **Networking → Virtual Cloud Networks → (a VCN) → Security Lists → Add Ingress Rules**: TCP 80 e TCP 443 de `0.0.0.0/0`.
   **Não abrir** a 5432, 5433, 5434 nem a 8080.
5. **Confirmar a região** da instância (canto superior da consola). Para dados reais de saúde tem de ser na UE.

---

## Parte 4 — Endereços e HTTPS (proxy Caddy)

1. **Escolher os endereços.** Sem domínio próprio, usar o sslip.io com o IP público (ex.: `1.2.3.4`):
   - `staging-api.1-2-3-4.sslip.io`
   - `api.1-2-3-4.sslip.io`

   Mais tarde, com um domínio próprio (cerca de 10 € por ano), basta mudar estes dois valores.
2. **Configurar e arrancar o proxy:**
   ```bash
   cd /opt/voltik/repo/infra/proxy
   cp env.example .env && nano .env      # email + os dois endereços
   docker compose up -d
   ```
   O Caddy pede e renova os certificados HTTPS sozinho. Até haver API publicada, os endereços respondem com erro 502: é normal.

---

## Parte 5 — Bases de dados de staging e produção

Cada ambiente tem a **sua** base de dados, num projeto Docker separado, com palavras-passe **diferentes**.

1. **Criar os dois `.env`:**
   ```bash
   cp /opt/voltik/repo/infra/server/env.staging.example  /opt/voltik/staging/.env
   cp /opt/voltik/repo/infra/server/env.producao.example /opt/voltik/producao/.env
   chmod 600 /opt/voltik/*/.env
   openssl rand -hex 24          # correr uma vez por palavra-passe
   nano /opt/voltik/staging/.env
   nano /opt/voltik/producao/.env
   ```
   Guardar as palavras-passe também no Bitwarden partilhado.
2. **Não é preciso criar nada à mão.** Na primeira publicação (Parte 6), o `publicar.sh`:
   - arranca o PostgreSQL e cria a base `voltik` e os três papéis (`database/init/01-papeis.sh`);
   - aplica todas as migrações com o papel `voltik_migracoes`;
   - arranca a API com o papel `voltik_api`.
3. **Cópias de segurança de produção** (depois da primeira publicação em produção):
   ```bash
   crontab -e
   # acrescentar:
   30 3 * * * /opt/voltik/repo/infra/server/backup-producao.sh >> /opt/voltik/backups/backup.log 2>&1
   ```
   Falta ainda enviar as cópias para fora do servidor (Object Storage da Oracle). Está marcado no script.

---

## Parte 6 — Jenkins

1. **Instalar o Jenkins LTS** seguindo a página oficial, secção *Debian/Ubuntu*:
   <https://www.jenkins.io/doc/book/installing/linux/> (inclui instalar o Java 21).
2. **Dar-lhe acesso ao Docker e às pastas:**
   ```bash
   sudo usermod -aG docker jenkins
   sudo chown -R jenkins: /opt/voltik/staging /opt/voltik/producao
   sudo systemctl restart jenkins
   ```
3. **Aceder à interface sem a expor à Internet**, por túnel SSH a partir do Mac:
   ```bash
   ssh -L 8081:localhost:8080 ubuntu@IP_DO_SERVIDOR
   ```
   Abrir <http://localhost:8081> e concluir a instalação (*Install suggested plugins*), criando uma conta para cada um.
4. **Credencial do GitHub:** **Manage Jenkins → Credentials → Add**: *Username with password*, com o nome de utilizador do GitHub e um token *fine-grained* só de leitura de *Contents* do repositório.
5. **Pipeline:** **New Item → Multibranch Pipeline** → nome `voltik`:
   - *Branch Sources* → **GitHub** → credencial do passo 4 → URL do repositório.
   - *Build Configuration*: `Jenkinsfile`.
   - *Scan Multibranch Pipeline Triggers*: ✅ **Periodically if not otherwise run**, a cada **1 minuto**.
     (O Jenkins vai ver se há alterações; assim não é preciso expô-lo à Internet para receber webhooks.)
6. **Primeira publicação em staging:** no Jenkins, abrir o ramo `staging` → **Build Now**. No fim, no Mac:
   ```bash
   curl https://staging-api.1-2-3-4.sslip.io/v1/saude
   # {"ambiente":"staging","estado":"ok","versao":"..."}
   ```
7. **Primeira publicação em produção:** abrir o ramo `main` → **Build Now** → carregar em **Publicar** quando o Jenkins pedir confirmação.
8. **Dados fictícios em staging:**
   ```bash
   /opt/voltik/repo/infra/server/repor-staging.sh
   ```

---

## Parte 7 — Ligar o DBeaver às bases de dados do servidor

Nova ligação → **PostgreSQL**:

| Campo | Staging | Produção |
|---|---|---|
| Host | `localhost` | `localhost` |
| Porta | `5433` | `5434` |
| Base de dados | `voltik` | `voltik` |
| Utilizador | `voltik_leitura` | `voltik_leitura` |

Separador **SSH** → ✅ *Use SSH Tunnel* → host = IP do servidor, utilizador `ubuntu`, autenticação por chave (`~/.ssh/id_ed25519`).

Em produção usa-se **só** o `voltik_leitura`. Não se alteram dados de produção à mão.

---

## Parte 8 — Frontends locais ligados ao staging

Só os frontends correm no Mac. Apontam sempre para a API de **staging**.

**Portal web (Vue).** Depois de criado o projeto em `web/`, criar `web/.env.development`:
```
VITE_API_URL=https://staging-api.1-2-3-4.sslip.io/v1
```
e `web/.env.production`:
```
VITE_API_URL=https://api.1-2-3-4.sslip.io/v1
```
`npm run dev` corre em `http://localhost:5173`, que já está autorizado no CORS de staging (`CORS_ORIGENS`).

**Android (Kotlin).** Em `android/app/build.gradle.kts`, duas variantes, que podem estar instaladas lado a lado no telemóvel:
```kotlin
android {
    buildFeatures { buildConfig = true }
    flavorDimensions += "ambiente"
    productFlavors {
        create("staging") {
            dimension = "ambiente"
            applicationIdSuffix = ".staging"
            versionNameSuffix = "-staging"
            buildConfigField("String", "API_URL", "\"https://staging-api.1-2-3-4.sslip.io/v1/\"")
        }
        create("producao") {
            dimension = "ambiente"
            buildConfigField("String", "API_URL", "\"https://api.1-2-3-4.sslip.io/v1/\"")
        }
    }
}
```
No código: `BuildConfig.API_URL`.

**iOS (Swift).** Duas configurações (*Staging* e *Produção*) com um ficheiro `.xcconfig` cada, onde se define `API_URL`. Esse valor é lido através de uma entrada no `Info.plist`, e cada configuração tem o seu *Scheme*.

---

## Parte 9 — O dia a dia

1. **Nova tarefa**, sempre a partir de `main` atualizado:
   ```bash
   git switch main && git pull
   git switch -c VOLT-42-registo-refeicoes
   ```
2. **Programar e fazer commits** (com a chave do Jira na mensagem):
   ```bash
   git commit -am "VOLT-42 registo de refeições por porções"
   ```
3. **Testar no servidor**, sem aprovação, sempre que quiserem:
   ```bash
   ./scripts/para-staging.sh
   ```
   Junta o vosso ramo ao `staging`, faz push, e o Jenkins publica em cerca de 1 a 2 minutos.
   Testam com o frontend local, com o Bruno ou com a app no telemóvel (variante staging).
4. **Quando estiver tudo a funcionar:** abrir no GitHub uma **pull request do ramo da tarefa para `main`**. Nunca do `staging` para `main`, porque o `staging` tem trabalho por acabar do outro.
5. **O outro revê e aprova.** Merge.
6. **O Jenkins pede confirmação** e publica em produção.

**Base de dados no dia a dia:**
- Nova alteração ao esquema: `./scripts/nova-migracao.sh nome_da_alteracao` e escrever o `.up.sql` e o `.down.sql`.
- Vai para staging com o resto do código, e só chega a produção depois da PR aprovada.
- Uma migração que já correu em staging não se edita: corrige-se com outra migração.
- Se o staging ficar baralhado com migrações de ramos abandonados: `infra/server/repor-staging.sh` no servidor.

**Reposição periódica do staging.** De vez em quando (por exemplo, no fim de cada sprint), alinhar o `staging` com o `main`:
```bash
git switch staging && git fetch origin
git reset --hard origin/main
git push --force origin staging
git switch -
```
Se houver migrações abandonadas, correr também o `repor-staging.sh` no servidor.

---

## Regras que não se quebram

1. Segredos (palavras-passe, tokens, chaves) nunca entram no Git.
2. Dados reais nunca entram em staging nem nos Macs.
3. Nenhuma porta de base de dados (5432, 5433, 5434) nem o Jenkins (8080) é aberto na Oracle.
4. `main` só recebe código por PR aprovada.
5. O esquema só muda por migração, e uma migração publicada nunca se edita.
6. A API usa sempre o papel `voltik_api`; ninguém usa o `postgres` no dia a dia.
