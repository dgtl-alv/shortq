# Analytics Persistence

ShortQ records redirect analytics in PostgreSQL. Redirect responses remain synchronous: successful redirects update analytics before responding, and links with `max_clicks` continue to enforce their limit atomically in the same transaction.

## Event identity

New redirect attempts receive one UUID v4 `event_id` and one UTC `occurred_at` timestamp when the handler creates the analytics event. `clicks.event_id` is nullable for historical compatibility. A partial unique index applies only to non-null IDs, so replaying a new event is idempotent while legacy callers with no event ID still create one row per call.

## Persistence rules

- Raw inserts use `ON CONFLICT (event_id) WHERE event_id IS NOT NULL DO NOTHING RETURNING id`.
- Link counters and daily rollups change only for rows returned by that insert.
- Rollup day comes from `occurred_at` normalized to UTC, not database processing time.
- Expired attempts create raw rows and rollups without incrementing `links.clicks`.
- Synchronous `RecordClick` checks `max_clicks` in its transaction and rolls back the new raw event when the limit rejects it.
- `RecordClicks` commits or rolls back its whole batch. It accepts counter-incrementing events only for links without `max_clicks`; limited links stay on `RecordClick`.

No queue, cache, worker, or feature flag is part of this foundation.
