package inventory

import "testing"

func TestParseFormattedContainers(t *testing.T) {
	items := parseFormattedContainers("abc123\tapi\tregistry/app:1\trunning\n", "podman")
	if len(items) != 1 || items[0].ID != "abc123" || items[0].Name != "api" || items[0].Image != "registry/app:1" || items[0].State != "running" || items[0].Runtime != "podman" {
		t.Fatalf("containers = %+v", items)
	}
	if items := parseFormattedContainers("invalid\n", "docker"); len(items) != 0 {
		t.Fatalf("invalid containers = %+v", items)
	}
}

func TestExecutableFromCmdlineDropsArguments(t *testing.T) {
	command := executableFromCmdline([]byte("/usr/bin/worker\x00--token\x00secret-value\x00"))
	if command != "/usr/bin/worker" {
		t.Fatalf("command = %q", command)
	}
}

// systemctl marks failed and not-found units with a leading glyph. Parsing it
// as the unit name shifts every column and loses the name of exactly the units
// an operator is looking for.
func TestParseServiceLineHandlesTheStatusGlyph(t *testing.T) {
	cases := []struct {
		line string
		want Service
	}{
		{"apparmor.service loaded active exited Load AppArmor profiles",
			Service{Name: "apparmor.service", Load: "loaded", Active: "active", Sub: "exited"}},
		{"● auditd.service not-found inactive dead auditd.service",
			Service{Name: "auditd.service", Load: "not-found", Active: "inactive", Sub: "dead"}},
		{"● nginx.service loaded failed failed A high performance web server",
			Service{Name: "nginx.service", Load: "loaded", Active: "failed", Sub: "failed"}},
	}
	for _, test := range cases {
		got, ok := parseServiceLine(test.line)
		if !ok || got != test.want {
			t.Errorf("parse(%q) = %+v (ok=%v), want %+v", test.line, got, ok, test.want)
		}
	}
	if _, ok := parseServiceLine("   "); ok {
		t.Error("a blank line produced a service")
	}
	if _, ok := parseServiceLine("only.service loaded"); ok {
		t.Error("a short line produced a service")
	}
}
