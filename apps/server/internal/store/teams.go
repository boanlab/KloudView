package store

import (
	"errors"
	"sort"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func (s *Memory) PutTeam(team domain.Team) domain.Team {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teams[team.ID] = team
	return team
}

func (s *Memory) Team(id string) (domain.Team, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	team, ok := s.teams[id]
	return team, ok
}

func (s *Memory) ListTeams() []domain.Team {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.Team, 0, len(s.teams))
	for _, team := range s.teams {
		items = append(items, team)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (s *Memory) DeleteTeam(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.teams[id]; !ok {
		return errors.New("team not found")
	}
	delete(s.teams, id)
	return nil
}
