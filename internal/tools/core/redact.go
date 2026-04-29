// Package core — secret redaction for tool result envelopes
// (octopus pattern, mcp-server/src/index.ts:107). Every error
// envelope clawtool returns to a peer agent or surfaces in
// stderr/stdout passes through redactSecrets first, so a tool
// that wraps an upstream error message containing
// `Authorization: Bearer ghp_…` or `OPENAI_API_KEY=sk-…` doesn't
// re-export the credential to whoever asked.
//
// The patterns deliberately err on the side of over-redacting:
// false positives (a value that LOOKS like a key but isn't) get
// replaced with [REDACTED]; the operator can re-investigate by
// re-running with `clawtool serve --debug` and reading the
// daemon log directly. False negatives (a real secret leaking
// through) are the unacceptable failure mode.
package core

import (
	"regexp"
)

// redactPatterns is the ordered set of regex → replacement
// rules. Each pattern is anchored to a recognisable prefix
// (KEY=, TOKEN=, Authorization:, password=, cookie:) so we
// don't aggressively redact every long alphanum string.
//
// Add a new pattern here, NOT inline in some tool's error path.
// Centralising the list means a future blind-spot fix lands once
// and protects every existing + future caller.
// Each pattern follows the same shape: group 1 captures a
// recognisable PREFIX that's safe to keep visible (so the operator
// sees WHAT kind of secret was masked), and the rest of the match
// is the credential body. ReplaceAllString rewrites the match as
// `${1}[REDACTED]`. Group 1 must therefore include any trailing
// punctuation (`=`, `: `) that should survive in the output.
var redactPatterns = []*regexp.Regexp{
	// VAR=value style: API_KEY=…, OPENAI_API_KEY=…, GH_TOKEN=…,
	// any uppercase ID ending in _KEY / _TOKEN / _SECRET / _PASSWORD.
	// Group 1 includes the trailing `=` so the substitution keeps it.
	regexp.MustCompile(`([A-Z][A-Z0-9_]*(?:_KEY|_TOKEN|_SECRET|_PASSWORD|_PWD)=)[^\s"']+`),
	// Authorization: Bearer <token>
	regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)[^\s"']+`),
	// Authorization: <other-scheme> <token>
	regexp.MustCompile(`(?i)(Authorization:\s*\w+\s+)[^\s"']+`),
	// PostHog / Anthropic / OpenAI / GitHub / Stripe key prefixes.
	// Group 1 is the literal prefix; the variable suffix is the
	// secret body and gets replaced by [REDACTED].
	regexp.MustCompile(`\b(phc_)[a-zA-Z0-9]{32,}\b`),  // posthog
	regexp.MustCompile(`\b(sk-)[a-zA-Z0-9_-]{20,}\b`), // openai-style
	regexp.MustCompile(`\b(ghp_)[a-zA-Z0-9]{30,}\b`),  // github personal
	regexp.MustCompile(`\b(ghs_)[a-zA-Z0-9]{30,}\b`),  // github server
	regexp.MustCompile(`\b(gho_)[a-zA-Z0-9]{30,}\b`),  // github oauth
	regexp.MustCompile(`\b(rk_)[a-zA-Z0-9]{20,}\b`),   // stripe restricted
	regexp.MustCompile(`\b(sk_live_)[a-zA-Z0-9]{20,}\b`),
	regexp.MustCompile(`\b(sk_test_)[a-zA-Z0-9]{20,}\b`),
	// cookie: name=value style — strip the value, keep the name+`=`.
	regexp.MustCompile(`(?i)(cookie:\s*[^=;]+=)[^;\s"']+`),
}

// redactSecrets walks `s` through every pattern in
// redactPatterns and replaces the credential portion with
// `<prefix>[REDACTED]`. The prefix is preserved (e.g.
// "Authorization: Bearer [REDACTED]") so the operator can still
// see WHAT kind of secret was masked and where it came from
// without seeing the value itself.
func redactSecrets(s string) string {
	if s == "" {
		return s
	}
	for _, re := range redactPatterns {
		s = re.ReplaceAllString(s, "${1}[REDACTED]")
	}
	return s
}
