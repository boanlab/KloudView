package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func TestDefaultBodyIsWhatWasAlwaysSent(t *testing.T) {
	body := renderNotificationBody(domain.NotificationChannel{}, sampleAlert(), "firing", time.Now().UTC())
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("default body: %v (%s)", err, body)
	}
	if decoded["event"] != "firing" || decoded["alert"] == nil || decoded["sentAt"] == nil {
		t.Fatalf("default body lost a field: %s", body)
	}
	// And gains a readable sentence, so a receiver does not have to rebuild it.
	if message, _ := decoded["message"].(string); !strings.Contains(message, "CPU saturation") {
		t.Fatalf("message = %q", message)
	}
}

func TestATemplateShapesTheBodyForItsReceiver(t *testing.T) {
	// What a Slack incoming webhook requires, and refuses every delivery
	// without: a text field.
	channel := domain.NotificationChannel{BodyTemplate: `{"text": "{{message}}", "detail": {{json.alert}}}`}
	body := renderNotificationBody(channel, sampleAlert(), "firing", time.Now().UTC())
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("templated body: %v (%s)", err, body)
	}
	text, _ := decoded["text"].(string)
	if !strings.Contains(text, "CPU saturation") || !strings.Contains(text, "node-01") {
		t.Fatalf("text = %q", text)
	}
	detail, _ := decoded["detail"].(map[string]any)
	if detail["resourceId"] != "node-01" {
		t.Fatalf("inlined alert = %+v", decoded["detail"])
	}
}

func TestATemplateCannotBreakOutOfItsString(t *testing.T) {
	alert := sampleAlert()
	alert.Summary = `he said "stop", then \ broke`
	body := renderNotificationBody(domain.NotificationChannel{BodyTemplate: `{"text": "{{text}}"}`}, alert, "firing", time.Now().UTC())
	if !json.Valid(body) {
		t.Fatalf("a quote in the alert broke the body: %s", body)
	}
}

func TestChannelValidationCatchesWhatWouldFailLater(t *testing.T) {
	slack := "https://hooks.slack.com/services/T000/B000/abc"
	// The default body cannot work against a Slack incoming webhook.
	if err := validateNotificationChannel(domain.NotificationChannel{Name: "slack", Type: "webhook", URL: slack}); err == nil {
		t.Fatal("a Slack webhook with the default body was accepted")
	}
	// With a template that carries text, it is fine.
	if err := validateNotificationChannel(domain.NotificationChannel{Name: "slack", Type: "webhook", URL: slack, BodyTemplate: `{"text": "{{message}}"}`}); err != nil {
		t.Fatalf("a Slack-shaped template was refused: %v", err)
	}
	// A typo is refused with the names that do exist.
	err := validateNotificationChannel(domain.NotificationChannel{Name: "x", Type: "webhook", URL: "https://example.invalid/h", BodyTemplate: `{"text": "{{alert.hostname}}"}`})
	if err == nil || !strings.Contains(err.Error(), "unknown variable") {
		t.Fatalf("typo = %v", err)
	}
	// So is a template that does not produce JSON.
	err = validateNotificationChannel(domain.NotificationChannel{Name: "x", Type: "webhook", URL: "https://example.invalid/h", BodyTemplate: `{"text": "{{message}}"`})
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("broken JSON = %v", err)
	}
}
