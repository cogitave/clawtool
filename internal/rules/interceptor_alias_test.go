package rules

import "testing"

// The MCP working-group SEP-1763 / experimental-ext-interceptors
// proposal spells `pre_tool_use` as `interceptor:pre_tool_use`.
// The loader accepts both spellings and normalizes the alias to
// the canonical form so Evaluate keeps a single dispatch path.
// See docs/rules.md (events table) for the operator-facing doc.

func TestLoader_AcceptsInterceptorAlias(t *testing.T) {
	body := []byte(`
[[rule]]
name      = "alias-form"
when      = "interceptor:pre_tool_use"
severity  = "warn"
condition = 'true'
`)
	rules, err := ParseBytes(body)
	if err != nil {
		t.Fatalf("ParseBytes(interceptor alias): %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
}

func TestLoader_NormalizesAliasToPreToolUse(t *testing.T) {
	body := []byte(`
[[rule]]
name      = "alias-form"
when      = "interceptor:pre_tool_use"
severity  = "warn"
condition = 'true'
`)
	rules, err := ParseBytes(body)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if got := rules[0].When; got != EventPreToolUse {
		t.Errorf("rules[0].When = %q, want %q (normalized canonical form)",
			got, EventPreToolUse)
	}
}

func TestEvaluate_AliasFiresOnPreToolUse(t *testing.T) {
	body := []byte(`
[[rule]]
name      = "alias-form"
when      = "interceptor:pre_tool_use"
severity  = "block"
condition = 'true'
hint      = "alias should fire on pre_tool_use dispatch"
`)
	rules, err := ParseBytes(body)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	v := Evaluate(rules, Context{Event: EventPreToolUse})
	if len(v.Results) != 1 {
		t.Fatalf("expected 1 result on EventPreToolUse, got %d (%+v)",
			len(v.Results), v.Results)
	}
	if v.Results[0].Rule != "alias-form" {
		t.Errorf("result rule = %q, want %q", v.Results[0].Rule, "alias-form")
	}
}
