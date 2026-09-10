package store

import (
	"path/filepath"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func TestPersistentStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	memory := NewMemory()
	memory.PutGroup(domain.Group{ID: "group-01", Name: "Rack-01", Type: "rack"})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", CPU: 42, Memory: 64, Disk: 30})
	memory.PutAlertInhibition(domain.AlertInhibition{ID: "inhibition-01", Name: "Critical suppresses warning", SourceSeverity: "critical", TargetSeverity: "warning"})
	memory.PutNotificationChannel(domain.NotificationChannel{ID: "channel-01", Name: "Webhook", Type: "webhook", URL: "https://example.invalid"})
	memory.PutNotificationRoute(domain.NotificationRoute{ID: "route-01", Name: "Critical", ChannelIDs: []string{"channel-01"}})
	if err := memory.Save(path); err != nil {
		t.Fatal(err)
	}
	restored, err := NewPersistent(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.ListGroups()) != 1 || restored.MetricSummary("node-01").CPUAvg != 42 {
		t.Fatalf("state not restored")
	}
	if len(restored.ListAlertInhibitions()) != 1 || len(restored.ListNotificationChannels()) != 1 || len(restored.ListNotificationRoutes()) != 1 {
		t.Fatalf("alert routing state not restored")
	}
}
