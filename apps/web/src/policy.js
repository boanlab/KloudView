export const actionPermissions = {
  "create-resource": ["resources", "create"],
  "edit-resource": ["resources", "update"],
  "delete-resource": ["resources", "delete"],
  "create-group": ["groups", "create"],
  "edit-group": ["groups", "update"],
  "manage-members": ["groups", "update"],
  "delete-group": ["groups", "delete"],
  "create-role": ["roles", "create"],
  "edit-role": ["roles", "update"],
  "delete-role": ["roles", "delete"],
  "create-scope": ["scopes", "create"],
  "edit-scope": ["scopes", "update"],
  "delete-scope": ["scopes", "delete"],
  "create-binding": ["bindings", "create"],
  "edit-binding": ["bindings", "update"],
  "delete-binding": ["bindings", "delete"],
  "create-operation": ["operations", "create"],
  "capture-logs": ["operations", "create"],
  "approve-operation": ["operations", "approve"],
  "delete-agent": ["agents", "delete"],
  "issue-token": ["agents", "create"],
  "ack-alert": ["alerts", "update"],
  "resolve-alert": ["alerts", "update"],
  "delete-alert": ["alerts", "delete"],
  "create-incident": ["incidents", "create"],
  "delete-incident": ["incidents", "delete"],
  "edit-incident": ["incidents", "update"],
  "add-incident-note": ["incidents", "update"],
  "change-incident-status": ["incidents", "update"],
  "create-alert-rule": ["alert-rules", "create"],
  "new-rule": ["alert-rules", "create"],
  "edit-alert-rule": ["alert-rules", "update"],
  "delete-alert-rule": ["alert-rules", "delete"],
  silence: ["alert-rules", "create"],
  "edit-alert-silence": ["alert-rules", "update"],
  "delete-alert-silence": ["alert-rules", "delete"],
  "create-runbook": ["runbooks", "create"],
  "edit-runbook": ["runbooks", "update"],
  "delete-runbook": ["runbooks", "delete"],
  "execute-runbook": ["runbooks", "execute"],
  "approve-runbook": ["runbooks", "approve"],
  "create-terminal-session": ["terminal", "create"],
  "connect-terminal": ["terminal", "create"],
  "approve-terminal": ["terminal", "approve"],
  "approve-terminal-self": ["terminal", "approve-self"],
  "close-terminal": ["terminal", "close"],
  "delete-terminal-recording": ["terminal", "close"],
  "create-user": ["users", "create"],
  "edit-user": ["users", "update"],
  "delete-user": ["users", "delete"],
  "create-team": ["teams", "create"],
  "edit-team": ["teams", "update"],
  "delete-team": ["teams", "delete"],
  "auto-group": ["groups", "create"],
  "create-inhibition": ["alert-rules", "create"],
  "edit-inhibition": ["alert-rules", "update"],
  "delete-inhibition": ["alert-rules", "delete"],
  "create-channel": ["alert-rules", "create"],
  "edit-channel": ["alert-rules", "update"],
  "delete-channel": ["alert-rules", "delete"],
  "create-route": ["alert-rules", "create"],
  "edit-route": ["alert-rules", "update"],
  "delete-route": ["alert-rules", "delete"],
};

// Permissions can() asks about directly rather than through a [data-action]
// button. Fetched alongside the mapped ones, since unknown reads as denied.
export const directPermissions = [
  ["relations", "create"],
  ["relations", "delete"],
];

// Every permission the console needs a decision for, deduplicated.
export function permissionChecks() {
  return [
    ...new Set(
      [...Object.values(actionPermissions), ...directPermissions].map((pair) =>
        pair.join(":"),
      ),
    ),
  ];
}

// Absence is not a grant: the map is empty before the first sync and after a
// failed one.
export function hasPermission(permissions, resource, action) {
  return permissions[`${resource}:${action}`] === true;
}
