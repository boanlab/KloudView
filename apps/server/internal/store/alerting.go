package store

import (
	"errors"
	"sort"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func (s *Memory) PutAlertInhibition(item domain.AlertInhibition) domain.AlertInhibition {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if previous, ok := s.alertInhibitions[item.ID]; ok {
		item.CreatedAt = previous.CreatedAt
	} else {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	s.alertInhibitions[item.ID] = item
	return item
}

func (s *Memory) ListAlertInhibitions() []domain.AlertInhibition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.AlertInhibition, 0, len(s.alertInhibitions))
	for _, item := range s.alertInhibitions {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (s *Memory) AlertInhibition(id string) (domain.AlertInhibition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.alertInhibitions[id]
	return item, ok
}

func (s *Memory) DeleteAlertInhibition(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.alertInhibitions[id]; !ok {
		return ErrNotFound
	}
	delete(s.alertInhibitions, id)
	return nil
}

func (s *Memory) PutNotificationChannel(item domain.NotificationChannel) domain.NotificationChannel {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if previous, ok := s.notificationChannels[item.ID]; ok {
		item.CreatedAt = previous.CreatedAt
	} else {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	s.notificationChannels[item.ID] = item
	return item
}

func (s *Memory) ListNotificationChannels() []domain.NotificationChannel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.NotificationChannel, 0, len(s.notificationChannels))
	for _, item := range s.notificationChannels {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (s *Memory) NotificationChannel(id string) (domain.NotificationChannel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.notificationChannels[id]
	return item, ok
}

func (s *Memory) DeleteNotificationChannel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.notificationChannels[id]; !ok {
		return ErrNotFound
	}
	for _, route := range s.notificationRoutes {
		for _, channelID := range route.ChannelIDs {
			if channelID == id {
				return errors.New("notification channel is in use")
			}
		}
	}
	delete(s.notificationChannels, id)
	return nil
}

func (s *Memory) PutNotificationRoute(item domain.NotificationRoute) domain.NotificationRoute {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if previous, ok := s.notificationRoutes[item.ID]; ok {
		item.CreatedAt = previous.CreatedAt
	} else {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	s.notificationRoutes[item.ID] = item
	return item
}

func (s *Memory) ListNotificationRoutes() []domain.NotificationRoute {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.NotificationRoute, 0, len(s.notificationRoutes))
	for _, item := range s.notificationRoutes {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (s *Memory) NotificationRoute(id string) (domain.NotificationRoute, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.notificationRoutes[id]
	return item, ok
}

func (s *Memory) DeleteNotificationRoute(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.notificationRoutes[id]; !ok {
		return ErrNotFound
	}
	delete(s.notificationRoutes, id)
	return nil
}

func (s *Memory) PutNotificationDelivery(item domain.NotificationDelivery) domain.NotificationDelivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if previous, ok := s.notificationDeliveries[item.ID]; ok {
		item.CreatedAt = previous.CreatedAt
	} else {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	s.notificationDeliveries[item.ID] = item
	if len(s.notificationDeliveries) > 5000 {
		var oldestID string
		var oldest time.Time
		for id, delivery := range s.notificationDeliveries {
			if oldestID == "" || delivery.CreatedAt.Before(oldest) {
				oldestID, oldest = id, delivery.CreatedAt
			}
		}
		delete(s.notificationDeliveries, oldestID)
	}
	return item
}

func (s *Memory) ListNotificationDeliveries() []domain.NotificationDelivery {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.NotificationDelivery, 0, len(s.notificationDeliveries))
	for _, item := range s.notificationDeliveries {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items
}
