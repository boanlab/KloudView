// Sidebar navigation. Multi-page sections expose their pages as in-page tabs.
export const nav = [
  { page: "overview", icon: "overview", name: "Dashboard" },

  { label: "Issues" },
  { page: "alerts", icon: "alerting", name: "Alerts" },
  { page: "incidents", icon: "incidents", name: "Incidents" },

  { label: "Infrastructure" },
  { page: "infra-map", icon: "infra", name: "Infrastructure Map" },
  { page: "infrastructure", icon: "inventory", name: "Resources" },
  { page: "utilization", icon: "activity", name: "Utilization" },
  { page: "logs", icon: "audit", name: "Logs" },
  { page: "fleet", icon: "fleet", name: "Agents" },

  { label: "Operations" },
  { page: "terminal", icon: "terminal", name: "Remote shell" },
  { page: "runbooks", icon: "runbooks", name: "Automations" },
  { page: "jobs", icon: "jobs", name: "Task history" },

  { label: "Settings" },
  {
    page: "users",
    icon: "access",
    name: "User Management",
    tabs: [
      ["users", "Users"],
      ["teams", "Teams"],
      ["roles", "Roles"],
      ["scopes", "Scopes"],
      ["bindings", "Role bindings"],
    ],
  },
  {
    page: "groups",
    icon: "groups",
    name: "Group Management",
    tabs: [
      ["groups", "Node groups"],
      ["dynamic-groups", "Dynamic groups"],
      ["tags", "Tags"],
    ],
  },
  {
    page: "unmanaged",
    icon: "infra",
    name: "Unmanaged",
    tabs: [["unmanaged", "Unmanaged resources"]],
  },
  {
    page: "alert-rules",
    icon: "alerting",
    name: "Alerting",
    tabs: [
      ["alert-rules", "Alert rules"],
      ["notification-routing", "Alert delivery"],
    ],
  },
  { page: "audit", icon: "audit", name: "Audit log" },
];
