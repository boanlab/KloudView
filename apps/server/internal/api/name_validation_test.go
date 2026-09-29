package api

import (
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// A name made of spaces is not a name. One accepted here is unfindable in the
// list it appears in, in the alerts it raises, and in the incidents declared
// from those.
func TestANameOfSpacesIsRefused(t *testing.T) {
	for _, name := range []string{"", " ", "   ", "\t", "\n  \t "} {
		if err := validateName(name); err == nil {
			t.Errorf("accepted %q as a name", name)
		}
	}
}

// Long enough for a sentence, short enough that a table cell still reads as one.
func TestANameHasACeiling(t *testing.T) {
	if err := validateName(repeat("n", nameLimit)); err != nil {
		t.Errorf("a name at the limit was refused: %v", err)
	}
	if err := validateName(repeat("n", nameLimit+1)); err == nil {
		t.Error("a name past the limit was accepted")
	}
}

// The rule is the same wherever a person types a name.
func TestEveryNamedThingAnswersTheSameWay(t *testing.T) {
	spaces := "   "
	long := repeat("n", nameLimit+1)

	if err := validateAlertRule(alertRuleNamed(spaces)); err == nil {
		t.Error("an alert rule took a name of spaces")
	}
	if err := validateAlertRule(alertRuleNamed(long)); err == nil {
		t.Error("an alert rule took a name past the limit")
	}
	if err := validateRunbook(runbookNamed(spaces)); err == nil {
		t.Error("a runbook took a name of spaces")
	}
	if err := validateRunbook(runbookNamed(long)); err == nil {
		t.Error("a runbook took a name past the limit")
	}
	// And a valid one still passes, so the rule refuses nothing it should keep.
	if err := validateAlertRule(alertRuleNamed("memory above capacity")); err != nil {
		t.Errorf("a good alert rule was refused: %v", err)
	}
	if err := validateRunbook(runbookNamed("refresh inventory")); err != nil {
		t.Errorf("a good runbook was refused: %v", err)
	}
}

func alertRuleNamed(name string) domain.AlertRule {
	return domain.AlertRule{
		Name: name, Metric: "cpu", Operator: ">", Threshold: 90,
		Duration: "1m", Severity: "warning",
	}
}

func runbookNamed(name string) domain.Runbook {
	return domain.Runbook{
		Name: name, Risk: "low",
		Steps: []domain.RunbookStep{{Name: "refresh", Operation: "inventory.refresh"}},
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, s[0])
	}
	return string(out)
}
