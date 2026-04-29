package secrets

import (
	"testing"
)

func TestShouldKeep_AlwaysKeep(t *testing.T) {
	for _, k := range []string{"PATH", "HOME", "USER", "LANG", "TERM", "TMPDIR", "XDG_CONFIG_HOME"} {
		if !shouldKeep(k, "/some/value", nil) {
			t.Errorf("process basic %q must always pass through", k)
		}
	}
}

func TestShouldKeep_HardBlocklistByName(t *testing.T) {
	for _, k := range []string{
		"GITHUB_TOKEN", "GH_TOKEN",
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY",
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"NPM_TOKEN", "REPLICATE_API_KEY",
	} {
		if shouldKeep(k, "anything", nil) {
			t.Errorf("hard-blocklisted %q must be stripped", k)
		}
	}
}

func TestShouldKeep_SecretSuffixPattern(t *testing.T) {
	for _, k := range []string{
		"MY_API_KEY", "ACME_TOKEN", "FOO_SECRET",
		"DB_PASSWORD", "ROOT_PWD",
	} {
		if shouldKeep(k, "v", nil) {
			t.Errorf("secret-suffix key %q must be stripped", k)
		}
	}
}

func TestShouldKeep_SecretValueLeak(t *testing.T) {
	// A benign-named env var (DEBUG_DUMP, MY_VAR) carrying a
	// known-shape token in its VALUE should still be stripped —
	// this is the leak the value-regex catches.
	cases := map[string]string{
		"DEBUG_DUMP": "phc_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA12345",
		"MY_VAR":     "ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"BENIGN":     "Bearer token=sk-AAAAAAAAAAAAAAAAAAAA1234567890",
		"OTHER":      "sk_live_AAAAAAAAAAAAAAAAAAAA",
	}
	for k, v := range cases {
		if shouldKeep(k, v, nil) {
			t.Errorf("value-pattern leak: key=%q value=%q should be stripped", k, v)
		}
	}
}

func TestShouldKeep_BenignPasses(t *testing.T) {
	for _, kv := range []struct{ k, v string }{
		{"CI", "true"},
		{"NODE_ENV", "production"},
		{"DOCKER_HOST", "tcp://localhost:2375"},
		{"GIT_AUTHOR_NAME", "Arda"},
		{"GOPATH", "/home/arda/go"},
	} {
		if !shouldKeep(kv.k, kv.v, nil) {
			t.Errorf("benign %s=%s should pass", kv.k, kv.v)
		}
	}
}

func TestShouldKeep_ExtraKeepEscapeHatch(t *testing.T) {
	keep := map[string]bool{"MY_API_KEY": true}
	if !shouldKeep("MY_API_KEY", "v", keep) {
		t.Error("CLAWTOOL_ENV_KEEP escape hatch must override the suffix block")
	}
	// But hard-blocklisted names still resolve to keep when in
	// the operator's keep set — operator opt-in is the higher
	// authority. Document this in the comment, not enforced as
	// a constraint here.
	keep2 := map[string]bool{"GITHUB_TOKEN": true}
	if !shouldKeep("GITHUB_TOKEN", "ghp_x", keep2) {
		t.Errorf("explicit keep should override the hard-blocklist (operator opt-in)")
	}
}

func TestScrubEnv_StripsSecretsFromInput(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"HOME=/home/u",
		"GITHUB_TOKEN=ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"OPENAI_API_KEY=sk-xxxxxxxxxxxxxxxxxxxxxxxx",
		"DB_PASSWORD=hunter2",
		"CI=true",
	}
	got := ScrubEnv(in)
	gotMap := map[string]string{}
	for _, kv := range got {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				gotMap[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	for _, want := range []string{"PATH", "HOME", "CI"} {
		if _, ok := gotMap[want]; !ok {
			t.Errorf("expected %q to survive scrubbing", want)
		}
	}
	for _, gone := range []string{"GITHUB_TOKEN", "OPENAI_API_KEY", "DB_PASSWORD"} {
		if _, ok := gotMap[gone]; ok {
			t.Errorf("expected %q to be stripped, got value: %q", gone, gotMap[gone])
		}
	}
}

func TestScrubEnv_KeepSecretsOptOut(t *testing.T) {
	t.Setenv("CLAWTOOL_KEEP_SECRETS", "1")
	in := []string{"GITHUB_TOKEN=ghp_x", "PATH=/usr/bin"}
	got := ScrubEnv(in)
	if len(got) != 2 {
		t.Fatalf("opt-out should pass everything through, got %d entries", len(got))
	}
}

func TestScrubEnv_EnvKeepEscapeHatch(t *testing.T) {
	t.Setenv("CLAWTOOL_ENV_KEEP", "OPENAI_API_KEY,MY_TOKEN")
	in := []string{
		"OPENAI_API_KEY=sk-x",
		"MY_TOKEN=abc",
		"OTHER_TOKEN=should_strip",
		"PATH=/usr/bin",
	}
	got := ScrubEnv(in)
	gotKeys := map[string]bool{}
	for _, kv := range got {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				gotKeys[kv[:i]] = true
				break
			}
		}
	}
	for _, want := range []string{"OPENAI_API_KEY", "MY_TOKEN", "PATH"} {
		if !gotKeys[want] {
			t.Errorf("expected %q to survive (in CLAWTOOL_ENV_KEEP)", want)
		}
	}
	if gotKeys["OTHER_TOKEN"] {
		t.Errorf("OTHER_TOKEN should still be stripped (not in keep list)")
	}
}

func TestParseKeepList_Edges(t *testing.T) {
	cases := map[string]map[string]bool{
		"":            nil,
		"FOO":         {"FOO": true},
		"FOO,BAR":     {"FOO": true, "BAR": true},
		" FOO , BAR ": {"FOO": true, "BAR": true},
		"FOO,,BAR,":   {"FOO": true, "BAR": true},
	}
	for in, want := range cases {
		got := parseKeepList(in)
		if len(got) != len(want) {
			t.Errorf("parseKeepList(%q) len = %d, want %d (%v)", in, len(got), len(want), got)
			continue
		}
		for k := range want {
			if !got[k] {
				t.Errorf("parseKeepList(%q) missing %q", in, k)
			}
		}
	}
}
