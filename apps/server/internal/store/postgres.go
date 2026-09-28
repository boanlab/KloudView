package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

const migrationTable = `
CREATE TABLE IF NOT EXISTS kloudview_schema_migrations (
    version integer PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);`

// postgresEnsure runs on every start, outside the version ledger. A migration
// is identified by its position in the list, so a database that recorded a
// version under one ordering skips whatever that number means under the next,
// and a column it never received is never added. Statements here are
// idempotent and cheap, and they repair exactly that drift.
const postgresEnsure = `
ALTER TABLE metric_samples ADD COLUMN IF NOT EXISTS values jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE metric_rollup ADD COLUMN IF NOT EXISTS values_max jsonb NOT NULL DEFAULT '{}'::jsonb;
`

var postgresMigrations = []string{`
CREATE TABLE IF NOT EXISTS kloudview_documents (
    kind text PRIMARY KEY,
    payload jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS metric_samples (
    resource_id text NOT NULL,
    sampled_at timestamptz NOT NULL,
    cpu double precision NOT NULL,
    memory double precision NOT NULL,
    disk double precision NOT NULL,
    network_rx numeric(20,0) NOT NULL,
    network_tx numeric(20,0) NOT NULL,
    -- Every reading that is not one of the five above. A host has as many
    -- filesystems as it has, so there is no column count that would cover
    -- them; a document keeps the schema from growing a column per signal.
    values jsonb NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (resource_id, sampled_at)
);
CREATE INDEX IF NOT EXISTS metric_samples_sampled_at_brin ON metric_samples USING brin (sampled_at);
`, `
CREATE TABLE IF NOT EXISTS metric_rollup (
    resource_id text NOT NULL,
    bucket_start timestamptz NOT NULL,
    bucket_seconds integer NOT NULL,
    cpu double precision NOT NULL,
    memory double precision NOT NULL,
    disk double precision NOT NULL,
    cpu_max double precision NOT NULL,
    memory_max double precision NOT NULL,
    disk_max double precision NOT NULL,
    network_rx numeric(20,0) NOT NULL,
    network_tx numeric(20,0) NOT NULL,
    -- The peak each named reading reached in the bucket. A rollup exists to
    -- answer "how bad did it get", and an average of an OOM count answers
    -- nothing.
    values_max jsonb NOT NULL DEFAULT '{}'::jsonb,
    samples integer NOT NULL,
    PRIMARY KEY (resource_id, bucket_seconds, bucket_start)
);
CREATE INDEX IF NOT EXISTS metric_rollup_bucket_brin ON metric_rollup USING brin (bucket_start);
`, `
CREATE TABLE IF NOT EXISTS resources (
    id text PRIMARY KEY,
    payload jsonb NOT NULL,
    terminated_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS resources_terminated_at ON resources (terminated_at);
`, `
-- Inventories hold each agent's whole host report, which grows with the number
-- of processes a node runs; the state document is rewritten on every save.
CREATE TABLE IF NOT EXISTS agent_inventories (
    agent_id text PRIMARY KEY,
    payload jsonb NOT NULL,
    observed_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
`, `
ALTER TABLE metric_samples ADD COLUMN IF NOT EXISTS values jsonb NOT NULL DEFAULT '{}'::jsonb;
`, `
ALTER TABLE metric_rollup ADD COLUMN IF NOT EXISTS values_max jsonb NOT NULL DEFAULT '{}'::jsonb;
`}

type Postgres struct {
	pool *pgxpool.Pool
	// Retention windows. Raw samples answer "what happened in the last few
	// days" and rollups answer "what has this looked like for months".
	rawRetentionDays    int
	rollupRetentionDays int
	rollupSeconds       int
	// Digests of the documents as last committed. The state document is
	// rewritten whole or not at all, and at a fleet's size that is megabytes of
	// write-ahead log every few seconds for a document that usually did not
	// change.
	stateDigest  [32]byte
	accessDigest [32]byte
	// When the whole retained window was last summarised.
	lastRollupSweep time.Time
}

// rollupSweepInterval is how often the incremental rollup gives way to one over
// the whole retained window.
const rollupSweepInterval = time.Hour

// bucketStart floors an instant to the start of the rollup bucket holding it,
// counting from the epoch so it matches date_bin's origin.
func bucketStart(at time.Time, seconds int) time.Time {
	if seconds <= 0 {
		return at
	}
	step := int64(seconds)
	return time.Unix((at.Unix()/step)*step, 0).UTC()
}

// WithRetention sets the raw and rollup windows in days and the rollup bucket
// size in seconds.
func (p *Postgres) WithRetention(rawDays, rollupDays, bucketSeconds int) *Postgres {
	if rawDays > 0 {
		p.rawRetentionDays = rawDays
	}
	if rollupDays > 0 {
		p.rollupRetentionDays = rollupDays
	}
	if bucketSeconds > 0 {
		p.rollupSeconds = bucketSeconds
	}
	return p
}

// MetricRange returns samples for one resource between from and to, reading raw
// samples when the window is inside the raw retention and rollups otherwise.
func (p *Postgres) MetricRange(ctx context.Context, resourceID string, from, to time.Time) ([]domain.MetricSample, error) {
	rawStart := time.Now().UTC().AddDate(0, 0, -p.rawRetentionDays)
	query := `SELECT sampled_at, cpu, memory, disk, network_rx, network_tx, values FROM metric_samples
              WHERE resource_id = $1 AND sampled_at BETWEEN $2 AND $3 ORDER BY sampled_at`
	if from.Before(rawStart) {
		query = `SELECT bucket_start, cpu_max, memory_max, disk_max, network_rx, network_tx, values_max FROM metric_rollup
                 WHERE resource_id = $1 AND bucket_start BETWEEN $2 AND $3 ORDER BY bucket_start`
	}
	rows, err := p.pool.Query(ctx, query, resourceID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.MetricSample{}
	for rows.Next() {
		sample := domain.MetricSample{ResourceID: resourceID}
		if err := rows.Scan(&sample.Timestamp, &sample.CPU, &sample.Memory, &sample.Disk, &sample.NetworkRx, &sample.NetworkTx, &sample.Values); err != nil {
			return nil, err
		}
		if len(sample.Values) == 0 {
			sample.Values = nil
		}
		items = append(items, sample)
	}
	return items, rows.Err()
}

func OpenPostgres(ctx context.Context, databaseURL string) (*Postgres, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 10
	config.MinConns = 1
	config.MaxConnLifetime = time.Hour
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := migratePostgres(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	// Retention defaults: raw covers incident investigation at
	// full fidelity, rollups carry the year at a minute with peaks preserved.
	return &Postgres{pool: pool, rawRetentionDays: 30, rollupRetentionDays: 400, rollupSeconds: 60}, nil
}

func migratePostgres(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(1263292749)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, migrationTable); err != nil {
		return err
	}
	var current int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) FROM kloudview_schema_migrations`).Scan(&current); err != nil {
		return err
	}
	for index, migration := range postgresMigrations {
		version := index + 1
		if version <= current {
			continue
		}
		if _, err := tx.Exec(ctx, migration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kloudview_schema_migrations (version) VALUES ($1)`, version); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, postgresEnsure); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) Close() {
	p.pool.Close()
}

func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// loadResources reads the resource table. A document written before resources
// moved out still carries them, so those are kept and written to the table on
// the next save rather than lost.
func (p *Postgres) loadResources(ctx context.Context, memory *Memory) error {
	rows, err := p.pool.Query(ctx, `SELECT payload FROM resources`)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []domain.Resource{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var resource domain.Resource
		if err := json.Unmarshal(payload, &resource); err != nil {
			return err
		}
		items = append(items, resource)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(items) == 0 && len(memory.ListResources()) > 0 {
		// Resources carried inline in the state document rather than the table.
		// Marked for writing, or the first save drops what the table has not
		// seen.
		memory.markAllResourcesDirty()
		return nil
	}
	memory.ReplaceResources(items)
	return nil
}

// loadInventories reads the agent reports, carrying across any the state
// document still holds inline.
func (p *Postgres) loadInventories(ctx context.Context, memory *Memory) error {
	rows, err := p.pool.Query(ctx, `SELECT payload FROM agent_inventories`)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []domain.AgentInventory{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var inventory domain.AgentInventory
		if err := json.Unmarshal(payload, &inventory); err != nil {
			return err
		}
		items = append(items, inventory)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(items) == 0 && len(memory.Inventories()) > 0 {
		memory.markAllInventoriesDirty()
		return nil
	}
	memory.ReplaceInventories(items)
	return nil
}

func (p *Postgres) Load(ctx context.Context) (*Memory, []byte, error) {
	var stateData []byte
	err := p.pool.QueryRow(ctx, `SELECT payload FROM kloudview_documents WHERE kind = 'state'`).Scan(&stateData)
	if errors.Is(err, pgx.ErrNoRows) {
		return NewMemory(), nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	memory, err := RestoreState(stateData)
	if err != nil {
		return nil, nil, err
	}
	if err := p.loadResources(ctx, memory); err != nil {
		return nil, nil, err
	}
	if err := p.loadInventories(ctx, memory); err != nil {
		return nil, nil, err
	}
	rows, err := p.pool.Query(ctx, `
WITH binned AS (
    SELECT resource_id,
           date_bin(interval '5 minutes', sampled_at, timestamptz '1970-01-01') AS sampled_at,
           avg(cpu) AS cpu, avg(memory) AS memory, avg(disk) AS disk,
           max(network_rx) AS network_rx, max(network_tx) AS network_tx
    FROM metric_samples
    WHERE sampled_at >= now() - interval '24 hours'
      AND sampled_at < now() - interval '1 hour'
    GROUP BY resource_id, 2
), binned_peaks AS (
    -- The peak each named reading reached in the bucket, the same summary the
    -- rollup keeps: an average of an OOM count answers nothing.
    SELECT resource_id, sampled_at, jsonb_object_agg(key, peak) AS values
    FROM (
        SELECT s.resource_id,
               date_bin(interval '5 minutes', s.sampled_at, timestamptz '1970-01-01') AS sampled_at,
               e.key AS key,
               max((e.value)::numeric) AS peak
        FROM metric_samples s, LATERAL jsonb_each(s.values) AS e
        WHERE s.sampled_at >= now() - interval '24 hours'
          AND s.sampled_at < now() - interval '1 hour'
        GROUP BY 1, 2, 3
    ) per_key
    GROUP BY resource_id, sampled_at
), retained AS (
    SELECT resource_id, sampled_at, cpu, memory, disk, network_rx, network_tx, values
    FROM metric_samples
    WHERE sampled_at >= now() - interval '1 hour'
    UNION ALL
    SELECT b.resource_id, b.sampled_at, b.cpu, b.memory, b.disk, b.network_rx, b.network_tx,
           coalesce(p.values, '{}'::jsonb)
    FROM binned b
    LEFT JOIN binned_peaks p ON p.resource_id = b.resource_id AND p.sampled_at = b.sampled_at
)
SELECT resource_id, sampled_at, cpu, memory, disk, network_rx, network_tx, values
FROM retained
ORDER BY resource_id, sampled_at`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	metrics := map[string][]domain.MetricSample{}
	for rows.Next() {
		var sample domain.MetricSample
		if err := rows.Scan(&sample.ResourceID, &sample.Timestamp, &sample.CPU, &sample.Memory, &sample.Disk, &sample.NetworkRx, &sample.NetworkTx, &sample.Values); err != nil {
			return nil, nil, err
		}
		if len(sample.Values) == 0 {
			sample.Values = nil
		}
		metrics[sample.ResourceID] = append(metrics[sample.ResourceID], sample)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	memory.mu.Lock()
	memory.metrics = metrics
	memory.mu.Unlock()
	var accessData []byte
	err = p.pool.QueryRow(ctx, `SELECT payload FROM kloudview_documents WHERE kind = 'access'`).Scan(&accessData)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, err
	}
	return memory, accessData, nil
}

func (p *Postgres) Save(ctx context.Context, memory *Memory, accessData []byte) error {
	stateData, err := memory.MarshalStateWithout(false, false, false)
	if err != nil {
		return err
	}
	metrics, metricCount := memory.pendingMetricBatch()
	changedResources, removedResources := memory.PendingResourceChanges()
	changedInventories := memory.PendingInventories()
	now := time.Now().UTC()
	stateDigest := sha256.Sum256(stateData)
	accessDigest := sha256.Sum256(accessData)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if stateDigest != p.stateDigest {
		if _, err := tx.Exec(ctx, `INSERT INTO kloudview_documents (kind, payload, updated_at) VALUES ('state', $1, now()) ON CONFLICT (kind) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`, stateData); err != nil {
			return err
		}
	}
	if len(accessData) > 0 && accessDigest != p.accessDigest {
		if _, err := tx.Exec(ctx, `INSERT INTO kloudview_documents (kind, payload, updated_at) VALUES ('access', $1, now()) ON CONFLICT (kind) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`, accessData); err != nil {
			return err
		}
	}
	// Resources are written by what changed, not by rewriting the whole set:
	// a fleet reporting every process makes that difference the whole cost.
	for _, resource := range changedResources {
		payload, err := json.Marshal(resource)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO resources (id, payload, terminated_at, updated_at) VALUES ($1, $2, $3, now()) ON CONFLICT (id) DO UPDATE SET payload = excluded.payload, terminated_at = excluded.terminated_at, updated_at = excluded.updated_at`,
			resource.ID, payload, resource.TerminatedAt); err != nil {
			return err
		}
	}
	if len(removedResources) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM resources WHERE id = ANY($1)`, removedResources); err != nil {
			return err
		}
	}
	for _, inventory := range changedInventories {
		payload, err := json.Marshal(inventory)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_inventories (agent_id, payload, observed_at, updated_at) VALUES ($1, $2, $3, now()) ON CONFLICT (agent_id) DO UPDATE SET payload = excluded.payload, observed_at = excluded.observed_at, updated_at = excluded.updated_at`,
			inventory.AgentID, payload, inventory.ObservedAt); err != nil {
			return err
		}
	}
	if len(metrics) > 0 {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE metric_samples_stage (LIKE metric_samples INCLUDING DEFAULTS) ON COMMIT DROP`); err != nil {
			return err
		}
		values := make([][]any, 0, len(metrics))
		for _, sample := range metrics {
			named := sample.Values
			if named == nil {
				named = map[string]float64{}
			}
			values = append(values, []any{sample.ResourceID, sample.Timestamp, sample.CPU, sample.Memory, sample.Disk, sample.NetworkRx, sample.NetworkTx, named})
		}
		columns := []string{"resource_id", "sampled_at", "cpu", "memory", "disk", "network_rx", "network_tx", "values"}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"metric_samples_stage"}, columns, pgx.CopyFromRows(values)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO metric_samples SELECT * FROM metric_samples_stage ON CONFLICT (resource_id, sampled_at) DO UPDATE SET cpu = excluded.cpu, memory = excluded.memory, disk = excluded.disk, network_rx = excluded.network_rx, network_tx = excluded.network_tx, values = excluded.values`); err != nil {
			return err
		}
	}
	// Summarise the buckets the new samples landed in, then prune both tiers.
	// Rollups are what makes a year of history affordable: a five-minute bucket
	// is roughly a fortieth of the rows it replaces. Only those buckets: every
	// sample older than them was summarised on the save that carried it, and
	// re-reading the whole retained window costs the same whether one sample
	// arrived or a million.
	// A sweep covers the whole retained window, which is what repairs buckets
	// no incremental pass would revisit: rows left by an earlier bucket size,
	// gaps from a period when saving was failing, anything written around the
	// batch. It is the expensive form, so it runs on a timer rather than every
	// save.
	sweep := now.Sub(p.lastRollupSweep) >= rollupSweepInterval
	if len(metrics) > 0 || sweep {
		rollupFrom := now.Add(-time.Duration(p.rawRetentionDays) * 24 * time.Hour)
		if !sweep {
			rollupFrom = metrics[0].Timestamp
			for _, sample := range metrics[1:] {
				if sample.Timestamp.Before(rollupFrom) {
					rollupFrom = sample.Timestamp
				}
			}
		}
		// Down to the start of the bucket that holds it. date_bin counts from
		// the epoch, so the bound has to land on the same boundaries: an
		// instant part-way into a bucket selects part of it, and the upsert
		// would replace a complete row with an average and a peak taken from
		// the remainder.
		rollupFrom = bucketStart(rollupFrom, p.rollupSeconds)
		if _, err := tx.Exec(ctx, `
WITH buckets AS (
  SELECT resource_id,
         date_bin(make_interval(secs => $1), sampled_at, timestamptz '1970-01-01') AS bucket,
         avg(cpu) AS cpu, avg(memory) AS memory, avg(disk) AS disk,
         max(cpu) AS cpu_max, max(memory) AS memory_max, max(disk) AS disk_max,
         max(network_rx) AS network_rx, max(network_tx) AS network_tx,
         count(*) AS samples
  FROM metric_samples
  WHERE sampled_at >= now() - make_interval(days => $2) AND sampled_at >= $3
  GROUP BY resource_id, 2
), peaks AS (
  -- The peak each named reading reached in the bucket. Expanded to rows and
  -- folded back so a key present in only some samples still keeps its highest
  -- value rather than being lost to the others. Grouped alongside the averages
  -- rather than correlated into them: a subquery per bucket rescans the whole
  -- table, and the bucket is an expression the outer grouping does not expose.
  SELECT resource_id, bucket, jsonb_object_agg(key, peak) AS values_max
  FROM (
    SELECT s.resource_id,
           date_bin(make_interval(secs => $1), s.sampled_at, timestamptz '1970-01-01') AS bucket,
           e.key AS key,
           max((e.value)::numeric) AS peak
    FROM metric_samples s, LATERAL jsonb_each(s.values) AS e
    WHERE s.sampled_at >= now() - make_interval(days => $2) AND s.sampled_at >= $3
    GROUP BY 1, 2, 3
  ) per_key
  GROUP BY resource_id, bucket
)
INSERT INTO metric_rollup (resource_id, bucket_start, bucket_seconds, cpu, memory, disk,
                           cpu_max, memory_max, disk_max, network_rx, network_tx, values_max, samples)
SELECT b.resource_id, b.bucket, $1,
       b.cpu, b.memory, b.disk,
       b.cpu_max, b.memory_max, b.disk_max,
       b.network_rx, b.network_tx,
       coalesce(p.values_max, '{}'::jsonb),
       b.samples
FROM buckets b
LEFT JOIN peaks p ON p.resource_id = b.resource_id AND p.bucket = b.bucket
ON CONFLICT (resource_id, bucket_seconds, bucket_start) DO UPDATE
SET cpu = excluded.cpu, memory = excluded.memory, disk = excluded.disk,
    cpu_max = excluded.cpu_max, memory_max = excluded.memory_max, disk_max = excluded.disk_max,
    network_rx = excluded.network_rx, network_tx = excluded.network_tx,
    values_max = excluded.values_max, samples = excluded.samples`,
			p.rollupSeconds, p.rawRetentionDays, rollupFrom); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM metric_samples WHERE sampled_at < now() - make_interval(days => $1)`, p.rawRetentionDays); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM metric_rollup WHERE bucket_start < now() - make_interval(days => $1)`, p.rollupRetentionDays); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	p.stateDigest, p.accessDigest = stateDigest, accessDigest
	if sweep {
		p.lastRollupSweep = now
	}
	memory.acknowledgeMetricBatch(metricCount)
	memory.acknowledgeResourceChanges(changedResources, removedResources)
	memory.acknowledgeInventories(changedInventories)
	return nil
}
