package config

import (
	"os"
	"strings"
	"time"
)

type Config struct {
	ServerURL       string
	EnrollmentToken string
	StatePath       string
	Interval        time.Duration
	AllowedServices []string
	TerminalEnabled bool
	// LogStreamEnabled follows the journal continuously. On by default: a node
	// that loses power cannot be asked afterwards what went wrong, so the
	// evidence has to already be on the server.
	LogStreamEnabled bool
	AutoUpdate       bool
	TerminalUser     string
}

func Load() Config {
	// "-5s" and "0s" parse cleanly, so the error alone is not enough: the main
	// loop passes this to time.NewTicker, which panics on a non-positive value.
	interval, err := time.ParseDuration(getenv("KLOUDVIEW_INTERVAL", "10s"))
	if err != nil || interval <= 0 {
		interval = 10 * time.Second
	}
	services := []string{}
	for _, service := range strings.Split(os.Getenv("KLOUDVIEW_ALLOWED_SERVICES"), ",") {
		if value := strings.TrimSpace(service); value != "" {
			services = append(services, value)
		}
	}
	return Config{ServerURL: getenv("KLOUDVIEW_SERVER_URL", "http://127.0.0.1:8080"), EnrollmentToken: os.Getenv("KLOUDVIEW_ENROLLMENT_TOKEN"), StatePath: getenv("KLOUDVIEW_STATE_PATH", "/var/lib/kloudview/agent.json"), Interval: interval, AllowedServices: services, TerminalEnabled: strings.EqualFold(os.Getenv("KLOUDVIEW_TERMINAL_ENABLED"), "true"), TerminalUser: os.Getenv("KLOUDVIEW_TERMINAL_USER"), AutoUpdate: strings.EqualFold(os.Getenv("KLOUDVIEW_AUTO_UPDATE"), "true"), LogStreamEnabled: !strings.EqualFold(os.Getenv("KLOUDVIEW_LOG_STREAM"), "false")}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
