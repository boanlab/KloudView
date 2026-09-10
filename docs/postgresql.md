# PostgreSQL storage

KloudView Server uses the PostgreSQL adapter when `KLOUDVIEW_DATABASE_URL` is set. Without it, the server falls back to a JSON snapshot store, which suits a single node.

## Storage structure

| Table | Purpose |
|---|---|
| `kloudview_schema_migrations` | Sequential schema versions |
| `kloudview_documents` | JSONB snapshot of resource, group, incident, and job state along with RBAC state |
| `metric_samples` | Per-resource CPU, Memory, Disk, and Network time-series |
| `metric_rollup` | One-minute mean and peak of the same series, for long windows |
| `resources` | One row per resource, written by what changed |
| `agent_inventories` | Each agent's latest host report |

Resources and agent inventories live in their own tables rather than the state
document, because the document is rewritten whole on every save and a fleet reporting
every process would make that cost follow the size of everything stored rather than
what changed. A document that still carries them inline has them scheduled for
writing on load, so the first save does not drop what the tables have not seen.

Operation results are capped at 4 KB and finished operations at the most recent 500.
An inventory refresh answers with the whole host report, already stored as the
inventory, so the copy is bounded. The bounds apply on load as well as on write, so
a document carrying oversized results is compacted rather than left as written.

General state, RBAC state, and metric batches not yet persisted are stored in a single transaction. Metrics use `(resource_id, sampled_at)` as the idempotency key and are upserted after `COPY` staging. Only samples newly collected in server memory become part of the next batch.

The server keeps the most recent 24 hours in memory for live views; anything older is
read from these tables. On restart recovery, the most recent hour is read at original
resolution and earlier ranges in 5-minute buckets to limit memory usage. A BRIN index
is created for time-range deletion.

## Configuration

```text
KLOUDVIEW_DATABASE_URL=postgres://user:password@database:5432/kloudview?sslmode=require
```

Compose runs PostgreSQL 17 by default, and the server starts after the database health check. The `KLOUDVIEW_POSTGRES_PASSWORD` in `.env` must be changed before any external exposure.

Schema migrations are applied sequentially at server startup after acquiring a PostgreSQL advisory transaction lock. Even if multiple servers start concurrently, only one instance performs the migration.

## Backup and recovery

Use the standard PostgreSQL `pg_dump` for a consistent full backup.

```bash
docker compose exec -T postgres pg_dump -U kloudview -Fc kloudview > kloudview.dump
```

Before recovery, stop server writes and confirm that the target database is empty. In production environments, database secrets must be injected from a secret manager rather than stored directly in the Compose file.

## Integration tests

Set the following environment variable only against a dedicated test database. The tests clear the target tables, so a production database must never be specified.

```bash
(cd apps/server && KLOUDVIEW_TEST_DATABASE_URL='postgres://kloudview:password@127.0.0.1:5432/kloudview_test?sslmode=disable' \
  go test ./internal/store -run TestPostgresRoundTrip)
```

## Metric retention

Samples land in `metric_samples` at the agent's reporting interval and are kept at
full resolution for `KLOUDVIEW_METRIC_RAW_DAYS` (30). Before they age out they are
summarised into `metric_rollup` at `KLOUDVIEW_METRIC_ROLLUP_SECONDS` (60) buckets and
kept for `KLOUDVIEW_METRIC_ROLLUP_DAYS` (400).

A rollup stores the mean **and the peak** of CPU, memory, and disk. Reads outside the
raw window return the peak, so a spike that lasted seconds is still visible a year
later instead of being averaged into the background.

Measured on four nodes reporting every ten seconds: 9 MB of raw samples per day, so
30 days is about 270 MB and a year of one-minute rollups about 440 MB. At fifty nodes
that is roughly 9 GB; raw for 90 days would be 16 GB. Collecting every second instead
would cost about 26 GB a year for four nodes alone.
