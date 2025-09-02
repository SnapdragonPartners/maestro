#!/usr/bin/env sh
set -euo pipefail

export GH_CONFIG_DIR="${GH_CONFIG_DIR:-/dev/shm/gh}"
[ -d /dev/shm ] || GH_CONFIG_DIR="/tmp/gh"
mkdir -p "$GH_CONFIG_DIR"
chmod 700 "$GH_CONFIG_DIR"

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GIT_USER_NAME:=Maestro Agent}"
: "${GIT_USER_EMAIL:=maestro-agent@local}"

# Network required
printf '%s' "$GITHUB_TOKEN" | gh auth login --with-token -h github.com
gh auth setup-git
gh auth status -h github.com >/dev/null

git config --global user.name  "$GIT_USER_NAME"
git config --global user.email "$GIT_USER_EMAIL"

# Optional: verify remote if provided (network)
if [ -n "${REPO_URL:-}" ]; then
  git ls-remote --heads "$REPO_URL" >/dev/null
fi

echo "[gh-init] GitHub auth configured (ephemeral: $GH_CONFIG_DIR)"