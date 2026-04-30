// Package agents — clawtool-autonomous-loop SKILL.md recipe.
//
// Drops a Claude Code skill at <repo>/.claude/skills/clawtool-
// autonomous-loop/SKILL.md that teaches a dispatched agent the
// tick-N.json contract used by `clawtool autonomous` (CLI verb)
// and the AutonomousRun MCP tool. Without it the agent has only
// the per-iteration session prompt to work from — easy to misread
// the schema, easy to forget to write the tick file.
//
// Stability: Beta. Core: true — every onboarded repo benefits
// because every onboarded repo can be the target of an autonomous
// run.
package agents

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/clawtool-autonomous-loop.md
var autonomousLoopSkillBody []byte

// autonomousLoopSkillPath is the repo-relative location Claude Code
// scans for project-scoped skills. Mirrors mattpocock-skills.
const autonomousLoopSkillPath = ".claude/skills/clawtool-autonomous-loop/SKILL.md"

// autonomousLoopMarker is the managed-by HTML comment prepended at
// Apply time. Encoded as a comment so it sits cleanly above the
// YAML frontmatter without breaking Claude Code's parser.
const autonomousLoopMarker = "<!-- " + setup.ManagedByMarker + " -->\n"

type clawtoolAutonomousSkillRecipe struct{}

func (clawtoolAutonomousSkillRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "clawtool-autonomous-loop",
		Category:    setup.CategoryAgents,
		Description: "Drops a Claude Code SKILL.md teaching the tick-N.json contract used by `clawtool autonomous` and the AutonomousRun MCP tool.",
		Upstream:    "https://github.com/cogitave/clawtool/blob/main/internal/cli/autonomous.go",
		Stability:   setup.StabilityBeta,
		Core:        true,
	}
}

func (clawtoolAutonomousSkillRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, autonomousLoopSkillPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, autonomousLoopSkillPath + " not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, autonomousLoopSkillPath + " exists but is not clawtool-managed; Apply will refuse to overwrite without force=true", nil
}

// Prereqs is empty: the SKILL.md is markdown that Claude Code
// reads on its own. No binary or service is required.
func (clawtoolAutonomousSkillRecipe) Prereqs() []setup.Prereq { return nil }

func (clawtoolAutonomousSkillRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	if repo == "" {
		return errors.New("apply: empty repo path")
	}
	path := filepath.Join(repo, autonomousLoopSkillPath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return fmt.Errorf("apply pre-check: %w", err)
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite (pass force=true to override)", path)
	}
	return setup.WriteAtomic(path, stampAutonomousMarker(autonomousLoopSkillBody), 0o644)
}

func (clawtoolAutonomousSkillRecipe) Verify(_ context.Context, repo string) error {
	path := filepath.Join(repo, autonomousLoopSkillPath)
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", path)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", path)
	}
	return nil
}

// stampAutonomousMarker prepends the managed-by HTML comment when it
// isn't already present. Idempotent so re-Apply over a managed file
// does not stack marker lines.
func stampAutonomousMarker(body []byte) []byte {
	if bytes.HasPrefix(body, []byte(autonomousLoopMarker)) {
		return body
	}
	out := make([]byte, 0, len(autonomousLoopMarker)+len(body))
	out = append(out, []byte(autonomousLoopMarker)...)
	out = append(out, body...)
	return out
}

func init() { setup.Register(clawtoolAutonomousSkillRecipe{}) }
