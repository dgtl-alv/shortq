# Analytics Persistence

ShortQ records redirect analytics in PostgreSQL. With `CLICK_QUEUE_ENABLED=false` (the default), redirect responses update analytics synchronously. When explicitly enabled, eligible events are published to RabbitMQ and links with `max_clicks` continue to enforce their limit synchronously and atomically in PostgreSQL.

## Event identity

New redirect attempts receive one UUID v4 `event_id` and one UTC `occurred_at` timestamp when the handler creates the analytics event. `clicks.event_id` is nullable for historical compatibility. A partial unique index applies only to non-null IDs, so replaying a new event is idempotent while legacy callers with no event ID still create one row per call.

## Persistence rules

- Raw inserts use `ON CONFLICT (event_id) WHERE event_id IS NOT NULL DO NOTHING RETURNING id`.
- Link counters and daily rollups change only for rows returned by that insert.
- Rollup day comes from `occurred_at` normalized to UTC, not database processing time.
- Expired attempts create raw rows and rollups without incrementing `links.clicks`.
- Synchronous `RecordClick` checks `max_clicks` in its transaction and rolls back the new raw event when the limit rejects it.
- `RecordClicks` commits or rolls back its whole batch. It accepts counter-incrementing events only for links without `max_clicks`; limited links stay on `RecordClick`.

## Publisher and fallback

- The publisher declares durable direct exchanges `shortq.analytics` and `shortq.analytics.dlx`, durable queues `shortq.clicks` and `shortq.clicks.dlq`, and their bindings idempotently when its long-lived channel connects.
- Messages are persistent, versioned (`version: 1`), and require a positive publisher confirmation within `CLICK_QUEUE_CONFIRM_TIMEOUT` (default `250ms`).
- One connection/channel is reused and serialized across concurrent HTTP requests. Definite failures reconnect at most once; confirmation timeout or channel closure resets the channel without republishing an ambiguous event.
- Any publish failure, NACK, or ambiguous confirmation uses synchronous `Store.RecordClick` with the exact same `event_id`. A later queue delivery is therefore harmless under the unique event constraint.
- Expired attempts remain analytics events with `increment: false`. Links with `max_clicks` bypass RabbitMQ entirely.
- The explicit payload allowlist contains analytics fields only. URL user info, query strings, and fragments are removed from queued resolved/referrer URLs; request authorization headers, cookies, session tokens, API keys, and passwords are never serialized.
- Runtime metrics expose publish attempts/duration, failures, confirmations, confirmation failures, and fallbacks. Logs identify failures and fallbacks by event ID without logging message bodies.

PR 3 intentionally includes no worker, consumer, staging/production RabbitMQ service, or production enablement. Until the worker phase is deployed separately, keep `CLICK_QUEUE_ENABLED=false` outside publisher integration testing.

## Analytics worker

Run the consumer as a separate process with:

```text
/app/shortq analytics-worker
```

The worker needs `DATABASE_URL` and `RABBITMQ_URL`; it does not initialize HTTP authentication, tenants, or the web server. Infrastructure wiring and production enablement remain outside this phase.

- Startup idempotently declares the existing analytics/DLQ topology plus durable exchange `shortq.analytics.retry` and queue `shortq.clicks.retry`.
- `shortq.clicks` is consumed with manual acknowledgements and prefetch `400`. Valid events are committed with `Store.RecordClicks` in batches of at most `200`, flushed at least every `250ms`.
- Payload decoding is strict and versioned. Version, UUID `event_id`, `occurred_at`, positive `link_id`, slug, method, status, and route type are validated. Unsupported, malformed, or incomplete messages are rejected without requeue and RabbitMQ routes them through the existing DLX to `shortq.clicks.dlq`.
- A successful batch is ACKed only after `RecordClicks` returns from its transaction commit. The returned inserted count records committed versus duplicate events; duplicates are then ACKed safely.
- A failed database batch is durably republished to `shortq.clicks.retry` with header `x-shortq-retry-count`, persistent delivery, publisher confirmation, and per-message backoff of `250ms`, `500ms`, `1s`, then `2s`. The original is ACKed only after that retry publish is confirmed. The fifth database failure rejects the original to the DLQ. If retry publication itself cannot be confirmed, the original goes to the DLQ rather than being requeued forever.
- Retry-queue dead lettering returns the message to `shortq.analytics` using routing key `click.v1`. This republish strategy is required because a plain `Nack(requeue=true)` cannot increment a retry header and therefore cannot implement a bounded retry policy.
- SIGINT/SIGTERM cancels broker intake, drains the bounded prefetched/in-process deliveries for up to 10 seconds, flushes the current batch, and then closes the AMQP channel and connection.
- Worker metrics cover received, committed, duplicate, invalid, requeue, DLQ, persistence failures, batch count/size, total/max batch latency, and total/max event lag. Structured logs include event IDs and counts, never message bodies, URLs, IP addresses, user agents, or other analytics payload values.

The worker consumes the same privacy-allowlisted payload emitted by the publisher. Retry messages copy only that body and standard message metadata plus the ShortQ retry-count header; arbitrary inbound headers are not propagated.
