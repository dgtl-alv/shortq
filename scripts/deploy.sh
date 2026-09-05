#!/usr/bin/env bash
set -euo pipefail
APP_DIR="${SHORTQ_APP_DIR:-/opt/alva/apps/prod/shortq}"
BRANCH="${SHORTQ_BRANCH:-main}"
COMPOSE_FILE="${SHORTQ_COMPOSE_FILE:-docker-compose.yml}"
PROJECT_NAME="${SHORTQ_PROJECT_NAME:-shortq}"
LOCAL_HEALTH="${SHORTQ_HEALTH_URL:-http://127.0.0.1:8000/healthz}"
cd "$APP_DIR"
git fetch --prune origin "$BRANCH"
git reset --hard "origin/$BRANCH"
docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" config --quiet
docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" up -d --build
a=0; while [ "$a" -lt 30 ]; do curl -fsS "$LOCAL_HEALTH" && exit 0; a=$((a+1)); sleep 2; done
printf 'shortq deploy: health failed\n' >&2
exit 1
