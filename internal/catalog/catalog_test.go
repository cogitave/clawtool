package catalog

import (
	"strings"
	"testing"
)

func TestBuiltin_Parses(t *testing.T) {
	c, err := Builtin()
	if err != nil {
		t.Fatalf("Builtin() failed: %v", err)
	}
	if c.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", c.SchemaVersion)
	}
	if len(c.Entries) < 8 {
		t.Errorf("entries = %d, want >= 8 (small catalog ships at least github+slack+postgres+sqlite+filesystem+fetch+brave-search+memory)", len(c.Entries))
	}
}

func TestBuiltin_HasGithub(t *testing.T) {
	c, _ := Builtin()
	gh, ok := c.Lookup("github")
	if !ok {
		t.Fatal("github not in catalog")
	}
	if gh.Runtime != "npx" {
		t.Errorf("github runtime = %q, want npx", gh.Runtime)
	}
	if !strings.Contains(gh.Package, "github") {
		t.Errorf("github package = %q, want it to mention github", gh.Package)
	}
	wantEnv := []string{"GITHUB_TOKEN"}
	if len(gh.RequiredEnv) != len(wantEnv) || gh.RequiredEnv[0] != wantEnv[0] {
		t.Errorf("github required_env = %v, want %v", gh.RequiredEnv, wantEnv)
	}
	if gh.AuthHint == "" {
		t.Error("github auth_hint must be present")
	}
}

func TestBuiltin_HasMcpToolbox(t *testing.T) {
	c, _ := Builtin()
	e, ok := c.Lookup("mcp-toolbox")
	if !ok {
		t.Fatal("mcp-toolbox not in catalog")
	}
	if e.Description == "" {
		t.Error("mcp-toolbox description must be set")
	}
	if e.Homepage == "" {
		t.Error("mcp-toolbox homepage must be set")
	}
	if e.Runtime != "binary" {
		t.Errorf("mcp-toolbox runtime = %q, want binary", e.Runtime)
	}
	if e.Maintained != "google" {
		t.Errorf("mcp-toolbox maintained = %q, want google", e.Maintained)
	}
	if !strings.Contains(strings.ToLower(e.Description), "apache-2.0") {
		t.Error("mcp-toolbox description should record the Apache-2.0 license")
	}
}

func TestBuiltin_HasSemble(t *testing.T) {
	c, _ := Builtin()
	e, ok := c.Lookup("semble")
	if !ok {
		t.Fatal("semble not in catalog")
	}
	if e.Description == "" {
		t.Error("semble description must be set")
	}
	if e.Homepage == "" {
		t.Error("semble homepage must be set")
	}
	if e.Runtime != "binary" {
		t.Errorf("semble runtime = %q, want binary", e.Runtime)
	}
	if e.Maintained != "minishlab" {
		t.Errorf("semble maintained = %q, want minishlab", e.Maintained)
	}
	if !strings.Contains(strings.ToLower(e.Description), "mit") {
		t.Error("semble description should record the MIT license")
	}
	if e.Package != "uvx" {
		t.Errorf("semble package = %q, want uvx (operator-installed runner)", e.Package)
	}
	// uvx invocation must pass the [mcp] extras + the semble entrypoint.
	wantArgs := []string{"--from", "semble[mcp]", "semble"}
	if len(e.Args) != len(wantArgs) {
		t.Fatalf("semble args = %v, want %v", e.Args, wantArgs)
	}
	for i, w := range wantArgs {
		if e.Args[i] != w {
			t.Errorf("semble args[%d] = %q, want %q", i, e.Args[i], w)
		}
	}
}

func TestLookup_MissReturnsOkFalse(t *testing.T) {
	c, _ := Builtin()
	_, ok := c.Lookup("definitely-not-a-real-source")
	if ok {
		t.Error("expected ok=false for non-existent entry")
	}
}

func TestList_SortedByName(t *testing.T) {
	c, _ := Builtin()
	entries := c.List()
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Name > entries[i].Name {
			t.Errorf("entries not sorted: %q before %q", entries[i-1].Name, entries[i].Name)
		}
	}
}

func TestSuggestSimilar(t *testing.T) {
	c, _ := Builtin()
	t.Run("substring match", func(t *testing.T) {
		got := c.SuggestSimilar("git", 5)
		if len(got) == 0 {
			t.Error("expected suggestions for 'git', got none")
		}
		// Both 'github' and 'git' should match.
		var hasGithub bool
		for _, s := range got {
			if s == "github" {
				hasGithub = true
			}
		}
		if !hasGithub {
			t.Errorf("suggestions for 'git' must include github: got %v", got)
		}
	})
	t.Run("no input returns nothing", func(t *testing.T) {
		if got := c.SuggestSimilar("", 5); got != nil {
			t.Errorf("empty query returned %v, want nil", got)
		}
	})
	t.Run("zero limit returns nothing", func(t *testing.T) {
		if got := c.SuggestSimilar("git", 0); got != nil {
			t.Errorf("zero limit returned %v, want nil", got)
		}
	})
}

func TestToSourceCommand_PerRuntime(t *testing.T) {
	cases := []struct {
		name string
		e    Entry
		want []string
	}{
		{
			"npx", Entry{Runtime: "npx", Package: "@scope/pkg"},
			[]string{"npx", "-y", "@scope/pkg"},
		},
		{
			"npx with args", Entry{Runtime: "npx", Package: "@scope/pkg", Args: []string{"--root", "/tmp"}},
			[]string{"npx", "-y", "@scope/pkg", "--root", "/tmp"},
		},
		{
			"python via uvx", Entry{Runtime: "python", Package: "mcp-server-fetch"},
			[]string{"uvx", "mcp-server-fetch"},
		},
		{
			"docker", Entry{Runtime: "docker", Package: "ghcr.io/org/server:latest"},
			[]string{"docker", "run", "-i", "--rm", "ghcr.io/org/server:latest"},
		},
		{
			"binary", Entry{Runtime: "binary", Package: "my-mcp-server", Args: []string{"--port", "0"}},
			[]string{"my-mcp-server", "--port", "0"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.e.ToSourceCommand()
			if err != nil {
				t.Fatalf("ToSourceCommand: %v", err)
			}
			if !equalStr(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestToSourceCommand_UnknownRuntimeErrors(t *testing.T) {
	e := Entry{Runtime: "wasm-future-thing", Package: "x"}
	if _, err := e.ToSourceCommand(); err == nil {
		t.Error("expected error for unknown runtime")
	}
}

func TestEnvTemplate_BuildsVarReferences(t *testing.T) {
	e := Entry{RequiredEnv: []string{"GITHUB_TOKEN", "BRAVE_API_KEY"}}
	got := e.EnvTemplate()
	if got["GITHUB_TOKEN"] != "${GITHUB_TOKEN}" {
		t.Errorf("template[GITHUB_TOKEN] = %q, want ${GITHUB_TOKEN}", got["GITHUB_TOKEN"])
	}
	if got["BRAVE_API_KEY"] != "${BRAVE_API_KEY}" {
		t.Errorf("template[BRAVE_API_KEY] = %q", got["BRAVE_API_KEY"])
	}
}

func TestEnvTemplate_NoEnvReturnsNil(t *testing.T) {
	e := Entry{}
	if got := e.EnvTemplate(); got != nil {
		t.Errorf("empty required_env produced template %v, want nil", got)
	}
}

func equalStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
