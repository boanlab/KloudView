package domain

import "testing"

func TestTagExpressionMatches(t *testing.T) {
	tags := map[string]string{"arch": "amd64", "env": "prod"}
	cases := []struct {
		expression TagExpression
		want       bool
	}{
		{TagExpression{Key: "arch", Operator: TagIn, Values: []string{"amd64", "arm64"}}, true},
		{TagExpression{Key: "arch", Operator: TagIn, Values: []string{"arm64"}}, false},
		{TagExpression{Key: "zone", Operator: TagIn, Values: []string{"a"}}, false},
		{TagExpression{Key: "env", Operator: TagNotIn, Values: []string{"dev", "stage"}}, true},
		{TagExpression{Key: "env", Operator: TagNotIn, Values: []string{"prod"}}, false},
		// A resource without the key is not one of the excluded values.
		{TagExpression{Key: "zone", Operator: TagNotIn, Values: []string{"a"}}, true},
		{TagExpression{Key: "arch", Operator: TagExists}, true},
		{TagExpression{Key: "zone", Operator: TagExists}, false},
		{TagExpression{Key: "zone", Operator: TagAbsent}, true},
		{TagExpression{Key: "arch", Operator: TagAbsent}, false},
		{TagExpression{Key: "arch", Operator: "regex"}, false},
	}
	for _, c := range cases {
		if got := c.expression.Matches(tags); got != c.want {
			t.Errorf("%+v.Matches = %v, want %v", c.expression, got, c.want)
		}
	}
}

func TestAlertRuleMatchesResource(t *testing.T) {
	container := Resource{Type: ResourceContainer, Tags: map[string]string{"arch": "amd64"}}
	node := Resource{Type: ResourceNode, Tags: map[string]string{"arch": "arm64"}}
	if !(AlertRule{}).MatchesResource(node) {
		t.Error("a rule without narrowing conditions refused a resource")
	}
	byType := AlertRule{ResourceTypes: []ResourceType{ResourceContainer, ResourceVM}}
	if !byType.MatchesResource(container) || byType.MatchesResource(node) {
		t.Error("resource types did not narrow")
	}
	both := AlertRule{
		ResourceTypes:    []ResourceType{ResourceContainer, ResourceNode},
		MatchExpressions: []TagExpression{{Key: "arch", Operator: TagIn, Values: []string{"amd64"}}},
	}
	if !both.MatchesResource(container) || both.MatchesResource(node) {
		t.Error("types and expressions are not both required")
	}
}
