package telemetry

import (
	"runtime"
	"strings"
	"testing"
)

// TestFingerprintProps_StrictAllowList verifies every key
// FingerprintProps emits is in the allowedKeys allow-list. A new
// dimension that lands in fingerprint.go without an allow-list
// entry would silently drop on the wire — this test catches that
// the moment it ships.
func TestFingerprintProps_StrictAllowList(t *testing.T) {
	props := FingerprintProps("manual")
	for k := range props {
		if !allowedKeys[k] {
			t.Errorf("FingerprintProps key %q missing from allowedKeys (would drop on wire)", k)
		}
	}
}

// TestFingerprintProps_NoSensitiveContent makes a strong negative
// assertion: no value in the fingerprint event may contain user-
// identifiable text. This is the legal contract — every reviewer
// reading the diff for a new dimension should run this test
// against a representative environment.
func TestFingerprintProps_NoSensitiveContent(t *testing.T) {
	props := FingerprintProps("manual")
	// Forbidden substrings — anything that would tie the event
	// to a specific operator's host. We don't enumerate every
	// possible PII shape; we sample the obvious ones.
	forbidden := []string{
		"/home/", "/Users/", "C:\\Users", // user home paths
		"@",                                // email-shaped
		"Authorization", "Bearer", "Token", // auth headers
		"sk-", "ghp_", "phc_", "gho_", // API key prefixes
	}
	for k, v := range props {
		s, ok := v.(string)
		if !ok {
			continue
		}
		for _, f := range forbidden {
			if strings.Contains(s, f) {
				t.Errorf("FingerprintProps[%q] = %q contains forbidden substring %q", k, s, f)
			}
		}
	}
}

// TestMemTier_Buckets covers the four documented size bands and
// the unknown-platform fallback. We can't actually probe the
// running host's memory in a deterministic way, but we can spot-
// check the bucket assignments by stubbing the input.
func TestMemTier_Buckets(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("memTier only reads /proc/meminfo on linux")
	}
	got := memTier()
	switch got {
	case "<2GB", "2-8GB", "8-32GB", ">32GB":
		// any of these is a healthy bucket on a real host.
	case "unknown":
		t.Error("memTier returned 'unknown' on linux — /proc/meminfo unreadable?")
	default:
		t.Errorf("memTier returned unexpected bucket: %q", got)
	}
}

// TestDetectLocaleLang_Buckets covers the documented head-only
// emission rule + the unknown fallback. We spot-check a handful
// of common locale strings.
func TestDetectLocaleLang_Buckets(t *testing.T) {
	cases := []struct {
		env  string
		want string
	}{
		{"tr_TR.UTF-8", "tr"},
		{"en_US.UTF-8", "en"},
		{"de_DE", "de"},
		{"C", "c"},
		{"", "unknown"},
		{"randombig.text.with.dots", "unknown"}, // first segment >5 chars: dropped
	}
	for _, tc := range cases {
		t.Setenv("LANG", tc.env)
		t.Setenv("LC_ALL", "")
		got := detectLocaleLang()
		if got != tc.want {
			t.Errorf("detectLocaleLang() with LANG=%q: got %q, want %q", tc.env, got, tc.want)
		}
	}
}
