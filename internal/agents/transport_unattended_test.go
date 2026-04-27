package agents

import (
	"strings"
	"testing"
)

// TestParseOptions_UnattendedRoundTrips: ADR-023 fix #201. send.go
// stuffs `opts["unattended"] = true` into the dispatch map; the
// transport must read it back via ParseOptions so the per-family
// elevation flag fires.
func TestParseOptions_UnattendedRoundTrips(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want bool
	}{
		{"unattended true", map[string]any{"unattended": true}, true},
		{"unattended false", map[string]any{"unattended": false}, false},
		{"absent", map[string]any{}, false},
		{"wrong type ignored", map[string]any{"unattended": "yes"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseOptions(tc.in).Unattended
			if got != tc.want {
				t.Errorf("Unattended = %v, want %v", got, tc.want)
			}
		})
	}
}

// argsBuildersForTest exposes the per-transport argv build to tests
// so we can assert the elevation flag fires without exec'ing real
// CLIs. Each builder mirrors what the production Send method does
// for a representative prompt + options pair.
//
// Keep these in lockstep with the per-transport Send methods. A
// regression here means ADR-023's elevation contract silently
// dropped on that family.
type transportArgs struct {
	name  string
	build func(prompt string, o SendOptions) []string
}

var argsBuildersForTest = []transportArgs{
	{"codex", func(prompt string, o SendOptions) []string {
		args := []string{"exec"}
		args = append(args, joinModel(o.Model, "--model")...)
		if o.SessionID != "" {
			args = []string{"exec", "resume", o.SessionID}
		}
		args = append(args, "--skip-git-repo-check", "--json")
		if o.Unattended {
			args = append(args, "--dangerously-bypass-approvals-and-sandbox")
		}
		args = append(args, o.ExtraArgs...)
		args = append(args, prompt)
		return args
	}},
	{"claude", func(prompt string, o SendOptions) []string {
		args := []string{"-p", prompt}
		args = append(args, joinModel(o.Model, "--model")...)
		if o.Format != "" {
			args = append(args, "--output-format", o.Format)
		}
		if o.Unattended {
			args = append(args, "--dangerously-skip-permissions")
		}
		args = append(args, o.ExtraArgs...)
		return args
	}},
	{"gemini", func(prompt string, o SendOptions) []string {
		args := []string{"-p", prompt, "--skip-trust"}
		args = append(args, joinModel(o.Model, "--model")...)
		args = append(args, "--output-format", "text")
		if o.Unattended {
			args = append(args, "--yolo")
		}
		args = append(args, o.ExtraArgs...)
		return args
	}},
	{"opencode", func(prompt string, o SendOptions) []string {
		args := []string{"run"}
		args = append(args, joinModel(o.Model, "--model")...)
		if o.Unattended {
			args = append(args, "--yolo")
		}
		args = append(args, o.ExtraArgs...)
		args = append(args, prompt)
		return args
	}},
	{"hermes", func(prompt string, o SendOptions) []string {
		args := []string{"chat", "-q", prompt}
		args = append(args, joinModel(o.Model, "--model")...)
		if o.Unattended {
			args = append(args, "--yolo")
		}
		args = append(args, o.ExtraArgs...)
		return args
	}},
}

func TestTransportArgs_UnattendedAddsElevationFlag(t *testing.T) {
	wantFlag := map[string]string{
		"codex":    "--dangerously-bypass-approvals-and-sandbox",
		"claude":   "--dangerously-skip-permissions",
		"gemini":   "--yolo",
		"opencode": "--yolo",
		"hermes":   "--yolo",
	}
	for _, tb := range argsBuildersForTest {
		t.Run(tb.name, func(t *testing.T) {
			args := tb.build("test prompt", SendOptions{Unattended: true})
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, wantFlag[tb.name]) {
				t.Errorf("%s: unattended args missing %q. got: %v", tb.name, wantFlag[tb.name], args)
			}
		})
	}
}

func TestTransportArgs_AttendedOmitsElevationFlag(t *testing.T) {
	dangerFlags := []string{
		"--dangerously-bypass-approvals-and-sandbox",
		"--dangerously-skip-permissions",
		"--yolo",
	}
	for _, tb := range argsBuildersForTest {
		t.Run(tb.name, func(t *testing.T) {
			args := tb.build("test prompt", SendOptions{Unattended: false})
			joined := strings.Join(args, " ")
			for _, f := range dangerFlags {
				if strings.Contains(joined, f) {
					t.Errorf("%s: attended args must not include %q. got: %v", tb.name, f, args)
				}
			}
		})
	}
}
