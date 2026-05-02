package sources

import (
	"context"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/tools/registry"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestManifestDrift_DetectsMismatch builds a tiny manifest where
// the spec description differs from the live mcp.WithDescription
// string; the source must surface one Idea pointing the operator
// at the drifted tool.
func TestManifestDrift_DetectsMismatch(t *testing.T) {
	src := NewManifestDrift()
	src.SetProvider(func() *registry.Manifest {
		m := registry.New()
		m.Append(registry.ToolSpec{
			Name:        "DriftTool",
			Description: "stale spec description",
			Category:    registry.CategoryFile,
			Register: func(s *server.MCPServer, _ registry.Runtime) {
				s.AddTool(mcp.NewTool("DriftTool", mcp.WithDescription("fresh registered description")), nil)
			},
		})
		return m
	})

	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 1 {
		t.Fatalf("Scan returned %d ideas, want 1", len(ideas))
	}
	if !strings.Contains(ideas[0].Title, "DriftTool") {
		t.Fatalf("Title: %q", ideas[0].Title)
	}
	if ideas[0].SuggestedPriority < 5 {
		t.Fatalf("priority too low: %d", ideas[0].SuggestedPriority)
	}
}

// TestManifestDrift_NoDriftIsEmpty confirms a manifest in lockstep
// emits no ideas.
func TestManifestDrift_NoDriftIsEmpty(t *testing.T) {
	src := NewManifestDrift()
	src.SetProvider(func() *registry.Manifest {
		m := registry.New()
		m.Append(registry.ToolSpec{
			Name:        "Aligned",
			Description: "in sync",
			Category:    registry.CategoryFile,
			Register: func(s *server.MCPServer, _ registry.Runtime) {
				s.AddTool(mcp.NewTool("Aligned", mcp.WithDescription("in sync")), nil)
			},
		})
		return m
	})

	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}

// TestManifestDrift_NilProviderIsNoOp confirms a source with no
// wired provider quietly returns empty.
func TestManifestDrift_NilProviderIsNoOp(t *testing.T) {
	src := &ManifestDrift{}
	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}
