# Jenkins

Multibranch Pipeline defined in the root [`Jenkinsfile`](../../Jenkinsfile): tests → staging → production.
Jenkins polls GitHub every minute, so it never needs to be exposed to the Internet; its UI is reached through an SSH tunnel.
Credentials (GitHub token) live in Jenkins *Credentials*, never in the repository.
Setup steps: [`docs/guides/00-getting-started.md`](../../docs/guides/00-getting-started.md), part 6.
