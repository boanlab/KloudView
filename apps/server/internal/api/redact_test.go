package api

import (
	"strings"
	"testing"
)

func TestRedactHidesCredentials(t *testing.T) {
	cases := []struct{ in, mustNotContain string }{
		{`sshd: password=hunter2 for root`, "hunter2"},
		{`Authorization: Bearer eyJhbGciOiJIUzI1NiJ9`, "eyJhbGciOiJIUzI1NiJ9"},
		{`api_key: "abcdef123456"`, "abcdef123456"},
		{`postgres://kloudview:supersecret@db:5432`, "supersecret"},
	}
	for _, test := range cases {
		got := redactSecrets(test.in)
		if strings.Contains(got, test.mustNotContain) {
			t.Errorf("%q leaked through as %q", test.in, got)
		}
		if !strings.Contains(got, "REDACTED") {
			t.Errorf("%q was not marked as redacted: %q", test.in, got)
		}
	}
}

// Attribution is the reason to keep an auth log at all: who, from where, doing
// what. None of it is secret material and none of it may be masked.
func TestRedactKeepsAttribution(t *testing.T) {
	lines := []string{
		"Failed password for invalid user admin from 203.0.113.9 port 40222 ssh2",
		"Accepted publickey for boan from 10.0.0.5 port 51234 ssh2",
		"pam_unix(sshd:auth): authentication failure; rhost=203.0.113.9 user=root",
		"boan : TTY=pts/0 ; PWD=/home/boan ; USER=root ; COMMAND=/usr/bin/systemctl restart nginx",
		"Sep 08 11:00:00 ryzen1 systemd[1]: Started Session 12 of user boan.",
	}
	for _, line := range lines {
		if got := redactSecrets(line); got != line {
			t.Errorf("attribution data was altered:\n in: %q\nout: %q", line, got)
		}
	}
}
