#!/usr/bin/env bash
set -Eeuo pipefail

: "${APP_DIR:?APP_DIR is required}"
: "${DEPLOY_SHA:?DEPLOY_SHA is required}"
: "${COMPOSE_FILE:?COMPOSE_FILE is required}"
: "${PROJECT_NAME:?PROJECT_NAME is required}"
: "${HEALTH_URL:?HEALTH_URL is required}"

case "$APP_DIR" in
  /opt/alva/apps/staging/shortq|/opt/alva/apps/prod/shortq) ;;
  *) printf 'refusing unsafe APP_DIR: %s\n' "$APP_DIR" >&2; exit 64 ;;
esac
[[ "$DEPLOY_SHA" =~ ^[0-9a-f]{40}$ ]] || { printf 'DEPLOY_SHA must be a full commit SHA\n' >&2; exit 64; }
[[ "$COMPOSE_FILE" != */* && "$COMPOSE_FILE" == docker-compose*.yml ]] || { printf 'invalid COMPOSE_FILE\n' >&2; exit 64; }
[[ "$PROJECT_NAME" =~ ^[a-z0-9][a-z0-9_-]*$ ]] || { printf 'invalid PROJECT_NAME\n' >&2; exit 64; }
[[ "$HEALTH_URL" =~ ^http://127\.0\.0\.1:[0-9]+/healthz$ ]] || { printf 'invalid HEALTH_URL\n' >&2; exit 64; }

cd -- "$APP_DIR"
[[ "$(pwd -P)" == "$APP_DIR" ]] || { printf 'APP_DIR symlink/path mismatch\n' >&2; exit 64; }
command -v git >/dev/null && command -v docker >/dev/null && command -v curl >/dev/null
docker compose version >/dev/null
SERVER_HOSTNAME="$(hostname)"
export SERVER_HOSTNAME
release_tag="${DEPLOY_TAG:-}"
export DEPLOY_TAG="${release_tag:-unknown}"

git fetch --no-tags origin "$DEPLOY_SHA"
git cat-file -e "$DEPLOY_SHA^{commit}"
previous_sha="$(git rev-parse HEAD 2>/dev/null || true)"
previous_tag="$(cat .active-production-tag 2>/dev/null || true)"
git checkout --detach --force "$DEPLOY_SHA"
test "$(git rev-parse HEAD)" = "$DEPLOY_SHA"

deploy() {
  docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" config --quiet || return 1
  docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" up -d --build --remove-orphans || return 1
  for _ in $(seq 1 30); do curl --fail --silent --show-error "$HEALTH_URL" >/dev/null && return 0; sleep 2; done
  return 1
}

if ! deploy; then
  printf 'deployment failed; rolling back\n' >&2
  if [[ "$previous_sha" =~ ^[0-9a-f]{40}$ ]]; then
    git checkout --detach --force "$previous_sha"
    deploy || { printf 'rollback failed\n' >&2; exit 1; }
    [[ -z "$previous_tag" ]] || printf '%s\n' "$previous_tag" > .active-production-tag
  fi
  exit 1
fi

if [[ -n "$release_tag" ]]; then
  [[ "$release_tag" =~ ^prod-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]+$ ]] || { printf 'invalid DEPLOY_TAG\n' >&2; exit 64; }
  printf '%s\n' "$release_tag" > .active-production-tag
fi
printf 'deployed %s\n' "$DEPLOY_SHA"
