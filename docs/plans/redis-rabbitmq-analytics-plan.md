# ShortQ Redirect Cache and Durable Analytics Plan

Status: PR 1 analytics idempotency foundation implemented; later phases not started
Baseline: `main` at `c73f1ec`
Owner: ALVA Digital
Last updated: 2026-09-19

## Purpose

Reduce redirect latency by moving eligible redirect lookups to Redis and eligible click analytics to RabbitMQ without changing redirect correctness, `max_clicks` enforcement, privacy rules, or production release controls.

This file is the durable handoff. A new model/session must read this file, inspect current Git state, and complete only the next unchecked PR phase. Never implement all phases in one PR.

## Confirmed Current Flow

1. `/{slug}` and `/r/{slug}` reach `Handler.redirectSlug` in `internal/handlers/handlers.go`.
2. PostgreSQL resolves the link and separately hydrates geo targets through `internal/store/store.go`.
3. Handler applies tenant-domain, password, expiry, `max_clicks`, geo/device, query-forwarding, and UTM rules.
4. `Store.RecordClick` synchronously updates `links.clicks`, inserts `clicks`, and upserts `click_rollups_daily` before redirect response.
5. `max_clicks` is enforced atomically in PostgreSQL. This path must remain synchronous whenever `max_clicks IS NOT NULL`.
6. Raw click details have 90-day retention; aggregate data remains available.

Primary graph nodes: `.redirectSlug()`, `.passwordGate()`, `.expiredResponse()`, `.redirectEvent()`, `.recordRedirectEvent()`, `.RecordClick()`, `ClickEvent`, `Store`, and `TestPostgresMigrationAndCoreDataPath()`.

## Non-Negotiable Decisions

- PostgreSQL remains source of truth.
- Feature flags default to disabled.
- `max_clicks IS NOT NULL` always uses synchronous atomic PostgreSQL enforcement.
- Password-protected links bypass Redis in first release; password hashes never enter Redis.
- Redis failure falls back to PostgreSQL and never invalidates a valid redirect.
- RabbitMQ publish ambiguity must not double-count. Queue and synchronous fallback share one `event_id`; both persistence paths are idempotent.
- Batch counters and rollups increment only for rows actually inserted by `ON CONFLICT DO NOTHING RETURNING`.
- Daily rollups use event `occurred_at`, not worker processing date.
- RabbitMQ messages are persistent, exchange/queues durable, publisher confirms required, worker ACK only after PostgreSQL commit.
- NACK/requeue alone is not a retry policy. Retry count and DLQ transition must be explicit and bounded.
- Redis and RabbitMQ ports are never publicly exposed.
- No production enablement in implementation PRs. Production remains separately approved and tag-gated.

## Known Guide Corrections

- Slugs are currently globally unique and store lookup is not hostname-scoped. Hostname remains in cache keys for domain correctness, but invalidation must fan out across base/custom host aliases.
- Current model/query does not expose link `updated_at`; add it only if cache serialization/version checks use it.
- Expired-attempt analytics must remain represented.
- `/app/shortq analytics-worker` is currently invalid because `main` ignores command arguments. Add explicit command dispatch before worker Compose wiring.
- Embedded startup schema in `internal/db/db.go` is current migration mechanism; schema changes must be backward-compatible and retry-safe within that mechanism.
- RabbitMQ volume durability covers container restart, not host/disk loss.
- Queue payload includes personal data. Define retention, access, log-redaction, DLQ retention, and erasure handling before staging enablement.

## Delivery Sequence

### PR 1 - Analytics Idempotency Foundation

Goal: make current synchronous analytics safe for future ambiguous queue delivery without adding Redis, RabbitMQ, worker, or Compose services.

Tasks:

- [x] Add nullable `clicks.event_id UUID` in embedded schema migration.
- [x] Add partial unique index where `event_id IS NOT NULL`.
- [x] Add `EventID` and `OccurredAt` to click/event models.
- [x] Generate one event ID per redirect analytics event.
- [x] Make synchronous `RecordClick` idempotent for non-null event IDs.
- [x] Add transactional batch persistence API.
- [x] Use `INSERT ... ON CONFLICT (event_id) DO NOTHING RETURNING`.
- [x] Increment link counters and daily rollups only for returned inserted events.
- [x] Derive rollup day from `OccurredAt`.
- [x] Preserve legacy behavior for historical/null event IDs.
- [x] Preserve synchronous atomic `max_clicks` enforcement.
- [x] Add unit and PostgreSQL integration tests for duplicate ID, mixed duplicate/new batch, counters, rollups, rollback, `max_clicks`, and expired events.
- [x] Update schema/docs without changing external API behavior.

Likely files:

- `internal/db/db.go`
- `internal/models/models.go`
- `internal/store/store.go`
- `internal/store/store_test.go`
- `internal/db/postgres_integration_test.go`
- `internal/handlers/handlers.go`

Acceptance gate:

- Existing redirect behavior unchanged.
- Same `event_id` submitted twice creates one raw click and one set of increments.
- Transaction failure produces no partial counters or rollups.
- `go test ./...`, `go vet ./...`, formatting, Compose validation, and PostgreSQL integration tests pass in CI.

### PR 2 - Redis Cache-Aside Redirect Resolution

Goal: reduce PostgreSQL reads while preserving redirect correctness.

Tasks:

- [ ] Add cache interface independent from Redis client.
- [ ] Add versioned cached redirect DTO; exclude password hashes and analytics data.
- [ ] Add Redis implementation with default TTL exactly 24 hours.
- [ ] Add operation timeout and bounded reconnect behavior.
- [ ] Use key `shortq:redirect:v1:{hostname}:{slug}`.
- [ ] Bypass cache for password-protected links in first release.
- [ ] Cache misses load PostgreSQL then populate Redis.
- [ ] Read/write failures fall back safely.
- [ ] Invalidate only after successful DB commit.
- [ ] Invalidate all affected host aliases on link edit/delete, domain changes, routing/geo/device/expiry/query/UTM changes.
- [ ] Add `REDIRECT_CACHE_ENABLED=false` default flag.
- [ ] Add hit/miss/error/invalidation metrics.
- [ ] Add Redis test service in CI and integration tests.

Acceptance gate:

- First request misses, second hits.
- Edit/delete never serves stale destination after successful invalidation.
- Failed DB transaction does not invalidate.
- Redis outage leaves valid redirects available through PostgreSQL.
- Password-protected and `max_clicks` correctness unchanged.

### PR 3 - RabbitMQ Publisher and Idempotent Fallback

Goal: publish eligible analytics durably while retaining safe synchronous fallback.

Tasks:

- [ ] Add publisher interface and RabbitMQ implementation.
- [ ] Declare durable exchange `shortq.analytics`, queue `shortq.clicks`, DLX, and DLQ idempotently.
- [ ] Publish persistent messages with shared `event_id` and short timeout.
- [ ] Require publisher confirmation.
- [ ] Reuse long-lived connection/channel strategy; never connect per request.
- [ ] On definite publish failure, call idempotent synchronous persistence with same `event_id`.
- [ ] On confirm timeout/ambiguous outcome, call same idempotent fallback; later worker redelivery must no-op.
- [ ] Keep links with `max_clicks` on synchronous atomic path.
- [ ] Keep expired-attempt semantics.
- [ ] Add `CLICK_QUEUE_ENABLED=false` default flag.
- [ ] Add publish duration, failure, confirmation, and fallback metrics.
- [ ] Ensure payload excludes authorization headers, cookies, tokens, API keys, and passwords.

Acceptance gate:

- Standard eligible link publishes once.
- Broker outage uses synchronous fallback.
- Confirm ambiguity cannot double-count.
- `max_clicks` link never relies on async counters.

### PR 4 - Analytics Worker and Batch Persistence

Goal: consume queued events safely and efficiently.

Tasks:

- [ ] Add explicit CLI command dispatch and `analytics-worker` command.
- [ ] Declare topology on startup.
- [ ] Consume with manual ACK and prefetch 400.
- [ ] Batch up to 200 events or 250 ms.
- [ ] Validate schema version and required fields.
- [ ] Persist through PR 1 batch API.
- [ ] ACK only after PostgreSQL commit.
- [ ] On transient DB error, retry with bounded backoff.
- [ ] Track retry count through retry queues/headers; send to DLQ after five failures.
- [ ] Reject unsupported/invalid payload to DLQ without immediate infinite requeue.
- [ ] Add worker metrics: batch size/duration, duplicates, failures, retries, DLQ, lag.

Acceptance gate:

- Worker restart processes pending events.
- Crash after commit and before ACK redelivers without duplicate analytics.
- Invalid/poison messages reach DLQ after bounded handling.
- Counters and rollups equal unique inserted events.

### PR 5 - Infrastructure, Observability, and Staging Validation

Goal: provide disabled-by-default staging infrastructure and prove restart/fallback behavior.

Tasks:

- [ ] Add Redis and RabbitMQ services with reviewed pinned image versions/digests.
- [ ] Add named RabbitMQ volume.
- [ ] Add analytics worker service using real CLI command.
- [ ] Keep Redis/RabbitMQ ports internal; loopback-only temporary management binding if approved.
- [ ] Add health checks and dependency ordering without making app availability depend on Redis/RabbitMQ after startup.
- [ ] Add environment documentation with no committed credentials.
- [ ] Add CI services/integration suite for PostgreSQL, Redis, RabbitMQ.
- [ ] Add queue depth, oldest message, consumer, DLQ, cache, fallback, and latency dashboards/alerts.
- [ ] Define broker/DLQ personal-data retention and access controls.
- [ ] Execute worker-stop, RabbitMQ-restart, Redis-outage, broker-outage, and redelivery tests.
- [ ] Run staged load tests at 50/100/200/400 RPS with documented stop conditions.

Acceptance gate:

- Feature flags remain disabled by default.
- Queue survives RabbitMQ container restart without volume deletion.
- Redis loss has no correctness loss.
- RabbitMQ outage preserves analytics through idempotent fallback.
- Staging p95/error/queue-lag targets pass.
- Production rollout still requires separate approval.

## Resume Protocol

At start of every implementation session:

1. Read this file completely.
2. Run `git status --short --branch` and stop if unexpected changes exist.
3. Fetch remote and confirm baseline/branch.
4. Open `graphify-out/GRAPH_REPORT.md` or run `graphify query` for affected symbols.
5. Work only on first PR phase containing unchecked items.
6. Write failing tests before implementation.
7. Update checkboxes and a short `Progress Log` entry before ending session.
8. Never mark a phase complete without test evidence.
9. Never deploy, enable production flags, delete broker volumes, or merge without explicit approval.

## Verification Commands

Run in CI or a host with Go installed:

```bash
gofmt -w <changed-go-files>
go test ./...
go vet ./...
docker compose config
```

For integration phases, also run dedicated PostgreSQL/Redis/RabbitMQ suites and record exact container/image versions.

## Progress Log

- 2026-09-19: Read-only audit completed on `main` at `c73f1ec`.
- 2026-09-19: Knowledge graph generated: 462 nodes, 1,353 built edges, 17 communities.
- 2026-09-19: Graph health warning recorded: 166 dangling external/reference endpoints and 22 endpoint-collapsed edges; useful for navigation, not a complete proof of all relationships.
- 2026-09-19: Plan approved in principle; implementation not started.

- 2026-09-19: PR 1 implemented on `feature/analytics-idempotency`: nullable UUID event identity, partial unique index, synchronous and batch idempotency, UTC occurred-at rollups, transactional counters, rollback protection, max-click preservation, and expired/null compatibility. Validation passed with Go 1.25.13 and PostgreSQL 17-alpine: `gofmt`, `go test -count=1 ./...`, `go vet ./...`, Docker Compose v5.5.1 `config --quiet`, PostgreSQL integration tests, and `git diff --check`.
