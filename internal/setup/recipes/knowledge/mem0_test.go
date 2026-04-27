package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestMem0_Registered(t *testing.T) {
	r := setup.Lookup("mem0")
	if r == nil {
		t.Fatal("mem0 should self-register")
	}
	if r.Meta().Category != setup.CategoryKnowledge {
		t.Errorf("category: got %q, want knowledge", r.Meta().Category)
	}
	if r.Meta().Stability != setup.StabilityBeta {
		t.Errorf("stability: got %q, want beta", r.Meta().Stability)
	}
}

func TestMem0_DetectAbsent(t *testing.T) {
	r := setup.Lookup("mem0")
	dir := t.TempDir()
	status, detail, err := r.Detect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("status: got %q, want absent", status)
	}
	if !strings.Contains(detail, "mem0.toml") {
		t.Errorf("detail should mention the missing file: %q", detail)
	}
}

func TestMem0_ApplyDropsConfig(t *testing.T) {
	r := setup.Lookup("mem0")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".clawtool/mem0.toml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, "managed-by: clawtool") {
		t.Error("config should carry the clawtool marker")
	}
	if !strings.Contains(s, "[knowledge.mem0]") {
		t.Error("config should declare [knowledge.mem0] block")
	}
	if !strings.Contains(s, "https://mcp.mem0.ai/mcp") {
		t.Error("config should default to the cloud MCP server endpoint")
	}
	if !strings.Contains(s, "namespace_per_agent") {
		t.Error("config should document the namespace_per_agent toggle")
	}
}

func TestMem0_VerifyAfterApply(t *testing.T) {
	r := setup.Lookup("mem0")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify should succeed after Apply: %v", err)
	}
}

func TestMem0_RefusesUnmanagedOverwrite(t *testing.T) {
	r := setup.Lookup("mem0")
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".clawtool")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mem0.toml"),
		[]byte("# user-authored, no marker\n"), 0o644); err != nil {
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

func TestMem0_ForcedOverwriteSucceeds(t *testing.T) {
	r := setup.Lookup("mem0")
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".clawtool")
	_ = os.MkdirAll(configDir, 0o755)
	if err := os.WriteFile(filepath.Join(configDir, "mem0.toml"),
		[]byte("# user-authored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, setup.Options{"force": true}); err != nil {
		t.Errorf("forced Apply should overwrite: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(configDir, "mem0.toml"))
	if !strings.Contains(string(body), "managed-by: clawtool") {
		t.Error("forced Apply should stamp the marker")
	}
}

func TestMem0_CustomEndpointAndNamespace(t *testing.T) {
	r := setup.Lookup("mem0")
	dir := t.TempDir()
	opts := setup.Options{
		"endpoint":  "http://localhost:8000/mcp",
		"namespace": "custom-ns",
	}
	if err := r.Apply(context.Background(), dir, opts); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".clawtool/mem0.toml"))
	s := string(body)
	if !strings.Contains(s, "http://localhost:8000/mcp") {
		t.Error("custom endpoint should appear in config")
	}
	if !strings.Contains(s, "custom-ns") {
		t.Error("custom namespace should appear in config")
	}
}
