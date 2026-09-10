# Alert inhibition and notification routing

## Inhibition

An inhibition policy suppresses only the notification delivery of matching target alerts while an active source alert exists. The alert's own `firing` state is preserved, and the response's `inhibited` and `inhibitedBy` fields identify the cause of the inhibition.

A policy consists of source/target severity, hierarchy scope, resource tag selector, and an equal label list. If there are no equal labels, only alerts of the same resource are compared. Label comparison uses resource tags, alert rule labels, severity, and resource ID. When the source is resolved or the policy is deleted, the target alert becomes eligible for notification routing again.

## Notification routing

The current channel type is a generic HTTP webhook. A route selects a channel based on severity, event (`firing`, `resolved`), hierarchy scope, and tag selector. Evaluation stops at the first matching route with `continue=false`.

The webhook payload includes the event, the alert, and the send timestamp. The request timeout is 10 seconds, and on failure it retries up to 3 times at 1-second and 2-second intervals. The latest delivery status and HTTP status can be checked in the Web Console and at `/api/v1/notification-deliveries`. Up to 5,000 delivery records are retained.

In production, restrict webhook destinations with an outbound allowlist or egress proxy, and inject authentication header values from an external secret manager.

## API

| Area | Endpoint |
|---|---|
| Inhibition | `/api/v1/alert-inhibitions` |
| Channel | `/api/v1/notification-channels` |
| Route | `/api/v1/notification-routes` |
| Delivery | `GET /api/v1/notification-deliveries` |

Inhibition, Channel, and Route support GET, POST, PUT, and DELETE. Delivery is an execution result, so it supports queries only.
