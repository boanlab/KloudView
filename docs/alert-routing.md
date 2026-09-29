# Alert inhibition and notification routing

## Inhibition

An inhibition policy suppresses only the notification delivery of matching target alerts while an active source alert exists. The alert's own `firing` state is preserved, and the response's `inhibited` and `inhibitedBy` fields identify the cause of the inhibition. The console marks such an alert `inhibited` beside its status, since a row that looks like every other firing row reads as one somebody was paged about.

A policy consists of source/target severity, hierarchy scope, resource tag selector, and an equal label list. If there are no equal labels, only alerts of the same resource are compared. Label comparison uses resource tags, alert rule labels, severity, and resource ID. When the source is resolved or the policy is deleted, the target alert becomes eligible for notification routing again.

## Notification routing

The current channel type is a generic HTTP webhook. A route selects a channel based on severity, event (`firing`, `resolved`), hierarchy scope, and tag selector. Evaluation stops at the first matching route with `continue=false`.

The default payload carries the event, the alert, the send timestamp, and a `title`,
`text` and `message` assembled from them so a receiver has something to show a person
without rebuilding the sentence itself.

A channel may instead carry a body template, since the shape belongs to the receiver:
plain text with `{{variable}}` placeholders, no loops and no functions, able only to
rearrange what the notification already holds.

| Placeholder | Renders |
|---|---|
| `{{message}}` | a scalar, JSON-escaped, so it sits inside quotes |
| `{{json.alert}}` | a raw subtree, where a value goes |
| `{{json}}` | the whole default payload, which is what an empty template sends |

A Slack incoming webhook URL is refused at save time unless the channel carries a
template with a text field, for example `{"text": "{{message}}"}`, because Slack refuses
anything else and a failed delivery during an incident is a poor place to learn that.

The request timeout is 10 seconds, and on failure it retries up to 3 times at 1-second
and 2-second intervals. A refusal is recorded with the status and with what the endpoint
said in its body, not the status alone. The latest delivery status and HTTP status can be checked in the Web Console and at `/api/v1/notification-deliveries`. Up to 5,000 delivery records are retained.

In production, restrict webhook destinations with an outbound allowlist or egress proxy, and inject authentication header values from an external secret manager.

## API

| Area | Endpoint |
|---|---|
| Inhibition | `/api/v1/alert-inhibitions` |
| Channel | `/api/v1/notification-channels` |
| Route | `/api/v1/notification-routes` |
| Delivery | `GET /api/v1/notification-deliveries` |

Inhibition, Channel, and Route support GET, POST, PUT, and DELETE. Delivery is an execution result, so it supports queries only.
