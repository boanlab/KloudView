package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// The body a webhook receives belongs to the receiver, not to this server.
// Sending one fixed shape meant anything that could not read it was simply
// unreachable: a Slack incoming webhook refuses every delivery with
// 400 missing_text_or_fallback_or_attachments, because it wants a text field
// and we sent {event, alert, sentAt}.
//
// A channel may now carry a body template — plain text with {{variable}}
// placeholders. There are no loops and no functions; a template can only
// rearrange what the notification already contains.
//
//	"text": "{{message}}"   a scalar, JSON-escaped, so it sits inside quotes
//	"alert": {{json.alert}} a raw subtree, where a value goes
//	{{json}}                the whole default payload, which is the default

const (
	bodyTemplateLimit = 8192
	rawPrefix         = "json"
)

var templateVariable = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.]+)\s*\}\}`)

// defaultBodyTemplate is what a channel sends when it defines none: the
// payload this server has always sent.
func defaultBodyTemplate() string { return "{{" + rawPrefix + "}}" }

// notificationPayload is what {{json}} renders and what a channel with no
// template receives. title, text and message are added so a receiver has
// something to show a human without rebuilding the sentence itself.
func notificationPayload(alert domain.Alert, event string, sentAt time.Time) map[string]any {
	title := fmt.Sprintf("[%s] %s %s", strings.ToUpper(alert.Severity), alert.Name, event)
	text := strings.TrimSpace(alert.Summary)
	if alert.ResourceID != "" {
		text = strings.TrimSpace(text + "\nresource: " + alert.ResourceID)
	}
	return map[string]any{
		"event": event, "alert": alert, "sentAt": sentAt,
		"title": title, "text": text, "message": strings.TrimSpace(title + "\n" + text),
	}
}

func renderNotificationBody(channel domain.NotificationChannel, alert domain.Alert, event string, sentAt time.Time) []byte {
	template := strings.TrimSpace(channel.BodyTemplate)
	if template == "" {
		template = defaultBodyTemplate()
	}
	scalars, raws := templateValues(notificationPayload(alert, event, sentAt))
	return []byte(renderBody(template, scalars, raws))
}

// templateValues splits the payload into the scalars a {{name}} may print and
// the subtrees a {{json.name}} may inline, keyed by dotted path. Names are the
// payload's own JSON field names, so what a template can reference is exactly
// what is sent.
func templateValues(payload map[string]any) (map[string]string, map[string]json.RawMessage) {
	scalars, raws := map[string]string{}, map[string]json.RawMessage{}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return scalars, raws
	}
	var generic any
	if json.Unmarshal(encoded, &generic) != nil {
		return scalars, raws
	}
	raws[""] = encoded
	flattenValue("", generic, scalars, raws)
	return scalars, raws
}

func flattenValue(path string, value any, scalars map[string]string, raws map[string]json.RawMessage) {
	if path != "" {
		if encoded, err := json.Marshal(value); err == nil {
			raws[path] = encoded
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			child := key
			if path != "" {
				child = path + "." + key
			}
			flattenValue(child, nested, scalars, raws)
		}
	case []any:
		// A list has no scalar form; a template reaches it through {{json.…}}.
	case nil:
		scalars[path] = ""
	case string:
		scalars[path] = typed
	case bool:
		scalars[path] = strconv.FormatBool(typed)
	case float64:
		// JSON numbers arrive as float64; a byte count must not come back out
		// as 1.142708e+06.
		scalars[path] = strconv.FormatFloat(typed, 'f', -1, 64)
	}
}

// renderBody substitutes every placeholder. A name with no value renders empty
// rather than failing: one template serves every alert, and a field this alert
// happens to lack must not cost the delivery.
func renderBody(template string, scalars map[string]string, raws map[string]json.RawMessage) string {
	return templateVariable.ReplaceAllStringFunc(template, func(match string) string {
		name := strings.TrimSpace(templateVariable.FindStringSubmatch(match)[1])
		if name == rawPrefix || strings.HasPrefix(name, rawPrefix+".") {
			if raw, ok := raws[strings.TrimPrefix(strings.TrimPrefix(name, rawPrefix), ".")]; ok {
				return string(raw)
			}
			return "null"
		}
		return jsonEscape(scalars[name])
	})
}

// jsonEscape returns the value as it appears inside a JSON string, without the
// quotes, so a template can write "text": "{{message}}".
func jsonEscape(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded[1 : len(encoded)-1])
}

// sampleAlert is what a template is checked against when a channel is saved.
func sampleAlert() domain.Alert {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return domain.Alert{ID: "alert-sample", RuleID: "rule-cpu", Name: "CPU saturation", Severity: "critical",
		Status: "firing", ResourceID: "node-01", Summary: "cpu > 40.00, observed 67.94", StartedAt: now, UpdatedAt: now}
}

// templateVariableNames lists every name a template may use, for the error a
// typo produces and for the help beside the field.
func templateVariableNames() []string {
	scalars, raws := templateValues(notificationPayload(sampleAlert(), "firing", time.Now().UTC()))
	names := map[string]bool{rawPrefix: true}
	for name := range scalars {
		names[name] = true
	}
	for name := range raws {
		if name != "" {
			names[rawPrefix+"."+name] = true
		}
	}
	list := make([]string, 0, len(names))
	for name := range names {
		list = append(list, name)
	}
	sort.Strings(list)
	return list
}

// validateBodyTemplate refuses a template that names something that does not
// exist or cannot produce a JSON body, because both would otherwise appear
// only when a real alert failed to deliver.
func validateBodyTemplate(template string) error {
	template = strings.TrimSpace(template)
	if template == "" {
		return nil
	}
	if len(template) > bodyTemplateLimit {
		return fmt.Errorf("body template must be %d characters or fewer", bodyTemplateLimit)
	}
	known := map[string]bool{}
	for _, name := range templateVariableNames() {
		known[name] = true
	}
	for _, match := range templateVariable.FindAllStringSubmatch(template, -1) {
		if name := strings.TrimSpace(match[1]); !known[name] {
			return fmt.Errorf("unknown variable %q; available: %s", name, strings.Join(templateVariableNames(), ", "))
		}
	}
	for _, event := range []string{"firing", "resolved"} {
		scalars, raws := templateValues(notificationPayload(sampleAlert(), event, time.Now().UTC()))
		if body := renderBody(template, scalars, raws); !json.Valid([]byte(body)) {
			return fmt.Errorf("template does not produce valid JSON for a %s notification: %s", event, strings.TrimSpace(body))
		}
	}
	return nil
}
