#!/usr/bin/env python3
"""Validate production Redis redirect-cache rollout invariants."""

import json
import os
import subprocess


def fail(message: str) -> None:
    raise AssertionError(message)


env = os.environ.copy()
env.update(
    {
        "DATABASE_URL": "postgres://shortq:ci-only@db.example/shortq_prod?sslmode=require",
    }
)
result = subprocess.run(
    ["docker", "compose", "-f", "docker-compose.production.yml", "config", "--format", "json"],
    check=True,
    capture_output=True,
    text=True,
    env=env,
)
config = json.loads(result.stdout)
services = config.get("services", {})
if set(services) != {"app", "redis"}:
    fail(f"production services={sorted(services)}, want app and redis only")

redis = services["redis"]
if redis.get("ports"):
    fail("production Redis must not publish host ports")
if redis.get("image") != "redis:7.4-alpine@sha256:858f009f9709ce576febc734aa78b8f6d624b82571f9ddb6bda4377c833b3499":
    fail("production Redis image must use reviewed pinned digest")
if not redis.get("healthcheck"):
    fail("production Redis healthcheck is required")
expected_command = [
    "redis-server",
    "--save",
    "",
    "--appendonly",
    "no",
    "--maxmemory",
    "128mb",
    "--maxmemory-policy",
    "allkeys-lru",
]
if redis.get("command") != expected_command:
    fail(f"redis command={redis.get('command')!r}, want {expected_command!r}")
if redis.get("mem_limit") != "201326592":
    fail("production Redis container memory limit must be 192 MiB")
if any(volume.get("target") == "/data" for volume in redis.get("volumes", [])):
    fail("redirect cache must remain disposable without a data volume")

app_env = services["app"].get("environment", {})
expected_env = {
    "REDIRECT_CACHE_ENABLED": "true",
    "REDIS_URL": "redis://redis:6379/0",
    "REDIRECT_CACHE_TIMEOUT": "250ms",
    "CLICK_QUEUE_ENABLED": "false",
}
for key, value in expected_env.items():
    if app_env.get(key) != value:
        fail(f"app {key}={app_env.get(key)!r}, want {value!r}")
if "RABBITMQ_URL" in app_env:
    fail("cache-only production rollout must not inject RABBITMQ_URL")
if services["app"].get("depends_on", {}).get("redis"):
    fail("app must not hard-depend on Redis; PostgreSQL fallback must start when Redis is unavailable")

print("production Redis cache topology: pass")
