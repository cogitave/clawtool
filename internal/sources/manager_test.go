package sources

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/mark3labs/mcp-go/mcp"
)

// ensureStubServer returns an absolute path to the e2e stub-server binary.
// It builds the binary on demand so `go test ./internal/sources` works
// even when the operator hasn't run `make stub-server` first.
func ensureStubServer(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	stubPath := filepath.Join(repoRoot, "test", "e2e", "stub-server", "stub-server")

	if _, err := os.Stat(stubPath); err == nil {
		return stubPath
	}

	cmd := exec.Command("go", "build", "-o", stubPath, "./test/e2e/stub-server")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build stub-server: %v\n%s", err, out)
	}
	return stubPath
}

// stubManager builds a manager with one configured stub source. Returns
// the manager (already Start()-ed) and a cleanup func.
func stubManager(t *testing.T) (*Manager, func()) {
	t.Helper()
	stub := ensureStubServer(t)

	cfg := config.Config{
		Sources: map[string]config.Source{
			"stub": {
				Type:    "mcp",
				Command: []string{stub},
			},
		},
	}
	sec := &secrets.Store{Scopes: map[string]map[string]string{}}
	mgr := NewManager(cfg, sec)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		mgr.Stop()
		t.Fatalf("manager Start: %v", err)
	}
	return mgr, mgr.Stop
}

func TestManager_StartsRunningInstance(t *testing.T) {
	mgr, stop := stubManager(t)
	defer stop()

	health := mgr.Health()
	if len(health) != 1 {
		t.Fatalf("health = %d entries, want 1", len(health))
	}
	h := health[0]
	if h.Name != "stub" {
		t.Errorf("health.Name = %q, want stub", h.Name)
	}
	if h.Status != StatusRunning {
		t.Errorf("health.Status = %q, want %q (reason: %s)", h.Status, StatusRunning, h.Reason)
	}
	if h.ToolCount < 1 {
		t.Errorf("health.ToolCount = %d, want >= 1 (stub registers echo)", h.ToolCount)
	}
}

func TestManager_AggregatedTools_PrefixedWithInstance(t *testing.T) {
	mgr, stop := stubManager(t)
	defer stop()

	tools := mgr.AggregatedTools()
	if len(tools) == 0 {
		t.Fatal("aggregated tools empty; expected at least stub__echo")
	}

	var found bool
	for _, st := range tools {
		if st.Tool.Name == "stub__echo" {
			found = true
			break
		}
	}
	if !found {
		var names []string
		for _, st := range tools {
			names = append(names, st.Tool.Name)
		}
		t.Errorf("stub__echo not in aggregated tools; got %v", names)
	}
}

func TestManager_RouteCall_ReturnsChildResult(t *testing.T) {
	mgr, stop := stubManager(t)
	defer stop()

	tools := mgr.AggregatedTools()
	var handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	for _, st := range tools {
		if st.Tool.Name == "stub__echo" {
			handler = st.Handler
			break
		}
	}
	if handler == nil {
		t.Fatal("stub__echo handler not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "stub__echo"
	req.Params.Arguments = map[string]any{"text": "hello-from-test"}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if result == nil || len(result.Content) == 0 {
		t.Fatalf("handler returned empty result: %+v", result)
	}
	body := ""
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			body += tc.Text
		}
	}
	if !strings.Contains(body, "echo:hello-from-test") {
		t.Errorf("routed result body = %q, want it to contain 'echo:hello-from-test'", body)
	}
}

func TestSplitWireName(t *testing.T) {
	cases := []struct {
		in       string
		instance string
		tool     string
		ok       bool
	}{
		{"stub__echo", "stub", "echo", true},
		{"github-personal__create_issue", "github-personal", "create_issue", true},
		{"Bash", "", "", false},                    // no separator: core tool
		{"__leading", "", "", false},               // empty instance
		{"trailing__", "", "", false},              // empty tool
		{"", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, gotTool, ok := SplitWireName(c.in)
			if got != c.instance || gotTool != c.tool || ok != c.ok {
				t.Errorf("SplitWireName(%q) = (%q, %q, %v), want (%q, %q, %v)",
					c.in, got, gotTool, ok, c.instance, c.tool, c.ok)
			}
		})
	}
}

func TestManager_MissingEnvMarksUnauthenticated(t *testing.T) {
	cfg := config.Config{
		Sources: map[string]config.Source{
			"with-auth": {
				Type:    "mcp",
				Command: []string{"/bin/true"},
				Env:     map[string]string{"DOES_NOT_EXIST_ANYWHERE": "${DOES_NOT_EXIST_ANYWHERE}"},
			},
		},
	}
	sec := &secrets.Store{Scopes: map[string]map[string]string{}}
	mgr := NewManager(cfg, sec)
	defer mgr.Stop()

	err := mgr.Start(context.Background())
	if err == nil {
		t.Error("expected aggregated start error for missing required env")
	}

	h := mgr.Health()
	if len(h) != 1 {
		t.Fatalf("health = %d, want 1", len(h))
	}
	if h[0].Status != StatusUnauthenticated {
		t.Errorf("status = %q, want %q (reason: %s)", h[0].Status, StatusUnauthenticated, h[0].Reason)
	}
	if !strings.Contains(h[0].Reason, "DOES_NOT_EXIST_ANYWHERE") {
		t.Errorf("reason should mention missing var: %q", h[0].Reason)
	}
}

func TestManager_BadCommandMarksDown(t *testing.T) {
	cfg := config.Config{
		Sources: map[string]config.Source{
			"broken": {
				Type:    "mcp",
				Command: []string{"/no/such/binary/here/clawtool-test"},
			},
		},
	}
	sec := &secrets.Store{Scopes: map[string]map[string]string{}}
	mgr := NewManager(cfg, sec)
	defer mgr.Stop()

	_ = mgr.Start(context.Background())
	h := mgr.Health()
	if len(h) != 1 {
		t.Fatalf("health = %d, want 1", len(h))
	}
	if h[0].Status != StatusDown {
		t.Errorf("status = %q, want %q (reason: %s)", h[0].Status, StatusDown, h[0].Reason)
	}
}

func TestManager_StopReapsAll(t *testing.T) {
	mgr, _ := stubManager(t)
	mgr.Stop()
	// Status of every instance should now be Down.
	for _, h := range mgr.Health() {
		if h.Status == StatusRunning {
			t.Errorf("instance %q still Running after Stop", h.Name)
		}
	}
	// Aggregated tools should be empty after stop.
	if got := mgr.AggregatedTools(); len(got) != 0 {
		t.Errorf("AggregatedTools after Stop = %d, want 0", len(got))
	}
}
