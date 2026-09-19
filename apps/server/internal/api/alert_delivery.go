package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/access"
	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func validateInhibition(item domain.AlertInhibition) error {
	if strings.TrimSpace(item.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if !validSeverity(item.SourceSeverity) || !validSeverity(item.TargetSeverity) || item.SourceSeverity == item.TargetSeverity {
		return fmt.Errorf("different warning or critical source and target severities are required")
	}
	return nil
}

func validateNotificationChannel(item domain.NotificationChannel) error {
	if strings.TrimSpace(item.Name) == "" || item.Type != "webhook" {
		return fmt.Errorf("name and webhook type are required")
	}
	parsed, err := url.ParseRequestURI(item.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("valid http or https webhook URL is required")
	}
	if err := validateBodyTemplate(item.BodyTemplate); err != nil {
		return err
	}
	// A Slack incoming webhook refuses anything but a Slack-shaped body, and
	// the default body is not one. Saying so here beats discovering it as a
	// failed delivery during an incident.
	if parsed.Host == "hooks.slack.com" && strings.HasPrefix(parsed.Path, "/services/") && strings.TrimSpace(item.BodyTemplate) == "" {
		return fmt.Errorf(`a Slack incoming webhook needs a body template carrying a text field, for example {"text": "{{message}}"}`)
	}
	return nil
}

func validateNotificationRoute(item domain.NotificationRoute, channelExists func(string) bool) error {
	if strings.TrimSpace(item.Name) == "" || len(item.ChannelIDs) == 0 {
		return fmt.Errorf("name and at least one channel are required")
	}
	for _, channelID := range item.ChannelIDs {
		if !channelExists(channelID) {
			return fmt.Errorf("notification channel %s does not exist", channelID)
		}
	}
	for _, severity := range item.Severities {
		if !validSeverity(severity) {
			return fmt.Errorf("unsupported severity %s", severity)
		}
	}
	for _, event := range item.Events {
		if event != "firing" && event != "resolved" {
			return fmt.Errorf("unsupported notification event %s", event)
		}
	}
	return nil
}

func validSeverity(value string) bool { return value == "warning" || value == "critical" }

func (s *Server) processAlertEvent(alert domain.Alert, event string) {
	s.recomputeInhibitions()
	current, ok := s.store.Alert(alert.ID)
	if !ok || current.Inhibited || (event == "firing" && current.Status != "firing") {
		return
	}
	s.dispatchNotification(current, event)
}

func (s *Server) recomputeInhibitions() {
	alerts := s.store.ListAlerts()
	rules := s.store.ListAlertInhibitions()
	becameActive := []domain.Alert{}
	for _, target := range alerts {
		inhibitedBy := []string{}
		if target.Status == "firing" || target.Status == "acknowledged" {
			for _, rule := range rules {
				if !rule.Enabled || target.Severity != rule.TargetSeverity || !s.store.ResourceMatchesScope(target.ResourceID, rule.ScopePath, rule.Selector) {
					continue
				}
				for _, source := range alerts {
					if source.ID == target.ID || source.Status != "firing" || source.Severity != rule.SourceSeverity || !s.store.ResourceMatchesScope(source.ResourceID, rule.ScopePath, rule.Selector) {
						continue
					}
					if s.inhibitionLabelsEqual(source, target, rule.EqualLabels) {
						inhibitedBy = append(inhibitedBy, source.ID)
					}
				}
			}
		}
		slices.Sort(inhibitedBy)
		inhibitedBy = slices.Compact(inhibitedBy)
		if target.Inhibited != (len(inhibitedBy) > 0) || !slices.Equal(target.InhibitedBy, inhibitedBy) {
			wasInhibited := target.Inhibited
			target.Inhibited = len(inhibitedBy) > 0
			target.InhibitedBy = inhibitedBy
			s.store.PutAlert(target)
			if wasInhibited && !target.Inhibited && target.Status == "firing" {
				becameActive = append(becameActive, target)
			}
		}
	}
	for _, alert := range becameActive {
		s.dispatchNotification(alert, "firing")
	}
}

func (s *Server) inhibitionLabelsEqual(source, target domain.Alert, equalLabels []string) bool {
	if len(equalLabels) == 0 {
		return source.ResourceID == target.ResourceID
	}
	sourceLabels, targetLabels := s.alertLabels(source), s.alertLabels(target)
	for _, key := range equalLabels {
		if sourceLabels[key] == "" || sourceLabels[key] != targetLabels[key] {
			return false
		}
	}
	return true
}

func (s *Server) alertLabels(alert domain.Alert) map[string]string {
	labels := map[string]string{"severity": alert.Severity, "resourceId": alert.ResourceID}
	if resource, ok := s.store.Resource(alert.ResourceID); ok {
		for key, value := range resource.Tags {
			labels[key] = value
		}
	}
	if rule, ok := s.store.AlertRule(alert.RuleID); ok {
		for key, value := range rule.Labels {
			labels[key] = value
		}
	}
	return labels
}

func (s *Server) dispatchNotification(alert domain.Alert, event string) {
	for _, route := range s.store.ListNotificationRoutes() {
		if !route.Enabled || len(route.Severities) > 0 && !slices.Contains(route.Severities, alert.Severity) || len(route.Events) > 0 && !slices.Contains(route.Events, event) || !s.store.ResourceMatchesScope(alert.ResourceID, route.ScopePath, route.Selector) {
			continue
		}
		for _, channelID := range route.ChannelIDs {
			channel, ok := s.store.NotificationChannel(channelID)
			if !ok || !channel.Enabled {
				continue
			}
			delivery := s.store.PutNotificationDelivery(domain.NotificationDelivery{ID: fmt.Sprintf("notification-%d", time.Now().UnixNano()), RouteID: route.ID, ChannelID: channel.ID, AlertID: alert.ID, Event: event, Status: "queued"})
			go s.deliverNotification(delivery, channel, alert)
		}
		if !route.Continue {
			break
		}
	}
}

// responseDetailLimit keeps an endpoint that answers with an HTML error page
// from writing it into every delivery record.
const responseDetailLimit = 400

// responseDetail reads the start of an error response as one printable line.
func responseDetail(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, responseDetailLimit+1))
	if err != nil {
		return ""
	}
	truncated := len(data) > responseDetailLimit
	if truncated {
		data = data[:responseDetailLimit]
	}
	detail := strings.Join(strings.Fields(string(data)), " ")
	if detail != "" && truncated {
		detail += "…"
	}
	return detail
}

func (s *Server) deliverNotification(delivery domain.NotificationDelivery, channel domain.NotificationChannel, alert domain.Alert) {
	payload := renderNotificationBody(channel, alert, delivery.Event, time.Now().UTC())
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, channel.URL, bytes.NewReader(payload))
		if err == nil {
			request.Header.Set("Content-Type", "application/json")
			for key, value := range channel.Headers {
				request.Header.Set(key, value)
			}
			response, requestErr := s.notificationClient.Do(request)
			err = requestErr
			if response != nil {
				delivery.StatusCode = response.StatusCode
				if response.StatusCode >= 200 && response.StatusCode < 300 {
					err = nil
				} else if detail := responseDetail(response.Body); detail != "" {
					// The endpoint almost always says why it refused. Closing
					// the body unread left the operator a bare status code and
					// nothing to act on.
					err = fmt.Errorf("webhook status %d: %s", response.StatusCode, detail)
				} else {
					err = fmt.Errorf("webhook status %d (empty response body)", response.StatusCode)
				}
				_ = response.Body.Close()
			}
		}
		cancel()
		delivery.Attempts = attempt
		if err == nil {
			now := time.Now().UTC()
			delivery.Status = "succeeded"
			delivery.Error = ""
			delivery.DeliveredAt = &now
			s.store.PutNotificationDelivery(delivery)
			return
		}
		delivery.Status = "retrying"
		delivery.Error = err.Error()
		s.store.PutNotificationDelivery(delivery)
		if attempt < 3 {
			time.Sleep(time.Duration(1<<(attempt-1)) * time.Second)
		}
	}
	delivery.Status = "failed"
	s.store.PutNotificationDelivery(delivery)
}

func (s *Server) listAlertInhibitions(w http.ResponseWriter, r *http.Request) {
	items := []domain.AlertInhibition{}
	for _, item := range s.store.ListAlertInhibitions() {
		if s.access.Evaluate(access.Request{SubjectID: s.subjectFromRequest(r), Resource: "alert-rules", Action: "read", ResourcePath: item.ScopePath, Tags: item.Selector}).Allowed {
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) putAlertInhibition(w http.ResponseWriter, r *http.Request) {
	var item domain.AlertInhibition
	if err := readJSON(r, &item); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if err := validateInhibition(item); err != nil {
		writeError(w, 400, "invalid_inhibition", err.Error())
		return
	}
	if !s.authorizeConfiguredScope(r, "alert-rules", map[bool]string{true: "update", false: "create"}[r.PathValue("id") != ""], item.ScopePath, item.Selector) {
		writeError(w, 403, "access_denied", "inhibition scope is not assigned")
		return
	}
	status := http.StatusCreated
	if id := r.PathValue("id"); id != "" {
		previous, ok := s.store.AlertInhibition(id)
		if !ok {
			writeError(w, 404, "not_found", "inhibition not found")
			return
		}
		if !s.authorizeConfiguredScope(r, "alert-rules", "update", previous.ScopePath, previous.Selector) {
			writeError(w, 403, "access_denied", "inhibition scope is not assigned")
			return
		}
		item.ID = id
		status = http.StatusOK
	} else {
		item.ID = fmt.Sprintf("inhibition-%d", time.Now().UnixNano())
	}
	writeJSON(w, status, s.store.PutAlertInhibition(item))
	s.recomputeInhibitions()
}

func (s *Server) deleteAlertInhibition(w http.ResponseWriter, r *http.Request) {
	item, ok := s.store.AlertInhibition(r.PathValue("id"))
	if !ok {
		writeError(w, 404, "not_found", "inhibition not found")
		return
	}
	if !s.authorizeConfiguredScope(r, "alert-rules", "delete", item.ScopePath, item.Selector) {
		writeError(w, 403, "access_denied", "inhibition scope is not assigned")
		return
	}
	if s.store.DeleteAlertInhibition(r.PathValue("id")) != nil {
		writeError(w, 404, "not_found", "inhibition not found")
		return
	}
	s.recomputeInhibitions()
	w.WriteHeader(204)
}

func (s *Server) listNotificationChannels(w http.ResponseWriter, _ *http.Request) {
	items := s.store.ListNotificationChannels()
	for index := range items {
		if len(items[index].Headers) > 0 {
			redacted := map[string]string{}
			for key := range items[index].Headers {
				redacted[key] = "***"
			}
			items[index].Headers = redacted
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) putNotificationChannel(w http.ResponseWriter, r *http.Request) {
	var item domain.NotificationChannel
	if err := readJSON(r, &item); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if err := validateNotificationChannel(item); err != nil {
		writeError(w, 400, "invalid_channel", err.Error())
		return
	}
	status := 201
	if id := r.PathValue("id"); id != "" {
		previous, ok := s.store.NotificationChannel(id)
		if !ok {
			writeError(w, 404, "not_found", "channel not found")
			return
		}
		if len(item.Headers) == 0 {
			item.Headers = previous.Headers
		}
		item.ID = id
		status = 200
	} else {
		item.ID = fmt.Sprintf("channel-%d", time.Now().UnixNano())
	}
	writeJSON(w, status, s.store.PutNotificationChannel(item))
}
func (s *Server) deleteNotificationChannel(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteNotificationChannel(r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, 404, "not_found", "channel not found")
			return
		}
		writeError(w, 409, "channel_in_use", err.Error())
		return
	}
	w.WriteHeader(204)
}
func (s *Server) listNotificationRoutes(w http.ResponseWriter, r *http.Request) {
	items := []domain.NotificationRoute{}
	for _, item := range s.store.ListNotificationRoutes() {
		if s.access.Evaluate(access.Request{SubjectID: s.subjectFromRequest(r), Resource: "alert-rules", Action: "read", ResourcePath: item.ScopePath, Tags: item.Selector}).Allowed {
			items = append(items, item)
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) putNotificationRoute(w http.ResponseWriter, r *http.Request) {
	var item domain.NotificationRoute
	if err := readJSON(r, &item); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if err := validateNotificationRoute(item, func(id string) bool { _, ok := s.store.NotificationChannel(id); return ok }); err != nil {
		writeError(w, 400, "invalid_route", err.Error())
		return
	}
	action := "create"
	if r.PathValue("id") != "" {
		action = "update"
	}
	if !s.authorizeConfiguredScope(r, "alert-rules", action, item.ScopePath, item.Selector) {
		writeError(w, 403, "access_denied", "notification route scope is not assigned")
		return
	}
	status := 201
	if id := r.PathValue("id"); id != "" {
		previous, ok := s.store.NotificationRoute(id)
		if !ok {
			writeError(w, 404, "not_found", "route not found")
			return
		}
		if !s.authorizeConfiguredScope(r, "alert-rules", "update", previous.ScopePath, previous.Selector) {
			writeError(w, 403, "access_denied", "notification route scope is not assigned")
			return
		}
		item.ID = id
		status = 200
	} else {
		item.ID = fmt.Sprintf("route-%d", time.Now().UnixNano())
	}
	writeJSON(w, status, s.store.PutNotificationRoute(item))
}
func (s *Server) deleteNotificationRoute(w http.ResponseWriter, r *http.Request) {
	item, ok := s.store.NotificationRoute(r.PathValue("id"))
	if !ok {
		writeError(w, 404, "not_found", "route not found")
		return
	}
	if !s.authorizeConfiguredScope(r, "alert-rules", "delete", item.ScopePath, item.Selector) {
		writeError(w, 403, "access_denied", "notification route scope is not assigned")
		return
	}
	if s.store.DeleteNotificationRoute(r.PathValue("id")) != nil {
		writeError(w, 404, "not_found", "route not found")
		return
	}
	w.WriteHeader(204)
}
func (s *Server) listNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	items := []domain.NotificationDelivery{}
	for _, item := range s.store.ListNotificationDeliveries() {
		alert, ok := s.store.Alert(item.AlertID)
		if ok && s.authorizeResourceTarget(r, "alerts", "read", alert.ResourceID) {
			items = append(items, item)
		}
		if len(items) == 200 {
			break
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
