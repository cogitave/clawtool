package biam

import "testing"

func TestDetectStreamFailure_TurnFailed(t *testing.T) {
	body := `{"type":"thread.started"}
{"type":"turn.started"}
{"type":"item.completed","item":{"type":"agent_message","text":"some intermediate output"}}
{"type":"error","message":"This content was flagged for possible cybersecurity risk."}
{"type":"turn.failed","error":{"message":"This content was flagged for possible cybersecurity risk."}}`
	got := detectStreamFailure(body)
	if got == "" {
		t.Fatal("expected failure detail, got empty")
	}
	if !contains(got, "cybersecurity") {
		t.Errorf("detail should carry the upstream message: %q", got)
	}
}

func TestDetectStreamFailure_HealthyTurn(t *testing.T) {
	body := `{"type":"thread.started"}
{"type":"item.completed","item":{"type":"agent_message","text":"ok"}}
{"type":"turn.completed"}`
	if got := detectStreamFailure(body); got != "" {
		t.Errorf("healthy stream should not flag failure, got %q", got)
	}
}

func TestDetectStreamFailure_IgnoresPerToolFailure(t *testing.T) {
	body := `{"type":"item.completed","item":{"type":"command_execution","status":"failed"}}
{"type":"turn.completed"}`
	if got := detectStreamFailure(body); got != "" {
		t.Errorf("a failed tool call inside a successful turn must not flag failure: %q", got)
	}
}

func TestDetectStreamFailure_EmptyBody(t *testing.T) {
	if got := detectStreamFailure(""); got != "" {
		t.Errorf("empty body should not flag, got %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
