// Package productivity hosts recipes that ship curated, daily-use
// playbooks for AI-assisted development. The recipes live under
// the existing CategoryAgents bucket because clawtool's category
// taxonomy is frozen at v1.0 (see internal/setup/category.go) and
// "skill bindings for an AI agent" is exactly that bucket's remit.
// The directory name is `productivity` to mirror upstream's own
// shelf-grouping vocabulary; it has no impact on category routing.
package productivity

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/cogitave/clawtool/internal/setup"
)

// Embedded SKILL.md bodies, one per shipped skill. Stored verbatim
// from upstream (https://github.com/mattpocock/skills, MIT) — the
// recipe stamps the managed-by marker at Apply time so the assets
// directory stays a clean mirror of the upstream playbooks.

//go:embed assets/diagnose.md
var skillDiagnose []byte

//go:embed assets/grill-with-docs.md
var skillGrillWithDocs []byte

//go:embed assets/tdd.md
var skillTDD []byte

//go:embed assets/to-prd.md
var skillToPRD []byte

//go:embed assets/to-issues.md
var skillToIssues []byte

//go:embed assets/triage.md
var skillTriage []byte

//go:embed assets/improve-codebase-architecture.md
var skillImproveCodebaseArchitecture []byte

//go:embed assets/zoom-out.md
var skillZoomOut []byte

//go:embed assets/setup-matt-pocock-skills.md
var skillSetup []byte

// skillsRoot is the in-repo directory that Claude Code scans for
// project-scoped skills. The recipe writes to <repo>/.claude/skills/
// rather than the operator's home so the playbooks travel with the
// checkout — same model as the rest of clawtool's repo recipes.
const skillsRoot = ".claude/skills"

// markerLine is the literal first line we prepend to every managed
// SKILL.md. Encoded as an HTML comment so it survives unmodified
// through the YAML frontmatter parser that opens each upstream
// SKILL.md (the parser bails out on the first non-`---` line, and
// an HTML comment above the frontmatter is treated as plain prose).
const markerLine = "<!-- " + setup.ManagedByMarker + " -->\n"

// shippedSkill pairs a skill's directory name with its embedded
// body. Order is alphabetical for deterministic Apply / Verify
// output; tests assert against this slice rather than a map so
// ordering bugs surface in CI.
type shippedSkill struct {
	name string
	body []byte
}

// shippedSkills lists every SKILL.md the recipe drops. Adding or
// removing a skill is a one-line edit here plus a matching
// //go:embed directive above. The list mirrors mattpocock's
// "engineering" shelf as of repo SHA b843cb5; setup-matt-pocock-skills
// is the prerequisite playbook he asks operators to install first.
var shippedSkills = []shippedSkill{
	{"diagnose", skillDiagnose},
	{"grill-with-docs", skillGrillWithDocs},
	{"improve-codebase-architecture", skillImproveCodebaseArchitecture},
	{"setup-matt-pocock-skills", skillSetup},
	{"tdd", skillTDD},
	{"to-issues", skillToIssues},
	{"to-prd", skillToPRD},
	{"triage", skillTriage},
	{"zoom-out", skillZoomOut},
}

// mattpocockSkillsRecipe drops mattpocock's curated engineering
// skills (https://github.com/mattpocock/skills, MIT) into the
// repo's .claude/skills/ tree so Claude Code picks them up as
// project-scoped playbooks. The skills cover the daily-use loop
// Pocock markets publicly: diagnose, tdd, to-prd / to-issues,
// triage, zoom-out, plus the architecture-review and grill-with-
// docs research patterns and the setup playbook he asks operators
// to install first.
//
// The skills retain their original MIT license; the recipe is just
// transport. Operators can edit any dropped file freely — once the
// `managed-by: clawtool` marker is gone (or the file content is
// changed in a way that strips the marker) re-Apply refuses to
// overwrite.
type mattpocockSkillsRecipe struct{}

func (mattpocockSkillsRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "mattpocock-skills",
		Category:    setup.CategoryAgents,
		Description: "Drops mattpocock's curated engineering Claude-Code skills (diagnose, tdd, to-prd, to-issues, triage, zoom-out, grill-with-docs, improve-codebase-architecture, setup) into .claude/skills/.",
		Upstream:    "https://github.com/mattpocock/skills",
		Stability:   setup.StabilityBeta,
		Core:        true,
	}
}

// Detect walks each shipped skill and folds the per-file states
// into one Status:
//
//   - every file present + carries the marker → Applied
//   - any file present without the marker     → Partial (Apply refuses)
//   - mix of present-managed + missing        → Partial (Apply will reconcile)
//   - none present                            → Absent
//
// The "Partial → refuse" case is the same operator-edit safety
// promise every other recipe makes: clawtool never silently
// overwrites a file it didn't write.
func (mattpocockSkillsRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	var (
		presentManaged   []string
		presentUnmanaged []string
		missing          []string
	)
	for _, s := range shippedSkills {
		path := skillFilePath(repo, s.name)
		b, err := setup.ReadIfExists(path)
		if err != nil {
			return setup.StatusError, "", fmt.Errorf("detect %s: %w", s.name, err)
		}
		switch {
		case b == nil:
			missing = append(missing, s.name)
		case setup.HasMarker(b, setup.ManagedByMarker):
			presentManaged = append(presentManaged, s.name)
		default:
			presentUnmanaged = append(presentUnmanaged, s.name)
		}
	}

	switch {
	case len(presentUnmanaged) > 0:
		sort.Strings(presentUnmanaged)
		return setup.StatusPartial,
			fmt.Sprintf("unmanaged SKILL.md present for: %v — Apply will refuse to overwrite without force=true", presentUnmanaged),
			nil
	case len(missing) == 0:
		return setup.StatusApplied, fmt.Sprintf("%d skills installed and clawtool-managed", len(presentManaged)), nil
	case len(presentManaged) == 0:
		return setup.StatusAbsent, "no mattpocock skills present in .claude/skills/", nil
	default:
		sort.Strings(missing)
		return setup.StatusPartial,
			fmt.Sprintf("%d/%d skills installed; missing: %v", len(presentManaged), len(shippedSkills), missing),
			nil
	}
}

// Prereqs is intentionally empty: the skills are markdown that
// Claude Code reads on its own. No binary is required for the
// recipe to function or to be useful.
func (mattpocockSkillsRecipe) Prereqs() []setup.Prereq { return nil }

// Apply writes every shipped SKILL.md into <repo>/.claude/skills/
// <name>/SKILL.md, prepending the managed-by marker as the FIRST
// line. Refuses to overwrite any file that exists without the
// marker (operator edits stay safe) unless opts[force] is true.
//
// Atomic per-file via setup.WriteAtomic; on a partial failure the
// already-written files stay on disk and the error wraps the
// offending skill name so the operator can investigate.
func (mattpocockSkillsRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	if repo == "" {
		return errors.New("apply: empty repo path")
	}
	// Pre-flight: refuse the whole batch if any unmanaged file
	// exists. Half-Applied state is worse than no-Apply because
	// the operator would have to reconcile per-file by hand.
	for _, s := range shippedSkills {
		path := skillFilePath(repo, s.name)
		existing, err := setup.ReadIfExists(path)
		if err != nil {
			return fmt.Errorf("apply pre-check %s: %w", s.name, err)
		}
		if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
			return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite (pass force=true to override)", path)
		}
	}

	for _, s := range shippedSkills {
		path := skillFilePath(repo, s.name)
		body := stampMarker(s.body)
		if err := setup.WriteAtomic(path, body, 0o644); err != nil {
			return fmt.Errorf("apply write %s: %w", s.name, err)
		}
	}
	return nil
}

// Verify confirms every shipped skill is present and carries the
// marker. Same shape as the per-file recipes (promptfoo-redteam,
// dependabot) — re-runnable, returns the first failing skill so
// the error message is actionable.
func (mattpocockSkillsRecipe) Verify(_ context.Context, repo string) error {
	for _, s := range shippedSkills {
		path := skillFilePath(repo, s.name)
		b, err := setup.ReadIfExists(path)
		if err != nil {
			return fmt.Errorf("verify %s: %w", s.name, err)
		}
		if b == nil {
			return fmt.Errorf("verify: %s missing", path)
		}
		if !setup.HasMarker(b, setup.ManagedByMarker) {
			return fmt.Errorf("verify: clawtool marker missing in %s", path)
		}
	}
	return nil
}

// skillFilePath returns the absolute on-disk path for a given
// skill's SKILL.md inside repo's .claude/skills tree.
func skillFilePath(repo, name string) string {
	return filepath.Join(repo, skillsRoot, name, "SKILL.md")
}

// stampMarker prepends the managed-by HTML-comment marker to the
// upstream body when it isn't already present. We check first so
// the function is safe to call repeatedly (idempotent re-Apply
// over a managed file would otherwise stack marker lines).
func stampMarker(body []byte) []byte {
	if bytes.HasPrefix(body, []byte(markerLine)) {
		return body
	}
	out := make([]byte, 0, len(markerLine)+len(body))
	out = append(out, []byte(markerLine)...)
	out = append(out, body...)
	return out
}

func init() { setup.Register(mattpocockSkillsRecipe{}) }
