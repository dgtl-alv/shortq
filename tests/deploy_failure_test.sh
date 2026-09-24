#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

remote="$tmp/remote.git"
app="$tmp/app"
fake_bin="$tmp/bin"
mkdir -p "$fake_bin"

git init --bare --quiet "$remote"
git init --quiet "$app"
git -C "$app" config user.name "Deploy Test"
git -C "$app" config user.email "deploy-test@example.invalid"
printf 'old\n' > "$app/version.txt"
git -C "$app" add version.txt
git -C "$app" commit --quiet -m "old"
old_sha="$(git -C "$app" rev-parse HEAD)"
git -C "$app" branch -M main
git -C "$app" remote add origin "$remote"
git -C "$app" push --quiet -u origin main
printf 'new\n' > "$app/version.txt"
git -C "$app" add version.txt
git -C "$app" commit --quiet -m "new"
new_sha="$(git -C "$app" rev-parse HEAD)"
git -C "$app" push --quiet origin main
git -C "$app" checkout --quiet --detach "$old_sha"
printf 'prod-2026-09-17-1\n' > "$app/.active-production-tag"
touch "$app/docker-compose.production.yml"

subject="$tmp/deploy.sh"
python3 - "$repo_root/scripts/deploy.sh" "$subject" "$app" <<'PY'
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
allowed_app = sys.argv[3]
needle = "  /opt/alva/apps/staging/shortq|/opt/alva/apps/prod/shortq) ;;"
replacement = f"  {allowed_app}) ;;"
if source.count(needle) != 1:
    raise SystemExit("deploy APP_DIR allowlist shape changed")
pathlib.Path(sys.argv[2]).write_text(source.replace(needle, replacement), encoding="utf-8")
PY

cat > "$fake_bin/docker" <<'EOF'
#!/usr/bin/env bash
set -u
case "$*" in
  "compose version") exit 0 ;;
  *" config --quiet") exit 42 ;;
  *" up -d --build --remove-orphans")
    : > "${DEPLOY_TEST_UP_MARKER:?}"
    exit 0
    ;;
  *)
    printf 'unexpected docker args: %s\n' "$*" >&2
    exit 99
    ;;
esac
EOF
chmod +x "$fake_bin/docker"

cat > "$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$fake_bin/curl"

set +e
output="$({
  APP_DIR="$app" \
  DEPLOY_SHA="$new_sha" \
  DEPLOY_TAG="prod-2026-09-24-1" \
  COMPOSE_FILE="docker-compose.production.yml" \
  PROJECT_NAME="shortq" \
  HEALTH_URL="http://127.0.0.1:8000/healthz" \
  DEPLOY_TEST_UP_MARKER="$tmp/up-called" \
  PATH="$fake_bin:$PATH" \
  bash "$subject"
} 2>&1)"
status=$?
set -e

if [[ "$status" -eq 0 ]]; then
  printf 'FAIL: deploy succeeded after compose config failed\n%s\n' "$output" >&2
  exit 1
fi

if [[ "$output" != *"deployment failed; rolling back"* ]]; then
  printf 'FAIL: rollback message does not describe a general deployment failure\n%s\n' "$output" >&2
  exit 1
fi

if [[ -e "$tmp/up-called" ]]; then
  printf 'FAIL: compose up ran after compose config failed\n%s\n' "$output" >&2
  exit 1
fi

actual_sha="$(git -C "$app" rev-parse HEAD)"
if [[ "$actual_sha" != "$old_sha" ]]; then
  printf 'FAIL: checkout was not rolled back; got %s want %s\n' "$actual_sha" "$old_sha" >&2
  exit 1
fi

actual_tag="$(cat "$app/.active-production-tag")"
if [[ "$actual_tag" != "prod-2026-09-17-1" ]]; then
  printf 'FAIL: active production tag changed to %s\n' "$actual_tag" >&2
  exit 1
fi

printf 'PASS: compose config failure stops deployment and preserves rollback state\n'
