package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestClawtoolRelay_Registered(t *testing.T) {
	r := setup.Lookup("clawtool-relay")
	if r == nil {
		t.Fatal("clawtool-relay should self-register")
	}
	if r.Meta().Category != setup.CategoryRuntime {
		t.Errorf("category: got %q, want runtime", r.Meta().Category)
	}
	if r.Meta().Stability != setup.StabilityBeta {
		t.Errorf("stability: got %q, want beta — promote to Stable after a soak window", r.Meta().Stability)
	}
}

func TestClawtoolRelay_DetectAbsent(t *testing.T) {
	r := setup.Lookup("clawtool-relay")
	dir := t.TempDir()
	status, detail, err := r.Detect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("status: got %q, want absent", status)
	}
	if !strings.Contains(detail, "compose.relay.yml") {
		t.Errorf("detail should mention the missing file: %q", detail)
	}
}

func TestClawtoolRelay_ApplyDropsCompose(t *testing.T) {
	r := setup.Lookup("clawtool-relay")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "compose.relay.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "managed-by: clawtool") {
		t.Errorf("compose.relay.yml should carry the clawtool marker")
	}
	if !strings.Contains(string(body), "CLAWTOOL_TOKEN_FILE") {
		t.Errorf("compose.relay.yml should mention CLAWTOOL_TOKEN_FILE")
	}
}

func TestClawtoolRelay_VerifyAfterApply(t *testing.T) {
	r := setup.Lookup("clawtool-relay")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify should succeed after Apply: %v", err)
	}
}

func TestClawtoolRelay_RefusesUnmanagedOverwrite(t *testing.T) {
	r := setup.Lookup("clawtool-relay")
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.relay.yml")
	if err := os.WriteFile(path, []byte("# user-authored, no marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := r.Apply(context.Background(), dir, nil)
	if err == nil {
		t.Fatal("Apply should refuse to overwrite an unmanaged file")
	}
	if !strings.Contains(err.Error(), "not clawtool-managed") {
		t.Errorf("error should mention unmanaged: %v", err)
	}
}

func TestClawtoolRelay_ForcedOverwriteSucceeds(t *testing.T) {
	r := setup.Lookup("clawtool-relay")
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.relay.yml")
	if err := os.WriteFile(path, []byte("# user-authored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, setup.Options{"force": true}); err != nil {
		t.Errorf("forced Apply should overwrite: %v", err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "managed-by: clawtool") {
		t.Errorf("forced Apply should stamp the marker")
	}
}
