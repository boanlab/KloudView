package store

import (
	"sync"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// A resource read from the store is changed and written back by the heartbeat,
// metric and inventory handlers. Changing it must not reach the stored copy,
// which other requests read without the lock.
func TestResourceReturnsMapsTheCallerOwns(t *testing.T) {
	memory := NewMemory()
	memory.UpsertResource(domain.Resource{
		ID:         "node-01",
		Attributes: map[string]string{"agentVersion": "1.0.0"},
		Tags:       map[string]string{"rack": "r1"},
	})

	resource, _ := memory.Resource("node-01")
	resource.Attributes["agentVersion"] = "2.0.0"
	resource.Tags["rack"] = "r6"

	stored, _ := memory.Resource("node-01")
	if stored.Attributes["agentVersion"] != "1.0.0" || stored.Tags["rack"] != "r1" {
		t.Fatalf("a caller's change reached the store before it was written back: %v %v", stored.Attributes, stored.Tags)
	}
	for _, listed := range memory.ListResources() {
		if listed.Attributes["agentVersion"] != "1.0.0" || listed.Tags["rack"] != "r1" {
			t.Fatalf("a caller's change reached the listing: %v %v", listed.Attributes, listed.Tags)
		}
	}
}

// The shape of the crash: one request lists resources and ranges over their
// maps while heartbeats change and upsert the same resource. Run with -race.
func TestResourceUpdatesDoNotRaceListings(t *testing.T) {
	memory := NewMemory()
	memory.UpsertResource(domain.Resource{
		ID:         "node-01",
		Attributes: map[string]string{},
		Tags:       map[string]string{},
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, resource := range memory.ListResources() {
				for key, value := range resource.Tags {
					_ = key + value
				}
				for key, value := range resource.Attributes {
					_ = key + value
				}
			}
		}
	}()
	for i := range 2000 {
		resource, _ := memory.Resource("node-01")
		resource.Attributes["agentVersion"] = string(rune('a' + i%26))
		resource.Tags[string(rune('a'+i%26))] = "label"
		memory.UpsertResource(resource)
	}
	close(stop)
	wg.Wait()
}
