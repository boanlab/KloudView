package store

import (
	"context"
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
ALTER TABLE metric_samples ADD COLUMN IF NOT EXISTS values jsonb NOT NULL DEFAULT '{}'::jsonb;
`, `
ALTER TABLE metric_rollup ADD COLUMN IF NOT EXISTS values_max jsonb NOT NULL DEFAULT '{}'::jsonb;
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
`}

type Postgres struct {
	pool *pgxpool.Pool
	// Retention windows. Raw samples answer "what happened in the last few
	// days" and rollups answer "what has this looked like for months".
	rawRetentionDays    int
	rollupRetentionDays int
	rollupSeconds       int
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
WITH retained AS (
    SELECT resource_id, sampled_at, cpu, memory, disk, network_rx, network_tx
    FROM metric_samples
    WHERE sampled_at >= now() - interval '1 hour'
    UNION ALL
    SELECT resource_id,
           date_bin(interval '5 minutes', sampled_at, timestamptz '1970-01-01') AS sampled_at,
           avg(cpu), avg(memory), avg(disk), max(network_rx), max(network_tx)
    FROM metric_samples
    WHERE sampled_at >= now() - interval '24 hours'
      AND sampled_at < now() - interval '1 hour'
    GROUP BY resource_id, date_bin(interval '5 minutes', sampled_at, timestamptz '1970-01-01')
)
SELECT resource_id, sampled_at, cpu, memory, disk, network_rx, network_tx
FROM retained
ORDER BY resource_id, sampled_at`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	metrics := map[string][]domain.MetricSample{}
	for rows.Next() {
		var sample domain.MetricSample
		if err := rows.Scan(&sample.ResourceID, &sample.Timestamp, &sample.CPU, &sample.Memory, &sample.Disk, &sample.NetworkRx, &sample.NetworkTx); err != nil {
			return nil, nil, err
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
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO kloudview_documents (kind, payload, updated_at) VALUES ('state', $1, now()) ON CONFLICT (kind) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`, stateData); err != nil {
		return err
	}
	if len(accessData) > 0 {
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
	// Summarise raw samples that are about to age out, then prune both tiers.
	// Rollups are what makes a year of history affordable: a five-minute bucket
	// is roughly a fortieth of the rows it replaces.
	if _, err := tx.Exec(ctx, `
INSERT INTO metric_rollup (resource_id, bucket_start, bucket_seconds, cpu, memory, disk,
                           cpu_max, memory_max, disk_max, network_rx, network_tx, values_max, samples)
SELECT resource_id,
       date_bin(make_interval(secs => $1), sampled_at, timestamptz '1970-01-01'),
       $1,
       avg(cpu), avg(memory), avg(disk),
       max(cpu), max(memory), max(disk),
       max(network_rx), max(network_tx),
       -- The peak each named reading reached in the bucket. Expanded to rows
       -- and folded back so a key present in only some samples still keeps
       -- its highest value rather than being lost to the others.
       coalesce((
         SELECT jsonb_object_agg(key, peak)
         FROM (
           SELECT e.key, max((e.value)::numeric) AS peak
           FROM metric_samples inner_samples,
                LATERAL jsonb_each(inner_samples.values) AS e
           WHERE inner_samples.resource_id = metric_samples.resource_id
             AND date_bin(make_interval(secs => $1), inner_samples.sampled_at, timestamptz '1970-01-01')
                 = date_bin(make_interval(secs => $1), metric_samples.sampled_at, timestamptz '1970-01-01')
           GROUP BY e.key
         ) peaks
       ), '{}'::jsonb),
       count(*)
FROM metric_samples
WHERE sampled_at >= now() - make_interval(days => $2)
GROUP BY resource_id, date_bin(make_interval(secs => $1), sampled_at, timestamptz '1970-01-01')
ON CONFLICT (resource_id, bucket_seconds, bucket_start) DO UPDATE
SET cpu = excluded.cpu, memory = excluded.memory, disk = excluded.disk,
    cpu_max = excluded.cpu_max, memory_max = excluded.memory_max, disk_max = excluded.disk_max,
    network_rx = excluded.network_rx, network_tx = excluded.network_tx,
    values_max = excluded.values_max, samples = excluded.samples`,
		p.rollupSeconds, p.rawRetentionDays); err != nil {
		return err
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
	memory.acknowledgeMetricBatch(metricCount)
	memory.acknowledgeResourceChanges(changedResources, removedResources)
	memory.acknowledgeInventories(changedInventories)
	return nil
}
