package main

import (
	"fmt"
	"strings"
)

// Self-check against the production hardening checklist in docs/deployment.md.
// An unhardened deployment is indistinguishable from a hardened one at runtime,
// so each unmet item is reported at startup. TLS is excluded: a reverse proxy is
// invisible from inside the process, so it is declared through PublicURL.
type hardeningConfig struct {
	AdminPassword string
	DevHeaderAuth bool
	// PublicURL is what operators and agents reach this server on. Declaring it
	// is the only way the process can tell whether it is served over TLS.
	PublicURL string
}

const defaultAdminPassword = "admin"

// hardeningWarnings returns one line per unmet checklist item, in the order the
// checklist lists them. Empty means the configuration is production-shaped.
func hardeningWarnings(config hardeningConfig) []string {
	var warnings []string
	switch {
	case config.AdminPassword == "" || config.AdminPassword == defaultAdminPassword:
		warnings = append(warnings, "KLOUDVIEW_ADMIN_PASSWORD is the default: anyone reaching this server can sign in as admin/admin")
	case publishedSecrets[config.AdminPassword]:
		warnings = append(warnings, "KLOUDVIEW_ADMIN_PASSWORD is the placeholder from .env.example, which is published in the repository")
	case len(config.AdminPassword) < 12:
		warnings = append(warnings, fmt.Sprintf("KLOUDVIEW_ADMIN_PASSWORD is %d characters; use at least 12", len(config.AdminPassword)))
	}
	if config.DevHeaderAuth {
		warnings = append(warnings, "KLOUDVIEW_DEV_HEADER_AUTH is on: an X-KloudView-Subject header selects an identity without a session")
	}
	if url := strings.TrimSpace(config.PublicURL); url == "" {
		warnings = append(warnings, "KLOUDVIEW_PUBLIC_URL is unset, so TLS cannot be confirmed; agent credentials and the enrollment token cross the network in cleartext over plain HTTP")
	} else if !strings.HasPrefix(strings.ToLower(url), "https://") {
		warnings = append(warnings, "KLOUDVIEW_PUBLIC_URL is "+url+": agent credentials and the enrollment token cross the network in cleartext")
	}
	return warnings
}
