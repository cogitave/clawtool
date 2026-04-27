package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerify_DetectsGoModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plans, err := pickRunners(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].name != "go test ./..." {
		t.Errorf("expected go runner; got %+v", plans)
	}
}

func TestVerify_DetectsPnpmAheadOfNpm(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"test":"echo ok"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfileVersion: 9\n"), 0o644)
	plans, err := pickRunners(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].name != "pnpm test" {
		t.Errorf("expected pnpm winner; got %+v", plans)
	}
}

func TestVerify_TargetOverride(t *testing.T) {
	dir := t.TempDir()
	// No detect-files, but explicit target should still resolve.
	plans, err := pickRunners(dir, "pytest")
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].name != "pytest" {
		t.Errorf("explicit target=pytest should win: %+v", plans)
	}
}

func TestVerify_UnknownTargetErrors(t *testing.T) {
	_, err := pickRunners(t.TempDir(), "ghost-runner")
	if err == nil {
		t.Error("unknown target should error")
	}
}

func TestVerify_NoRunnerDetected(t *testing.T) {
	dir := t.TempDir()
	res := executeVerify(context.Background(), dir, "", 5*time.Second)
	if res.Overall != "fail" {
		t.Errorf("no runner should mark overall=fail; got %q", res.Overall)
	}
	if len(res.Checks) != 1 || res.Checks[0].Status != "skipped" {
		t.Errorf("expected one skipped detect check; got %+v", res.Checks)
	}
}

func TestVerify_HappyPath(t *testing.T) {
	// Build a tiny Go module that passes.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module verifytest\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte("package verifytest\nimport \"testing\"\nfunc TestX(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := executeVerify(context.Background(), dir, "", 60*time.Second)
	if res.Overall != "pass" {
		t.Errorf("expected pass; got %q (checks: %+v)", res.Overall, res.Checks)
	}
	if len(res.Checks) != 1 || res.Checks[0].Status != "pass" {
		t.Errorf("expected single passing check; got %+v", res.Checks)
	}
}

func TestVerify_FailingTestSurfaces(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module verifytest\n\ngo 1.25\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "x_test.go"),
		[]byte("package verifytest\nimport \"testing\"\nfunc TestX(t *testing.T) { t.Fatal(\"boom\") }\n"),
		0o644)
	res := executeVerify(context.Background(), dir, "", 60*time.Second)
	if res.Overall != "fail" {
		t.Errorf("failing test should mark overall=fail; got %q", res.Overall)
	}
	if len(res.Checks) != 1 || res.Checks[0].Status != "fail" {
		t.Errorf("expected fail check; got %+v", res.Checks)
	}
	if !strings.Contains(res.Checks[0].DetailsLogExcerpt, "boom") {
		t.Errorf("log excerpt should carry the failing assertion; got %q", res.Checks[0].DetailsLogExcerpt)
	}
}

func TestTailString(t *testing.T) {
	if got := tailString("abc", 10); got != "abc" {
		t.Errorf("short string: %q", got)
	}
	got := tailString("abcdefghij", 4)
	if got != "…ghij" {
		t.Errorf("tail: %q", got)
	}
}
