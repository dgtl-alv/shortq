#!/usr/bin/env bash
set -euo pipefail

APP_DIR="/home/fitrah/apps/shortq"
BRANCH="main"
LOCAL_HEALTH="http://127.0.0.1:8000/healthz"

cd "$APP_DIR"

printf 'shortq deploy: fetch %s\n' "$BRANCH"
git fetch --prune origin "$BRANCH"

printf 'shortq deploy: reset origin/%s\n' "$BRANCH"
git reset --hard "origin/$BRANCH"

printf 'shortq deploy: compose config\n'
docker compose config --quiet

printf 'shortq deploy: build and restart\n'
docker compose up -d --build

printf 'shortq deploy: health\n'
for i in $(seq 1 30); do
  if curl -fsS "$LOCAL_HEALTH"; then
    printf '\nshortq deploy: ok\n'
    exit 0
  fi
  sleep 2
done
printf 'shortq deploy: health failed\n' >&2
exit 1
