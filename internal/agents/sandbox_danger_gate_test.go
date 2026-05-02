package agents

import (
	"errors"
	"strings"
	"testing"
)

// TestSandbox_DangerProfileRequiresUnsafeYes — ADR-020 §Resolved.
// A dispatch resolving to the danger-full-access profile WITHOUT
// the operator's --unsafe-yes confirmation must be refused, with
// a directive stderr-shaped error.
func TestSandbox_DangerProfileRequiresUnsafeYes(t *testing.T) {
	cases := []struct {
		name  string
		opts  map[string]any
		agent Agent
	}{
		{
			name:  "per-call --sandbox=danger-full-access",
			opts:  map[string]any{"sandbox": "danger-full-access"},
			agent: Agent{Instance: "claude"},
		},
		{
			name:  "agent-config sandbox=danger-full-access",
			opts:  map[string]any{},
			agent: Agent{Instance: "codex", Sandbox: "danger-full-access"},
		},
		{
			name:  "case-insensitive — Danger-Full-Access",
			opts:  map[string]any{"sandbox": "Danger-Full-Access"},
			agent: Agent{Instance: "claude"},
		},
		{
			name: "unsafe_yes=false explicitly",
			opts: map[string]any{
				"sandbox":    "danger-full-access",
				"unsafe_yes": false,
			},
			agent: Agent{Instance: "claude"},
		},
		{
			name: "unsafe_yes string=\"false\"",
			opts: map[string]any{
				"sandbox":    "danger-full-access",
				"unsafe_yes": "false",
			},
			agent: Agent{Instance: "claude"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDangerSandboxGate(tc.opts, tc.agent)
			if err == nil {
				t.Fatalf("expected refusal, got nil")
			}
			if !errors.Is(err, ErrDangerSandboxRequiresUnsafeYes) {
				t.Errorf("error must wrap ErrDangerSandboxRequiresUnsafeYes; got %v", err)
			}
			// Stderr-shaped messaging — operator must see both
			// the flag name and the explanation.
			msg := err.Error()
			if !strings.Contains(msg, "--unsafe-yes") {
				t.Errorf("error message must name the --unsafe-yes flag; got %q", msg)
			}
			if !strings.Contains(msg, "danger-full-access") {
				t.Errorf("error message must name the profile; got %q", msg)
			}
			if !strings.Contains(msg, "bypasses all sandbox restrictions") {
				t.Errorf("error message must explain the implication; got %q", msg)
			}
		})
	}
}

// TestSandbox_DangerWithUnsafeYesAllowed — gate releases when the
// confirmation flag is present in any of its accepted shapes (typed
// bool, string "true" / "1" / "yes").
func TestSandbox_DangerWithUnsafeYesAllowed(t *testing.T) {
	cases := []struct {
		name string
		opts map[string]any
	}{
		{
			name: "typed bool true",
			opts: map[string]any{
				"sandbox":    "danger-full-access",
				"unsafe_yes": true,
			},
		},
		{
			name: "string \"true\"",
			opts: map[string]any{
				"sandbox":    "danger-full-access",
				"unsafe_yes": "true",
			},
		},
		{
			name: "string \"1\"",
			opts: map[string]any{
				"sandbox":    "danger-full-access",
				"unsafe_yes": "1",
			},
		},
		{
			name: "string \"yes\" — case insensitive",
			opts: map[string]any{
				"sandbox":    "danger-full-access",
				"unsafe_yes": "YES",
			},
		},
		{
			name: "agent-config danger + opts unsafe_yes=true",
			opts: map[string]any{"unsafe_yes": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := Agent{Instance: "claude"}
			if _, hasSandbox := tc.opts["sandbox"]; !hasSandbox {
				agent.Sandbox = "danger-full-access"
			}
			if err := checkDangerSandboxGate(tc.opts, agent); err != nil {
				t.Errorf("dispatch must be allowed when unsafe_yes is set; got %v", err)
			}
		})
	}
}

// TestSandbox_OtherProfilesUnaffected — non-danger sandbox names
// (and the no-sandbox case) must NOT need --unsafe-yes. The gate
// only fires for the reserved profile name.
func TestSandbox_OtherProfilesUnaffected(t *testing.T) {
	cases := []struct {
		name  string
		opts  map[string]any
		agent Agent
	}{
		{
			name:  "no sandbox at all",
			opts:  map[string]any{},
			agent: Agent{Instance: "claude"},
		},
		{
			name:  "per-call strict",
			opts:  map[string]any{"sandbox": "strict"},
			agent: Agent{Instance: "claude"},
		},
		{
			name:  "per-call lenient",
			opts:  map[string]any{"sandbox": "lenient"},
			agent: Agent{Instance: "claude"},
		},
		{
			name:  "agent-config unrelated profile",
			opts:  map[string]any{},
			agent: Agent{Instance: "codex", Sandbox: "ci-readonly"},
		},
		{
			name:  "near-miss name not matched as danger",
			opts:  map[string]any{"sandbox": "danger-readonly"},
			agent: Agent{Instance: "claude"},
		},
		{
			name:  "empty string sandbox",
			opts:  map[string]any{"sandbox": ""},
			agent: Agent{Instance: "claude"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkDangerSandboxGate(tc.opts, tc.agent); err != nil {
				t.Errorf("non-danger profile must pass without --unsafe-yes; got %v", err)
			}
		})
	}
}

// resolvedSandboxName precedence — per-call opts override
// agent-config when both are present.
func TestResolvedSandboxName_PerCallOverridesAgentConfig(t *testing.T) {
	got := resolvedSandboxName(
		map[string]any{"sandbox": "lenient"},
		Agent{Sandbox: "danger-full-access"},
	)
	if got != "lenient" {
		t.Errorf("per-call opts must win; got %q", got)
	}
}

// unsafeYesFromOpts — defensive decoding of the confirmation flag.
func TestUnsafeYesFromOpts(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want bool
	}{
		{"missing", nil, false},
		{"bool true", true, true},
		{"bool false", false, false},
		{"string true", "true", true},
		{"string TRUE", "TRUE", true},
		{"string 1", "1", true},
		{"string yes", "yes", true},
		{"string on", "on", true},
		{"string false", "false", false},
		{"string 0", "0", false},
		{"string empty", "", false},
		{"int 1 — not a recognised shape", 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := map[string]any{}
			if tc.v != nil {
				opts["unsafe_yes"] = tc.v
			}
			if got := unsafeYesFromOpts(opts); got != tc.want {
				t.Errorf("unsafeYesFromOpts(%v) = %v, want %v", tc.v, got, tc.want)
			}
		})
	}
	// nil opts → false, never panics.
	if unsafeYesFromOpts(nil) {
		t.Error("nil opts must return false, not true")
	}
}
