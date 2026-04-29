package secrets

import (
	"os"
	"regexp"
	"strings"
)

// ScrubEnv returns a copy of the parent environment with
// secrets-shaped variables removed. Used at the boundary where
// clawtool spawns subprocesses (Bash tool, BIAM dispatch, agent
// transport) — without this, the parent's GITHUB_TOKEN /
// OPENAI_API_KEY / similar would silently flow into every
// child process and leak via misbehaving tools, log lines, or
// rogue scripts.
//
// Octopus pattern (mcp-server/src/index.ts:107): err on the side
// of over-scrubbing; the operator can opt out per-spawn via
// `CLAWTOOL_KEEP_SECRETS=1` when they actually need a token in
// the child (rare — a tool that genuinely needs OPENAI_API_KEY
// should ask the user via a documented flag, not pick it up
// implicitly from ambient env).
//
// Variables stripped:
//   - keys ending in _KEY / _TOKEN / _SECRET / _PASSWORD / _PWD
//   - the OAuth / API-key prefix family used in core/redact.go
//     (anywhere in the value): ghp_/ghs_/gho_/sk-/phc_/...
//   - exact-match list of known sensitive vars (GITHUB_TOKEN,
//     OPENAI_API_KEY, ANTHROPIC_API_KEY, AWS_*, etc.)
//
// Variables ALWAYS preserved (process basics):
//   - PATH, HOME, USER, LOGNAME, SHELL, PWD
//   - LANG, LC_*, TZ, TERM, COLORTERM, NO_COLOR
//   - TMPDIR / TEMP / TMP
//   - XDG_CONFIG_HOME / XDG_DATA_HOME / XDG_STATE_HOME / XDG_CACHE_HOME
//   - HTTP_PROXY / HTTPS_PROXY / NO_PROXY (network plumbing)
//
// Anything else (CI=true, GIT_*, DOCKER_*, application-specific
// env from the parent shell) passes through if it doesn't match
// the secret-suffix patterns. The principle: a key ending in
// _TOKEN is a secret regardless of its prefix; everything else
// is presumed safe unless its name explicitly says otherwise.

var secretSuffixRe = regexp.MustCompile(`(?i)_(KEY|TOKEN|SECRET|PASSWORD|PWD)$`)

// secretValueRe checks the VALUE of an env var for the same
// prefix family core/redact.go scrubs in error strings. A key
// named DEBUG_DUMP=ghp_xxxxxxxx... shouldn't slip through just
// because the key name doesn't end in _TOKEN.
var secretValueRe = regexp.MustCompile(`\b(phc_[A-Za-z0-9]{32,}|sk-[A-Za-z0-9_-]{20,}|ghp_[A-Za-z0-9]{30,}|ghs_[A-Za-z0-9]{30,}|gho_[A-Za-z0-9]{30,}|rk_[A-Za-z0-9]{20,}|sk_live_[A-Za-z0-9]{20,}|sk_test_[A-Za-z0-9]{20,})\b`)

// alwaysKeep is the explicit allow-list of process-basics. Even
// if a name in this set somehow matches the suffix regex (it
// shouldn't), we preserve it.
var alwaysKeep = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true,
	"SHELL": true, "PWD": true, "OLDPWD": true,
	"LANG": true, "LANGUAGE": true, "TZ": true,
	"TERM": true, "COLORTERM": true, "NO_COLOR": true,
	"TMPDIR": true, "TEMP": true, "TMP": true,
	"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
	"XDG_STATE_HOME": true, "XDG_CACHE_HOME": true,
	"XDG_RUNTIME_DIR": true,
	"HTTP_PROXY":      true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true,
}

// hardBlocklist is exact-match for known-sensitive vars whose
// names don't match the suffix regex (e.g. AWS_ACCESS_KEY_ID,
// where the suffix is _ID not _KEY). Add here when a leak surfaces.
var hardBlocklist = map[string]bool{
	"GITHUB_TOKEN": true, "GH_TOKEN": true,
	"OPENAI_API_KEY":    true,
	"ANTHROPIC_API_KEY": true,
	"GOOGLE_API_KEY":    true, "GEMINI_API_KEY": true,
	"AWS_ACCESS_KEY_ID":     true,
	"AWS_SECRET_ACCESS_KEY": true,
	"AWS_SESSION_TOKEN":     true,
	"NPM_TOKEN":             true, "PYPI_TOKEN": true,
	"DOCKERHUB_TOKEN":   true,
	"CLAUDE_API_KEY":    true,
	"DEEPSEEK_API_KEY":  true,
	"GROQ_API_KEY":      true,
	"MISTRAL_API_KEY":   true,
	"COHERE_API_KEY":    true,
	"PERPLEXITY_TOKEN":  true,
	"REPLICATE_API_KEY": true,
}

// keepEscapeHatch lets the operator force-include a variable
// even when it would otherwise be stripped. Comma-separated key
// names in CLAWTOOL_ENV_KEEP. Useful when a specific tool legit-
// imately needs OPENAI_API_KEY in the child env and the user
// has accepted the risk.
const keepEscapeHatch = "CLAWTOOL_ENV_KEEP"

// ScrubEnv returns a fresh slice safe to assign to cmd.Env.
// Pass os.Environ() (or any []string of "K=V" entries). The
// input slice is NOT mutated.
//
// When CLAWTOOL_KEEP_SECRETS=1 is set on the parent process,
// the function passes the env through unchanged — explicit
// opt-out for the rare cases where the operator wants the
// pre-octopus behaviour. The opt-out is logged once on stderr
// when the package is first imported... actually, it's a
// per-call decision, so no logging here; the caller can warn
// if they want that visible.
func ScrubEnv(parent []string) []string {
	if os.Getenv("CLAWTOOL_KEEP_SECRETS") == "1" {
		out := make([]string, len(parent))
		copy(out, parent)
		return out
	}
	keepExtra := parseKeepList(os.Getenv(keepEscapeHatch))
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			out = append(out, kv)
			continue
		}
		key := kv[:i]
		val := kv[i+1:]
		if shouldKeep(key, val, keepExtra) {
			out = append(out, kv)
		}
	}
	return out
}

// shouldKeep is the core decision: does a (key, value) pass
// through to the child? Pure function, easy to unit-test.
func shouldKeep(key, val string, keepExtra map[string]bool) bool {
	if alwaysKeep[key] {
		return true
	}
	if keepExtra[key] {
		return true
	}
	if hardBlocklist[key] {
		return false
	}
	if secretSuffixRe.MatchString(key) {
		return false
	}
	if val != "" && secretValueRe.MatchString(val) {
		return false
	}
	return true
}

func parseKeepList(s string) map[string]bool {
	if s == "" {
		return nil
	}
	out := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		k := strings.TrimSpace(part)
		if k != "" {
			out[k] = true
		}
	}
	return out
}
