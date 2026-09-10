package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func (s *Server) listTeams(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.store.ListTeams()})
}

func (s *Server) createTeam(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		MemberIDs   []string `json:"memberIds"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_team", "name is required")
		return
	}
	now := time.Now().UTC()
	team := domain.Team{ID: fmt.Sprintf("team-%d", now.UnixNano()), Name: input.Name, Description: input.Description, MemberIDs: input.MemberIDs, CreatedAt: now, UpdatedAt: now}
	if team.MemberIDs == nil {
		team.MemberIDs = []string{}
	}
	writeJSON(w, http.StatusCreated, s.store.PutTeam(team))
}

func (s *Server) updateTeam(w http.ResponseWriter, r *http.Request) {
	team, ok := s.store.Team(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "team not found")
		return
	}
	var input struct {
		Name        *string   `json:"name"`
		Description *string   `json:"description"`
		MemberIDs   *[]string `json:"memberIds"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "invalid_team", "name is required")
			return
		}
		team.Name = name
	}
	if input.Description != nil {
		team.Description = *input.Description
	}
	if input.MemberIDs != nil {
		team.MemberIDs = *input.MemberIDs
	}
	team.UpdatedAt = time.Now().UTC()
	writeJSON(w, http.StatusOK, s.store.PutTeam(team))
}

func (s *Server) deleteTeam(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteTeam(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "team not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
