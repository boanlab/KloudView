package config

import (
	"testing"
	"time"
)

// The three feature flags do not default the same way, and each default is a
// deliberate posture: collection on unless refused, code execution off unless
// granted. A flag that flips its default silently changes what an installed
// agent does on every host, so the defaults are pinned here.
func TestFlagDefaultsMatchTheirIntendedPosture(t *testing.T) {
	config := Load()
	if !config.LogStreamEnabled {
		t.Error("log streaming must be on unless it is explicitly refused")
	}
	if config.AutoUpdate {
		t.Error("auto-update must be off unless it is explicitly granted: it lets the server put code on a host")
	}
	if config.TerminalEnabled {
		t.Error("terminal must be off unless it is explicitly granted")
	}
}

func TestLogStreamIsRefusedOnlyByAnExplicitFalse(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  bool
	}{
		{"false", false},
		{"FALSE", false},
		{"False", false},
		{"true", true},
		{"", true},
		{"0", true},  // not the word "false"
		{"no", true}, // not the word "false"
	} {
		t.Setenv("KLOUDVIEW_LOG_STREAM", testCase.value)
		if got := Load().LogStreamEnabled; got != testCase.want {
			t.Errorf("KLOUDVIEW_LOG_STREAM=%q gave %v, want %v", testCase.value, got, testCase.want)
		}
	}
}

// Auto-update is the flag that lets the server replace the binary on a host.
// Anything short of an explicit "true" must leave it off.
func TestAutoUpdateIsGrantedOnlyByAnExplicitTrue(t *testing.T) {
	for _, value := range []string{"", "false", "1", "yes", "TRUE!", "  true"} {
		t.Setenv("KLOUDVIEW_AUTO_UPDATE", value)
		if Load().AutoUpdate {
			t.Errorf("KLOUDVIEW_AUTO_UPDATE=%q enabled self-update", value)
		}
	}
	for _, value := range []string{"true", "TRUE", "True"} {
		t.Setenv("KLOUDVIEW_AUTO_UPDATE", value)
		if !Load().AutoUpdate {
			t.Errorf("KLOUDVIEW_AUTO_UPDATE=%q did not enable self-update", value)
		}
	}
}

// The service allowlist arrives as one comma-separated variable and is the
// only thing bounding what the console can restart.
func TestAllowedServicesIsParsedAndTrimmed(t *testing.T) {
	t.Setenv("KLOUDVIEW_ALLOWED_SERVICES", " containerd.service , ,docker.service,  ")
	services := Load().AllowedServices
	if len(services) != 2 || services[0] != "containerd.service" || services[1] != "docker.service" {
		t.Fatalf("services = %#v", services)
	}
	t.Setenv("KLOUDVIEW_ALLOWED_SERVICES", "")
	if got := Load().AllowedServices; len(got) != 0 {
		t.Fatalf("an unset allowlist must grant nothing, got %#v", got)
	}
}

// The main loop hands this to time.NewTicker, which panics on a non-positive
// duration -- and "-5s" and "0s" parse without error, so checking only the
// parse error let one typo crash-loop the agent on every host it reached.
func TestIntervalFallsBackWhenUnusable(t *testing.T) {
	for _, value := range []string{"", "soon", "-5s", "0s", "0", "-1h", "10"} {
		t.Setenv("KLOUDVIEW_INTERVAL", value)
		if got := Load().Interval; got <= 0 {
			t.Errorf("KLOUDVIEW_INTERVAL=%q gave %v, which would spin the loop", value, got)
		}
	}
	t.Setenv("KLOUDVIEW_INTERVAL", "30s")
	if got := Load().Interval; got != 30*time.Second {
		t.Errorf("interval = %v, want 30s", got)
	}
}
