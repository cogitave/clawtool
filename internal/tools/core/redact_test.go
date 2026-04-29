package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactSecrets_BearerToken(t *testing.T) {
	in := "request failed: Authorization: Bearer ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	out := redactSecrets(in)
	if strings.Contains(out, "ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Fatalf("token leaked: %q", out)
	}
	if !strings.Contains(out, "Authorization: Bearer [REDACTED]") {
		t.Fatalf("redaction shape lost: %q", out)
	}
}

func TestRedactSecrets_EnvVarStyle(t *testing.T) {
	cases := []struct{ in, leak string }{
		{"OPENAI_API_KEY=sk-secret-1234567890abcdef value=x", "sk-secret-1234567890abcdef"},
		{"GH_TOKEN=ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA boom", "ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{"DB_PASSWORD=hunter2 next", "hunter2"},
		{"SERVICE_SECRET=topsekrit", "topsekrit"},
	}
	for _, tc := range cases {
		got := redactSecrets(tc.in)
		if strings.Contains(got, tc.leak) {
			t.Fatalf("leaked %q in %q (input: %q)", tc.leak, got, tc.in)
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Fatalf("no redaction marker: %q", got)
		}
	}
}

func TestRedactSecrets_KeyPrefixes(t *testing.T) {
	// Tokens that appear bare (without a KEY= prefix) — still match
	// via the prefix-pattern rules.
	cases := []string{
		"phc_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789",
		"sk-1234567890abcdef1234",
		"ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"sk_live_abcdef1234567890abcd",
	}
	for _, in := range cases {
		got := redactSecrets("error talking to upstream: " + in + " — retry")
		if strings.Contains(got, in) {
			t.Fatalf("bare key leaked: %q", got)
		}
	}
}

func TestRedactSecrets_NoFalsePositiveOnPlainPath(t *testing.T) {
	// A plain error message with no credential substrings should
	// pass through unchanged.
	in := "open /tmp/foo: no such file or directory"
	if redactSecrets(in) != in {
		t.Fatalf("clean message altered: %q", redactSecrets(in))
	}
}

func TestBaseResultMarshalJSON_RedactsErrorReason(t *testing.T) {
	br := BaseResult{
		Operation:   "fetch",
		ErrorReason: "boom: OPENAI_API_KEY=sk-secret-1234567890abcdef in env",
	}
	b, err := json.Marshal(br)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if strings.Contains(got, "sk-secret-1234567890abcdef") {
		t.Fatalf("MarshalJSON leaked secret: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("no redaction in marshaled output: %s", got)
	}
}

func TestBaseResultErrorLine_RedactsReason(t *testing.T) {
	br := BaseResult{
		Operation:   "fetch",
		ErrorReason: "Authorization: Bearer ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA failed",
	}
	line := br.ErrorLine("https://api.example.com")
	if strings.Contains(line, "ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Fatalf("ErrorLine leaked: %s", line)
	}
	if !strings.Contains(line, "[REDACTED]") {
		t.Fatalf("ErrorLine missing redaction marker: %s", line)
	}
}
