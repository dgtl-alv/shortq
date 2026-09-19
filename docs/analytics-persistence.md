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
