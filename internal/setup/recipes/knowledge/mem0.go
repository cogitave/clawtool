package knowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

// mem0 recipe — cross-agent persistent memory via mem0.ai's official
// cloud MCP server. Per ADR-014 T3 (design from the 2026-04-26
// multi-CLI fan-out), this is the cross-machine complement to the
// brain (claude-obsidian) recipe — both can be installed; they don't
// compete. brain = single-machine personal vault; mem0 = cross-machine
// cross-agent shared memory.
//
// Apply does three things:
//   1. Inject `[knowledge.mem0]` block in the project's
//      `.clawtool/mem0.toml` recording endpoint + namespace.
//   2. Drop a marker stamp so re-applies are idempotent and
//      non-managed files refuse overwrite without --force.
//   3. Document (in the dropped file) the `claude plugin` /
//      `clawtool source add` follow-ups the user runs to wire
//      the MCP server into their agent.
//
// Per ADR-007 we wrap mem0.ai's official cloud MCP server
// (`https://mcp.mem0.ai/mcp`); we never reimplement the vector store
// or the embedding pipeline. Self-hosted Docker is supported by
// pointing `endpoint` at the local URL — same recipe, different
// destination.

const (
	mem0ConfigPath = ".clawtool/mem0.toml"
	mem0Upstream   = "https://mem0.ai"
	mem0DefaultURL = "https://mcp.mem0.ai/mcp"
)

type mem0Recipe struct{}

func (mem0Recipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "mem0",
		Category:    setup.CategoryKnowledge,
		Description: "Cross-agent persistent memory via mem0.ai's official cloud MCP server. Coexists with `brain` (claude-obsidian); brain stays the single-machine vault, mem0 adds cross-machine cross-agent recall. Apache-2.0 core; managed cloud + self-hosted Docker both supported.",
		Upstream:    mem0Upstream,
		Stability:   setup.StabilityBeta,
	}
}

func (mem0Recipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	path := filepath.Join(repo, mem0ConfigPath)
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, ".clawtool/mem0.toml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "mem0.toml exists but is not clawtool-managed; Apply will refuse to overwrite without force", nil
}

func (mem0Recipe) Prereqs() []setup.Prereq {
	// `claude` CLI is the canonical follow-up for wiring the MCP
	// server into Claude Code. We surface it as a prereq so the
	// wizard can prompt; the recipe itself doesn't shell out.
	return []setup.Prereq{
		{
			Name: "Claude Code CLI (for MCP source registration)",
			Check: func(_ context.Context) error {
				if _, err := exec.LookPath("claude"); err != nil {
					return errors.New("claude CLI not on PATH")
				}
				return nil
			},
			ManualHint: "Install Claude Code from https://claude.ai/code, then run `claude mcp add mem0 -- npx -y mcp-remote https://mcp.mem0.ai/mcp` to wire the cloud MCP server. mem0 also works with self-hosted Docker; point the endpoint at the local URL.",
		},
	}
}

func (mem0Recipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	endpoint := mem0DefaultURL
	if v, ok := setup.GetOption[string](opts, "endpoint"); ok && v != "" {
		endpoint = v
	}
	namespace := defaultNamespaceFromRepo(repo)
	if v, ok := setup.GetOption[string](opts, "namespace"); ok && v != "" {
		namespace = v
	}

	path := filepath.Join(repo, mem0ConfigPath)
	if existing, err := setup.ReadIfExists(path); err != nil {
		return err
	} else if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", mem0ConfigPath)
	}

	body := []byte(fmt.Sprintf(`# managed-by: clawtool — mem0 recipe
# Cross-agent persistent memory via mem0.ai. Edit freely; the recipe
# re-applies only when explicitly forced.

[knowledge.mem0]
endpoint  = %q
namespace = %q
# Set namespace_per_agent = true to scope memories per agent
# instance (claude-personal vs claude-work). Default = false (shared).
namespace_per_agent = false

# Wire the MCP server into Claude Code (one-time, host-global):
#   claude mcp add mem0 -- npx -y mcp-remote %s
#
# Then ask any agent: "remember that we use postgres pgvector for
# embeddings." mem0 stores it; later sessions can search_memories or
# get_memories to recall.
#
# Self-hosted Docker: point the endpoint at your local URL (e.g.
# http://localhost:8000/mcp) and rerun 'claude mcp add' against it.
`, endpoint, namespace, endpoint))

	return setup.WriteAtomic(path, body, 0o644)
}

func (mem0Recipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, mem0ConfigPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", mem0ConfigPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", mem0ConfigPath)
	}
	return nil
}

// defaultNamespaceFromRepo derives a per-project namespace from the
// repo path. Uses the basename so memories isolate cleanly between
// projects without leaking absolute paths.
func defaultNamespaceFromRepo(repo string) string {
	abs, err := filepath.Abs(repo)
	if err != nil {
		return filepath.Base(repo)
	}
	// Walk up to the git toplevel if available; otherwise basename.
	ns := filepath.Base(abs)
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		// Not a git root; basename is fine.
		return ns
	}
	return ns
}

func init() { setup.Register(mem0Recipe{}) }
