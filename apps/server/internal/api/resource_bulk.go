package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

const (
	bulkResourceLimit = 1000
	bulkTagLimit      = 32
	tagPartMaxLength  = 128
)

// resourceBulkRequest changes tags and group membership on many resources at
// once. Only tags are touched: the server applies the change to the record it
// holds, so a client never sends back a copy that may have gone stale while
// an agent was writing attributes.
type resourceBulkRequest struct {
	ResourceIDs  []string          `json:"resourceIds"`
	SetTags      map[string]string `json:"setTags,omitempty"`
	RemoveTags   []string          `json:"removeTags,omitempty"`
	AddToGroupID string            `json:"addToGroupId,omitempty"`
}

type bulkFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

func validateResourceBulk(request resourceBulkRequest) error {
	if len(request.ResourceIDs) == 0 || len(request.ResourceIDs) > bulkResourceLimit {
		return fmt.Errorf("between 1 and %d resource ids are required", bulkResourceLimit)
	}
	seen := map[string]bool{}
	for _, id := range request.ResourceIDs {
		if strings.TrimSpace(id) == "" || seen[id] {
			return fmt.Errorf("resource ids must be non-empty and unique")
		}
		seen[id] = true
	}
	if len(request.SetTags)+len(request.RemoveTags) == 0 && request.AddToGroupID == "" {
		return fmt.Errorf("nothing to change: give setTags, removeTags or addToGroupId")
	}
	if len(request.SetTags)+len(request.RemoveTags) > bulkTagLimit {
		return fmt.Errorf("at most %d tag changes per request", bulkTagLimit)
	}
	for key, value := range request.SetTags {
		if err := validateTagPart(key); err != nil {
			return err
		}
		if err := validateTagPart(value); err != nil {
			return err
		}
	}
	for _, key := range request.RemoveTags {
		if err := validateTagPart(key); err != nil {
			return err
		}
		if _, both := request.SetTags[key]; both {
			return fmt.Errorf("tag %q is both set and removed", key)
		}
	}
	return nil
}

func validateTagPart(part string) error {
	if strings.TrimSpace(part) == "" || len(part) > tagPartMaxLength {
		return fmt.Errorf("tag keys and values must be non-blank and at most %d characters", tagPartMaxLength)
	}
	return nil
}

// bulkUpdateResources applies one change to each resource on its own: a
// resource the caller may not change, or one that is gone, is reported and
// the rest still go through.
func (s *Server) bulkUpdateResources(w http.ResponseWriter, r *http.Request) {
	var request resourceBulkRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateResourceBulk(request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_bulk", err.Error())
		return
	}
	var group domain.Group
	if request.AddToGroupID != "" {
		found, ok := s.store.Group(request.AddToGroupID)
		if !ok {
			writeError(w, http.StatusBadRequest, "group_not_found", "group not found")
			return
		}
		if found.Mode == "dynamic" {
			writeError(w, http.StatusConflict, "dynamic_group", "dynamic group membership is selector-managed")
			return
		}
		if !s.authorizeGroupTarget(r, "groups", "update", found.ID) {
			writeError(w, http.StatusForbidden, "access_denied", "group scope is not assigned")
			return
		}
		group = found
	}
	changesTags := len(request.SetTags)+len(request.RemoveTags) > 0
	updated := []string{}
	failed := []bulkFailure{}
	for _, id := range request.ResourceIDs {
		resource, ok := s.store.Resource(id)
		if !ok {
			failed = append(failed, bulkFailure{ID: id, Error: "resource not found"})
			continue
		}
		if changesTags && !s.authorizeResourceTarget(r, "resources", "update", id) {
			failed = append(failed, bulkFailure{ID: id, Error: "resource scope is not assigned"})
			continue
		}
		if group.ID != "" && !s.authorizeResourceTarget(r, "groups", "update", id) {
			failed = append(failed, bulkFailure{ID: id, Error: "membership scope is not assigned"})
			continue
		}
		if changesTags {
			tags := make(map[string]string, len(resource.Tags)+len(request.SetTags))
			for key, value := range resource.Tags {
				tags[key] = value
			}
			for key, value := range request.SetTags {
				tags[key] = value
			}
			for _, key := range request.RemoveTags {
				delete(tags, key)
			}
			resource.Tags = tags
			s.store.UpsertResource(resource)
		}
		if group.ID != "" {
			s.store.PutMembership(domain.GroupMembership{ID: stableID("membership", group.ID+"-"+id), GroupID: group.ID, ResourceID: id})
		}
		updated = append(updated, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": updated, "failed": failed})
}
