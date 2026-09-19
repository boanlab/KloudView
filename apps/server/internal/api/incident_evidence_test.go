package api

import (
	"strings"
	"testing"
)

// The excerpt is copied into the incident, which lives in the state document,
// so what can be attached is a finding rather than a log. A read's own output
// is 117 kilobytes; the two lines that explain it are not.
func TestAttachedEvidenceIsAnExcerptNotALog(t *testing.T) {
	if err := validateIncidentEvidence(map[string]string{
		"excerpt": "kernel: Memory cgroup out of memory: Killed process 4079295 (dd)\nkernel: oom-kill:constraint=CONSTRAINT_MEMCG",
	}); err != nil {
		t.Fatalf("a two-line finding was refused: %v", err)
	}

	whole := strings.Repeat("2026-09-19T12:00:00+00:00 node-01 grafana[1208]: a line\n", 200)
	if err := validateIncidentEvidence(map[string]string{"excerpt": whole}); err == nil {
		t.Fatal("a whole log was accepted into the state document")
	}

	// Many short lines are refused too: the cost is the rewriting, and a
	// hundred one-word lines is still a hundred lines to read back.
	many := strings.TrimSuffix(strings.Repeat("x\n", evidenceMaxLines+5), "\n")
	if len(many) > evidenceMaxBytes {
		t.Fatalf("the fixture is %d bytes; it has to pass the byte check to test the line check", len(many))
	}
	if err := validateIncidentEvidence(map[string]string{"excerpt": many}); err == nil {
		t.Fatal("more lines than the cap were accepted")
	}
}

// A note is still a note. Attaching evidence is an extra thing an entry may
// carry, not a new kind of entry with its own rules.
func TestAnEntryWithoutEvidenceIsUnaffected(t *testing.T) {
	if err := validateIncidentEvidence(nil); err != nil {
		t.Fatalf("a plain note was refused: %v", err)
	}
	if err := validateIncidentEvidence(map[string]string{"resourceId": "node-01"}); err != nil {
		t.Fatalf("an entry with other metadata was refused: %v", err)
	}
	// But an empty one is a mistake worth naming rather than storing.
	if err := validateIncidentEvidence(map[string]string{"excerpt": "   \n  "}); err == nil {
		t.Fatal("an empty excerpt was stored")
	}
}
