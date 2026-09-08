#!/usr/bin/env bash
set -euo pipefail

DEPLOY_ENV="${SHORTQ_DEPLOY_ENV:?SHORTQ_DEPLOY_ENV is required}"
DEPLOY_REF="${SHORTQ_DEPLOY_REF:?SHORTQ_DEPLOY_REF is required}"

case "$DEPLOY_ENV:$DEPLOY_REF" in
  staging:main)
    APP_DIR="${SHORTQ_APP_DIR:-/opt/alva/apps/staging/shortq}"
    COMPOSE_FILE="docker-compose.staging.yml"
    PROJECT_NAME="shortq-staging"
    LOCAL_HEALTH="${SHORTQ_HEALTH_URL:-http://127.0.0.1:8000/healthz}"
    ;;
  production:prod-*)
    APP_DIR="${SHORTQ_APP_DIR:-/opt/alva/apps/prod/shortq}"
    COMPOSE_FILE="docker-compose.production.yml"
    PROJECT_NAME="shortq-production"
    LOCAL_HEALTH="${SHORTQ_HEALTH_URL:-http://127.0.0.1:8000/healthz}"
    ;;
  *)
    printf 'shortq deploy: ref %s cannot deploy to %s\n' "$DEPLOY_REF" "$DEPLOY_ENV" >&2
    exit 1
    ;;
esac

if docker compose version >/dev/null 2>&1; then
  COMPOSE=(docker compose)
elif docker-compose version >/dev/null 2>&1; then
  COMPOSE=(docker-compose)
else
  printf 'shortq deploy: Docker Compose not found\n' >&2
  exit 1
fi

cd "$APP_DIR"
printf 'shortq deploy: fetch %s\n' "$DEPLOY_REF"
if [ "$DEPLOY_REF" = main ]; then
  git fetch --prune origin main
  DEPLOY_COMMIT="origin/main"
else
  git fetch --force origin "refs/tags/$DEPLOY_REF:refs/tags/$DEPLOY_REF"
  DEPLOY_COMMIT="refs/tags/$DEPLOY_REF"
fi

git reset --hard "$DEPLOY_COMMIT"
"${COMPOSE[@]}" -p "$PROJECT_NAME" -f "$COMPOSE_FILE" config --quiet
"${COMPOSE[@]}" -p "$PROJECT_NAME" -f "$COMPOSE_FILE" up -d --build

for _ in $(seq 1 30); do
  curl -fsS "$LOCAL_HEALTH" && exit 0
  sleep 2
done
printf 'shortq deploy: health failed\n' >&2
exit 1
