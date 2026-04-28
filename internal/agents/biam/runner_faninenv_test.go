package biam

import "testing"

func TestInjectFanInEnv_AddsKeysWhenMissing(t *testing.T) {
	opts := injectFanInEnv(nil, "task-123", "codex")
	env := opts["env"].(map[string]string)
	if env["CLAWTOOL_TASK_ID"] != "task-123" {
		t.Errorf("CLAWTOOL_TASK_ID = %q, want task-123", env["CLAWTOOL_TASK_ID"])
	}
	if env["CLAWTOOL_FROM_INSTANCE"] != "codex" {
		t.Errorf("CLAWTOOL_FROM_INSTANCE = %q, want codex", env["CLAWTOOL_FROM_INSTANCE"])
	}
}

func TestInjectFanInEnv_RespectsExisting(t *testing.T) {
	opts := map[string]any{
		"env": map[string]string{
			"CLAWTOOL_TASK_ID":       "operator-override",
			"CLAWTOOL_FROM_INSTANCE": "operator-set",
			"OTHER_VAR":              "stay-put",
		},
	}
	out := injectFanInEnv(opts, "task-123", "codex")
	env := out["env"].(map[string]string)
	if env["CLAWTOOL_TASK_ID"] != "operator-override" {
		t.Errorf("CLAWTOOL_TASK_ID overridden; want operator-override")
	}
	if env["CLAWTOOL_FROM_INSTANCE"] != "operator-set" {
		t.Errorf("CLAWTOOL_FROM_INSTANCE overridden; want operator-set")
	}
	if env["OTHER_VAR"] != "stay-put" {
		t.Errorf("OTHER_VAR clobbered; want stay-put")
	}
}

func TestInjectFanInEnv_PreservesNonEnvOpts(t *testing.T) {
	opts := map[string]any{"session_id": "s-1", "model": "m-x"}
	out := injectFanInEnv(opts, "task-1", "claude")
	if out["session_id"] != "s-1" {
		t.Errorf("session_id lost during injection")
	}
	if out["model"] != "m-x" {
		t.Errorf("model lost during injection")
	}
	env, ok := out["env"].(map[string]string)
	if !ok {
		t.Fatalf("env map missing after injection")
	}
	if env["CLAWTOOL_TASK_ID"] != "task-1" {
		t.Errorf("CLAWTOOL_TASK_ID not set")
	}
}

func TestInjectFanInEnv_SkipsEmptyValues(t *testing.T) {
	out := injectFanInEnv(nil, "", "")
	env, ok := out["env"].(map[string]string)
	if !ok {
		t.Fatalf("env map missing")
	}
	if _, has := env["CLAWTOOL_TASK_ID"]; has {
		t.Errorf("CLAWTOOL_TASK_ID set despite empty taskID")
	}
	if _, has := env["CLAWTOOL_FROM_INSTANCE"]; has {
		t.Errorf("CLAWTOOL_FROM_INSTANCE set despite empty instance")
	}
}
