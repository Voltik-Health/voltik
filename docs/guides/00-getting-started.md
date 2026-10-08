# Voltik getting-started guide

Step by step, done once, to set up the whole project:
GitHub, server, staging, production, databases, Jenkins and the team's Macs.

**Day-to-day flow, in short:**

```
task branch ──free push──▶ staging ──Jenkins──▶ staging-api   (testing, fictitious data)
       │
       └──PR + approval──▶ main ──Jenkins + confirmation──▶ api   (production, real data)

Mac: only the frontends (web, Android, iOS) run locally, pointing to the STAGING API.
```

| Who | Parts |
|---|---|
| Both of us, each on our own Mac | 1 |
| One of us, once | 2, 3, 4, 5, 6 |
| Both of us, always | 7, 8, 9 |

---

## Part 1 — Apps to install on the Mac

1. **Homebrew** (package manager), in Terminal:
   ```bash
   /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
   ```
2. **Command-line tools:**
   ```bash
   xcode-select --install        # git, compilers
   brew install go node uv       # Go (API), Node (Vue portal), uv (Python prediction service)
   ```
3. **Applications:**
   ```bash
   brew install --cask visual-studio-code android-studio dbeaver-community bruno bitwarden
   ```
   | App | What for |
   |---|---|
   | Visual Studio Code | Editor for Go, Vue and Python (or GoLand, free with the JetBrains student licence) |
   | Android Studio | Android app in Kotlin |
   | Xcode (App Store) | iOS app in Swift |
   | DBeaver Community | Browse and query the staging and production databases |
   | Bruno | Test the API (free, local alternative to Postman) |
   | Bitwarden | Share passwords between the two of us securely (never over WhatsApp) |
   | Docker Desktop | **Optional.** Only to test migrations locally. Download from docker.com (Apple Silicon) |

4. **SSH key** for GitHub and the server (if you don't have one yet):
   ```bash
   ssh-keygen -t ed25519 -C "your@email"
   cat ~/.ssh/id_ed25519.pub     # add it to GitHub and send it to whoever manages the server
   ```
5. **Git configuration:**
   ```bash
   git config --global user.name "Your Name"
   git config --global user.email "email@used.on.github"
   git config --global pull.ff only
   ```

> Keep the project **outside** iCloud-synced folders (Desktop/Documents when iCloud sync is on).
> iCloud corrupts `.git` folders. `~/Projects/voltik` is a safe place.

---

## Part 2 — GitHub repository

1. **Repository:** `voltik`, **public** (branch rules on private repositories require a paid plan; the repo has no secrets and no real data).
2. **Push the initial structure:**
   ```bash
   cd ~/Projects/voltik
   git init -b main
   git add .
   git commit -m "Initial repository structure"
   git remote add origin git@github.com:OWNER/voltik.git
   git push -u origin main
   ```
3. **Create the staging branch** from `main`:
   ```bash
   git switch -c staging
   git push -u origin staging
   git switch main
   ```
4. **Usernames in `CODEOWNERS`:** replace the placeholders in `.github/CODEOWNERS`.
5. **Invite the other developer:** **Settings → Collaborators → Add people**, role **Write**.
6. **Protect `main`:** **Settings → Rules → Rulesets → New branch ruleset**
   - Name `protect-main`, **Enforcement: Active**, empty bypass list, target **Include default branch**.
   - ✅ **Restrict deletions** and ✅ **Block force pushes**.
   - ✅ **Require a pull request before merging**, with **1 approval**, ✅ dismiss stale approvals, ✅ require review from Code Owners, ✅ require conversation resolution.
   - `staging` gets **no rules**: free push, and it can be reset with `--force` when needed.
7. **Settings → General → Pull Requests:** ✅ **Automatically delete head branches**.
8. **Check:** a direct `git push` to `main` must be rejected with `GH013: Repository rule violations`.
9. **Jira:** install the **GitHub for Jira** app and connect the repository. Commits and branches containing `VOLT-42` show up on the ticket.

---

## Part 3 — Server setup (once)

1. **Log in to the server:**
   ```bash
   ssh ubuntu@SERVER_IP
   ```
2. **Clone the repository** to `/opt/voltik/repo` (to run the scripts):
   ```bash
   sudo mkdir -p /opt/voltik && sudo chown ubuntu: /opt/voltik
   git clone https://github.com/OWNER/voltik.git /opt/voltik/repo
   ```
3. **Install Docker, network, folders and firewall rules:**
   ```bash
   sudo /opt/voltik/repo/infra/server/setup-server.sh
   sudo usermod -aG docker ubuntu     # log out and back in to SSH afterwards
   ```
4. **Open 80 and 443 on Oracle:** web console → **Networking → Virtual Cloud Networks → (the VCN) → Security Lists → Add Ingress Rules**: TCP 80 and TCP 443 from `0.0.0.0/0`.
   **Do not open** 5432, 5433, 5434 or 8080.
5. **Check the region** of the instance (top of the console). For real health data it must be in the EU.

---

## Part 4 — Addresses and HTTPS (Caddy proxy)

1. **Pick the addresses.** Without your own domain, use sslip.io with the public IP (e.g. `1.2.3.4`):
   - `staging-api.1-2-3-4.sslip.io`
   - `api.1-2-3-4.sslip.io`

   Later, with your own domain (around €10 a year), only these two values change.
2. **Configure and start the proxy:**
   ```bash
   cd /opt/voltik/repo/infra/proxy
   cp env.example .env && nano .env      # email + the two addresses
   docker compose up -d
   ```
   Caddy obtains and renews the HTTPS certificates on its own. Until an API is deployed the addresses answer with a 502 error: that is expected.

---

## Part 5 — Staging and production databases

Each environment has **its own** database, in a separate Docker project, with **different** passwords.

1. **Create both `.env` files:**
   ```bash
   cp /opt/voltik/repo/infra/server/env.staging.example    /opt/voltik/staging/.env
   cp /opt/voltik/repo/infra/server/env.production.example /opt/voltik/production/.env
   chmod 600 /opt/voltik/*/.env
   openssl rand -hex 24          # run once per password
   nano /opt/voltik/staging/.env
   nano /opt/voltik/production/.env
   ```
   Also store the passwords in the shared Bitwarden vault.
2. **Nothing has to be created by hand.** On the first deployment (part 6), `deploy.sh`:
   - starts PostgreSQL and creates the `voltik` database and the three roles (`database/init/01-roles.sh`);
   - applies every migration as `voltik_migrations`;
   - starts the API as `voltik_api`.
3. **Production backups** (after the first production deployment):
   ```bash
   crontab -e
   # add:
   30 3 * * * /opt/voltik/repo/infra/server/backup-production.sh >> /opt/voltik/backups/backup.log 2>&1
   ```
   Copying backups off the server (Oracle Object Storage) is still to do. It is marked in the script.

---

## Part 6 — Jenkins

1. **Install Jenkins LTS** following the official page, *Debian/Ubuntu* section:
   <https://www.jenkins.io/doc/book/installing/linux/> (includes installing Java 21).
2. **Give it access to Docker and the folders:**
   ```bash
   sudo usermod -aG docker jenkins
   sudo chown -R jenkins: /opt/voltik/staging /opt/voltik/production
   sudo systemctl restart jenkins
   ```
3. **Open the UI without exposing it to the Internet**, through an SSH tunnel from the Mac:
   ```bash
   ssh -L 8081:localhost:8080 ubuntu@SERVER_IP
   ```
   Open <http://localhost:8081> and finish the setup (*Install suggested plugins*), creating one account for each of us.
4. **GitHub credential:** **Manage Jenkins → Credentials → Add**: *Username with password*, with a GitHub username and a *fine-grained* token with read-only access to the repository's *Contents*.
5. **Pipeline:** **New Item → Multibranch Pipeline** → name `voltik`:
   - *Branch Sources* → **GitHub** → credential from step 4 → repository URL.
   - *Build Configuration*: `Jenkinsfile`.
   - *Scan Multibranch Pipeline Triggers*: ✅ **Periodically if not otherwise run**, every **1 minute**.
     (Jenkins checks for changes itself, so it never has to be exposed to receive webhooks.)
6. **First staging deployment:** in Jenkins, open the `staging` branch → **Build Now**. Then, on the Mac:
   ```bash
   curl https://staging-api.1-2-3-4.sslip.io/v1/health
   # {"environment":"staging","status":"ok","version":"..."}
   ```
7. **First production deployment:** open the `main` branch → **Build Now** → click **Deploy** when Jenkins asks for confirmation.
8. **Fictitious data on staging:**
   ```bash
   /opt/voltik/repo/infra/server/reset-staging.sh
   ```

---

## Part 7 — Connecting DBeaver to the server databases

New connection → **PostgreSQL**:

| Field | Staging | Production |
|---|---|---|
| Host | `localhost` | `localhost` |
| Port | `5433` | `5434` |
| Database | `voltik` | `voltik` |
| User | `voltik_readonly` | `voltik_readonly` |

**SSH** tab → ✅ *Use SSH Tunnel* → host = server IP, user `ubuntu`, key authentication (`~/.ssh/id_ed25519`).

In production **only** `voltik_readonly` is used. Production data is never changed by hand.

---

## Part 8 — Local frontends pointing to staging

Only the frontends run on the Mac. They always point to the **staging** API.

**Web portal (Vue).** Once the project exists in `web/`, create `web/.env.development`:
```
VITE_API_URL=https://staging-api.1-2-3-4.sslip.io/v1
```
and `web/.env.production`:
```
VITE_API_URL=https://api.1-2-3-4.sslip.io/v1
```
`npm run dev` runs on `http://localhost:5173`, which is already allowed by the staging CORS setting (`CORS_ORIGINS`).

**Android (Kotlin).** In `android/app/build.gradle.kts`, two flavours that can be installed side by side on the phone:
```kotlin
android {
    buildFeatures { buildConfig = true }
    flavorDimensions += "environment"
    productFlavors {
        create("staging") {
            dimension = "environment"
            applicationIdSuffix = ".staging"
            versionNameSuffix = "-staging"
            buildConfigField("String", "API_URL", "\"https://staging-api.1-2-3-4.sslip.io/v1/\"")
        }
        create("production") {
            dimension = "environment"
            buildConfigField("String", "API_URL", "\"https://api.1-2-3-4.sslip.io/v1/\"")
        }
    }
}
```
In code: `BuildConfig.API_URL`.

**iOS (Swift).** Two configurations (*Staging* and *Production*), each with its own `.xcconfig` file defining `API_URL`. The value is read through an `Info.plist` entry, and each configuration has its own *Scheme*.

---

## Part 9 — Day to day

1. **New task**, always from an up-to-date `main`:
   ```bash
   git switch main && git pull
   git switch -c VOLT-42-meal-logging
   ```
2. **Code and commit** (in English, with the Jira key in the message):
   ```bash
   git commit -am "VOLT-42 log meals by serving"
   ```
3. **Test on the server**, no approval needed, whenever you want:
   ```bash
   ./scripts/to-staging.sh
   ```
   It merges your branch into `staging` and pushes; Jenkins deploys in about 1–2 minutes.
   Test with the local frontend, with Bruno, or with the staging build of the app on your phone.
4. **When everything works:** open on GitHub a **pull request from the task branch to `main`**. Never from `staging` to `main`, because `staging` holds the other developer's unfinished work.
5. **The other developer reviews and approves.** Merge.
6. **Jenkins asks for confirmation** and deploys to production.

**Database, day to day:**
- New schema change: `./scripts/new-migration.sh change_name`, then write the `.up.sql` and the `.down.sql`.
- It goes to staging with the rest of the code, and only reaches production after the PR is approved.
- A migration that has already run on staging is never edited: fix it with another migration.
- If staging gets tangled with migrations from abandoned branches: `infra/server/reset-staging.sh` on the server.

**Periodic staging reset.** Every so often (for example at the end of each sprint), realign `staging` with `main`:
```bash
git switch staging && git fetch origin
git reset --hard origin/main
git push --force origin staging
git switch -
```
If there are abandoned migrations, also run `reset-staging.sh` on the server.

---

## Rules we never break

1. Secrets (passwords, tokens, keys) never go into Git.
2. Real data never goes into staging or onto the Macs.
3. No database port (5432, 5433, 5434) and no Jenkins port (8080) is opened on Oracle.
4. `main` only receives code through an approved PR.
5. The schema only changes through migrations, and a deployed migration is never edited.
6. The API always uses the `voltik_api` role; nobody uses `postgres` day to day.
7. Sign-in is external only: Voltik never stores passwords.
8. Everything in the repository is written in English.
