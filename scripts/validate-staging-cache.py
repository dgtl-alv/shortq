#!/usr/bin/env python3
"""Validate Redis-only staging rollout invariants."""

import json
import os
import subprocess


def fail(message: str) -> None:
    raise AssertionError(message)


env = os.environ.copy()
env.update(
    {
        "DATABASE_URL": "postgres://shortq:ci-only-password@db:5432/shortq?sslmode=disable",
        "SHORTQ_POSTGRES_PASSWORD": "ci-only-password",
    }
)
result = subprocess.run(
    ["docker", "compose", "-f", "docker-compose.staging.yml", "config", "--format", "json"],
    check=True,
    capture_output=True,
    text=True,
    env=env,
)
config = json.loads(result.stdout)
services = config.get("services", {})
if "redis" not in services:
    fail("redis staging service is missing")
redis = services["redis"]
if redis.get("ports"):
    fail("redis must not publish host ports")
if "@sha256:" not in redis.get("image", ""):
    fail("redis image must be pinned by digest")
if not redis.get("healthcheck"):
    fail("redis healthcheck is required")
command = redis.get("command", [])
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
if command != expected_command:
    fail(f"redis command={command!r}, want {expected_command!r}")
if redis.get("mem_limit") != "201326592":
    fail("redis container memory limit must be 192 MiB")
if any(volume.get("target") == "/data" for volume in redis.get("volumes", [])):
    fail("redirect cache must remain disposable without a data volume")

app = services["app"]
app_env = app.get("environment", {})
expected = {
    "REDIRECT_CACHE_ENABLED": "true",
    "REDIS_URL": "redis://redis:6379/0",
    "REDIRECT_CACHE_TIMEOUT": "250ms",
    "CLICK_QUEUE_ENABLED": "false",
}
if "RABBITMQ_URL" in app_env:
    fail("Redis-only rollout must not inject RABBITMQ_URL")
for key, value in expected.items():
    if app_env.get(key) != value:
        fail(f"app {key}={app_env.get(key)!r}, want {value!r}")
if app_env.get("CLICK_QUEUE_ENABLED", "false").lower() == "true":
    fail("click queue must remain disabled during Redis-only rollout")

print("staging Redis cache topology: pass")
