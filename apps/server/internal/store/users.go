package store

import (
	"errors"
	"sort"
	"strings"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func (s *Memory) PutUser(user domain.User) domain.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[user.ID] = user
	return user
}

func (s *Memory) User(id string) (domain.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[id]
	return user, ok
}

func (s *Memory) UserByUsername(username string) (domain.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	target := strings.ToLower(strings.TrimSpace(username))
	for _, user := range s.users {
		if strings.ToLower(user.Username) == target {
			return user, true
		}
	}
	return domain.User{}, false
}

func (s *Memory) ListUsers() []domain.User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.User, 0, len(s.users))
	for _, user := range s.users {
		items = append(items, user)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Username < items[j].Username })
	return items
}

func (s *Memory) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[id]; !ok {
		return errors.New("user not found")
	}
	delete(s.users, id)
	return nil
}
