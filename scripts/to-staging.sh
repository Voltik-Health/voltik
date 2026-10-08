#!/bin/bash
# =============================================================================
# Sends the current branch to STAGING (no approval needed) to test it on the server.
#   ./scripts/to-staging.sh
# Jenkins then deploys it to the staging API automatically.
# =============================================================================
set -euo pipefail

BRANCH="$(git branch --show-current)"
case "$BRANCH" in
  main|staging) echo "Run this from your task branch (e.g. DVT-42-...)."; exit 1;;
esac
git diff --quiet && git diff --cached --quiet || { echo "You have uncommitted changes."; exit 1; }

git push -u origin "$BRANCH"
git fetch origin
git switch staging
git reset --hard origin/staging
git merge --no-edit "$BRANCH"
git push origin staging
git switch "$BRANCH"
echo "Sent to staging. Follow the build in Jenkins."
