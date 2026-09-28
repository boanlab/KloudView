package store

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func TestPostgresRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("KLOUDVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("KLOUDVIEW_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	database, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.pool.Exec(ctx, `TRUNCATE kloudview_documents, metric_samples, resources, agent_inventories`); err != nil {
		t.Fatal(err)
	}
	memory := NewMemory()
	memory.PutGroup(domain.Group{ID: "rack-01", Name: "Rack 01", Type: "rack"})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", Timestamp: time.Now().UTC().Truncate(time.Microsecond), CPU: 42, Memory: 64, Disk: 30, NetworkRx: 100, NetworkTx: 200})
	accessData := []byte(`{"roles":{},"scopes":{},"bindings":{}}`)
	if err := database.Save(ctx, memory, accessData); err != nil {
		t.Fatal(err)
	}
	restored, restoredAccess, err := database.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.ListGroups()) != 1 {
		t.Fatalf("groups = %d", len(restored.ListGroups()))
	}
	if restored.MetricSummary("node-01").CPUAvg != 42 {
		t.Fatalf("metrics not restored")
	}
	var expectedAccess, actualAccess any
	if json.Unmarshal(accessData, &expectedAccess) != nil || json.Unmarshal(restoredAccess, &actualAccess) != nil || !reflect.DeepEqual(expectedAccess, actualAccess) {
		t.Fatalf("access state = %s", restoredAccess)
	}
}

// Resources are written by what changed rather than inside the state document,
// so the round trip and the incremental path both need proving against a real
// database.
func TestPostgresResourcesPersistIncrementally(t *testing.T) {
	databaseURL := os.Getenv("KLOUDVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("KLOUDVIEW_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	database, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.pool.Exec(ctx, `TRUNCATE kloudview_documents, metric_samples, resources, agent_inventories`); err != nil {
		t.Fatal(err)
	}

	memory := NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "process-01", Name: "sh", Type: domain.ResourceProcess, AgentID: "agent-01"})
	if err := database.Save(ctx, memory, nil); err != nil {
		t.Fatal(err)
	}

	// The document no longer carries them.
	var payload []byte
	if err := database.pool.QueryRow(ctx, `SELECT payload FROM kloudview_documents WHERE kind = 'state'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "process-01") {
		t.Fatal("the state document still carries resources")
	}

	restored, _, err := database.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.ListResources()) != 2 {
		t.Fatalf("restored %d resources, want 2", len(restored.ListResources()))
	}

	// A second save with one change writes one row and leaves the rest alone.
	terminated := time.Now().UTC()
	stopped, _ := restored.Resource("process-01")
	stopped.TerminatedAt = &terminated
	restored.UpsertResource(stopped)
	changed, removed := restored.PendingResourceChanges()
	if len(changed) != 1 || len(removed) != 0 {
		t.Fatalf("pending changes = %d changed, %d removed; want only the edited one", len(changed), len(removed))
	}
	if err := database.Save(ctx, restored, nil); err != nil {
		t.Fatal(err)
	}
	if changed, removed := restored.PendingResourceChanges(); len(changed) != 0 || len(removed) != 0 {
		t.Fatalf("changes still pending after a save: %d/%d", len(changed), len(removed))
	}

	// A deletion reaches the table.
	if err := restored.DeleteResource("process-01"); err != nil {
		t.Fatal(err)
	}
	if err := database.Save(ctx, restored, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.pool.QueryRow(ctx, `SELECT count(*) FROM resources`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("resources table holds %d rows, want 1", count)
	}
}

// A state document may carry resources and inventories inline, from before they
// had their own tables. They have to be scheduled for writing, or the first save
// drops everything the tables have not seen.
func TestPostgresCarriesLegacyDocumentContentAcross(t *testing.T) {
	databaseURL := os.Getenv("KLOUDVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("KLOUDVIEW_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	database, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.pool.Exec(ctx, `TRUNCATE kloudview_documents, metric_samples, resources, agent_inventories`); err != nil {
		t.Fatal(err)
	}

	// A document in the old shape: resources and inventories inside it.
	legacy := NewMemory()
	legacy.UpsertResource(domain.Resource{ID: "node-01", Name: "node", Type: domain.ResourceNode})
	legacy.UpsertResource(domain.Resource{ID: "container-01", Name: "web", Type: domain.ResourceContainer, AgentID: "agent-01"})
	legacy.PutInventory(domain.AgentInventory{AgentID: "agent-01", NodeID: "node-01", ObservedAt: time.Now().UTC().Truncate(time.Microsecond), Data: map[string]any{"hostname": "node-01"}})
	document, err := legacy.MarshalState(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.pool.Exec(ctx, `INSERT INTO kloudview_documents (kind, payload) VALUES ('state', $1)`, document); err != nil {
		t.Fatal(err)
	}

	loaded, _, err := database.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.ListResources()) != 2 || len(loaded.Inventories()) != 1 {
		t.Fatalf("legacy content not read: %d resources, %d inventories", len(loaded.ListResources()), len(loaded.Inventories()))
	}
	if err := database.Save(ctx, loaded, nil); err != nil {
		t.Fatal(err)
	}

	// The next start reads the tables, and nothing is lost.
	restarted, _, err := database.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.ListResources()) != 2 {
		t.Fatalf("resources lost across the split: %d", len(restarted.ListResources()))
	}
	if len(restarted.Inventories()) != 1 {
		t.Fatalf("inventories lost across the split: %d", len(restarted.Inventories()))
	}
	if _, ok := restarted.Resource("container-01"); !ok {
		t.Fatal("an agent-managed resource was dropped")
	}
}

// The rollup lower bound has to land on a bucket boundary. An instant part-way
// into a bucket selects part of its samples, and the upsert would replace a
// complete row with an average taken from the remainder.
func TestBucketStartFloorsToTheBucketHoldingTheInstant(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 7, 32, 500, time.UTC)
	if got := bucketStart(at, 60); !got.Equal(time.Date(2026, 9, 28, 10, 7, 0, 0, time.UTC)) {
		t.Errorf("60s bucket = %s", got)
	}
	if got := bucketStart(at, 300); !got.Equal(time.Date(2026, 9, 28, 10, 5, 0, 0, time.UTC)) {
		t.Errorf("300s bucket = %s", got)
	}
	// Already on a boundary: the bucket that holds it is its own.
	onBoundary := time.Date(2026, 9, 28, 10, 5, 0, 0, time.UTC)
	if got := bucketStart(onBoundary, 300); !got.Equal(onBoundary) {
		t.Errorf("boundary moved to %s", got)
	}
	if got := bucketStart(at, 0); !got.Equal(at) {
		t.Errorf("a bucket size of zero changed the instant to %s", got)
	}
}
