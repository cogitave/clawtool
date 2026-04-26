package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSetGetDeleteRoundTrip(t *testing.T) {
	s := &Store{Scopes: map[string]map[string]string{}}
	s.Set("github", "GITHUB_TOKEN", "ghp_abc")
	s.Set("github-work", "GITHUB_TOKEN", "ghp_xyz")
	s.Set("", "BRAVE_API_KEY", "brave_global") // scope "" -> global

	if v, ok := s.Get("github", "GITHUB_TOKEN"); !ok || v != "ghp_abc" {
		t.Errorf("github GITHUB_TOKEN = %q ok=%v, want ghp_abc/true", v, ok)
	}
	if v, ok := s.Get("github-work", "GITHUB_TOKEN"); !ok || v != "ghp_xyz" {
		t.Errorf("github-work GITHUB_TOKEN = %q ok=%v, want ghp_xyz/true", v, ok)
	}
	if v, ok := s.Get("github", "BRAVE_API_KEY"); !ok || v != "brave_global" {
		t.Errorf("github BRAVE_API_KEY should fall back to global: got %q ok=%v", v, ok)
	}

	s.Delete("github", "GITHUB_TOKEN")
	if _, ok := s.Get("github", "GITHUB_TOKEN"); ok {
		t.Error("after delete, github GITHUB_TOKEN must not be present at scope")
	}
	// global value still present:
	if v, ok := s.Get("github", "BRAVE_API_KEY"); !ok || v != "brave_global" {
		t.Errorf("global BRAVE_API_KEY lost after unrelated delete: %q ok=%v", v, ok)
	}
}

func TestSaveLoadRoundTrip_FileMode0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.toml")

	s := &Store{Scopes: map[string]map[string]string{}}
	s.Set("github-personal", "GITHUB_TOKEN", "ghp_personal")
	s.Set("github-work", "GITHUB_TOKEN", "ghp_work")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		mode := info.Mode().Perm()
		if mode != 0o600 {
			t.Errorf("file mode = %o, want 0600", mode)
		}
	}

	loaded, err := LoadOrEmpty(path)
	if err != nil {
		t.Fatalf("LoadOrEmpty: %v", err)
	}
	if v, _ := loaded.Get("github-personal", "GITHUB_TOKEN"); v != "ghp_personal" {
		t.Errorf("personal token lost: %q", v)
	}
	if v, _ := loaded.Get("github-work", "GITHUB_TOKEN"); v != "ghp_work" {
		t.Errorf("work token lost: %q", v)
	}
}

func TestLoadOrEmpty_MissingFileReturnsEmptyStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-such-secrets.toml")
	s, err := LoadOrEmpty(path)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if s == nil || s.Scopes == nil {
		t.Errorf("Scopes must be non-nil even when empty")
	}
}

func TestResolve_FillsTemplate(t *testing.T) {
	s := &Store{Scopes: map[string]map[string]string{}}
	s.Set("github", "GITHUB_TOKEN", "ghp_secret")

	template := map[string]string{
		"GITHUB_TOKEN": "${GITHUB_TOKEN}",
	}
	got, missing := s.Resolve("github", template)
	if got["GITHUB_TOKEN"] != "ghp_secret" {
		t.Errorf("resolved GITHUB_TOKEN = %q, want ghp_secret", got["GITHUB_TOKEN"])
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want empty", missing)
	}
}

func TestResolve_FallsThroughToProcessEnv(t *testing.T) {
	t.Setenv("CLAWTOOL_TEST_FALLTHROUGH", "from-env")
	s := &Store{Scopes: map[string]map[string]string{}}

	template := map[string]string{"X": "${CLAWTOOL_TEST_FALLTHROUGH}"}
	got, missing := s.Resolve("github", template)
	if got["X"] != "from-env" {
		t.Errorf("resolved X = %q, want from-env (process env fallback)", got["X"])
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want empty when env supplies it", missing)
	}
}

func TestResolve_ReportsMissingKeys(t *testing.T) {
	s := &Store{Scopes: map[string]map[string]string{}}
	template := map[string]string{
		"PRESENT":     "literal-value",
		"INTERPOLATE": "${NO_SUCH_VAR_HERE_E2025}",
	}
	got, missing := s.Resolve("github", template)
	if got["PRESENT"] != "literal-value" {
		t.Errorf("literal value transformed: got %q", got["PRESENT"])
	}
	if len(missing) != 1 || missing[0] != "INTERPOLATE" {
		t.Errorf("missing = %v, want [INTERPOLATE]", missing)
	}
}

func TestResolve_NilTemplateReturnsNil(t *testing.T) {
	s := &Store{}
	got, missing := s.Resolve("any", nil)
	if got != nil || missing != nil {
		t.Errorf("nil template produced %v / %v, want nil/nil", got, missing)
	}
}

func TestExpand_LiteralPassthrough(t *testing.T) {
	s := &Store{Scopes: map[string]map[string]string{}}
	got, missing := s.Expand("scope", "no-template-here")
	if got != "no-template-here" {
		t.Errorf("literal mutated: %q", got)
	}
	if len(missing) != 0 {
		t.Errorf("missing should be empty for literal: %v", missing)
	}
}

func TestExpand_SubstitutesScopeThenEnv(t *testing.T) {
	t.Setenv("CLAWTOOL_TEST_FROM_ENV", "from-env")

	s := &Store{Scopes: map[string]map[string]string{}}
	s.Set("github", "GITHUB_TOKEN", "secret")

	cases := []struct {
		in   string
		want string
	}{
		{"${GITHUB_TOKEN}", "secret"},
		{"prefix-${GITHUB_TOKEN}-suffix", "prefix-secret-suffix"},
		{"${CLAWTOOL_TEST_FROM_ENV}", "from-env"},
		{"two ${GITHUB_TOKEN} ${GITHUB_TOKEN}", "two secret secret"},
	}
	for _, c := range cases {
		got, missing := s.Expand("github", c.in)
		if got != c.want {
			t.Errorf("Expand(%q) = %q, want %q", c.in, got, c.want)
		}
		if len(missing) != 0 {
			t.Errorf("Expand(%q) reported missing %v", c.in, missing)
		}
	}
}

func TestExpand_ReportsMissingDeduplicated(t *testing.T) {
	s := &Store{Scopes: map[string]map[string]string{}}
	t.Setenv("DEFINITELY_NOT_SET_ANYWHERE_ALPHA", "")
	t.Setenv("DEFINITELY_NOT_SET_ANYWHERE_BETA", "")

	out, missing := s.Expand("scope", "${DEFINITELY_NOT_SET_ANYWHERE_ALPHA} and ${DEFINITELY_NOT_SET_ANYWHERE_ALPHA} and ${DEFINITELY_NOT_SET_ANYWHERE_BETA}")
	if out != " and  and " {
		t.Errorf("expected unresolved refs to collapse to empty: %q", out)
	}
	if len(missing) != 2 {
		t.Errorf("missing = %v, want 2 unique entries (alpha, beta)", missing)
	}
}
