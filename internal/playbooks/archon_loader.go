// Package playbooks owns clawtool's read-only ingestion of external
// workflow / playbook formats. Phase 1 carries one entrant: the
// Archon YAML workflow loader (coleam00/Archon, MIT). Phase 2 will
// wire execution; today the loader only parses + surfaces.
//
// Targeted Archon schema: the v2 DAG-workflow format used by
// `.archon/workflows/*.yaml`, as documented at
// https://archon.diy/guides/authoring-workflows/ and observed in
// upstream's defaults at .archon/workflows/defaults/ on the `dev`
// branch (commit set as of 2026-04-29). Archon does not version-tag
// its schema; we pin behaviour to that snapshot. Unknown / future
// node kinds are tolerated (tagged "unsupported:<name>") so a
// schema bump won't crash the loader.
package playbooks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// ErrArchonYAMLParse wraps a yaml.v3 decode failure for a single
// workflow file. Tests pin this typed error via errors.Is.
var ErrArchonYAMLParse = errors.New("playbooks: archon yaml parse error")

// ArchonWorkflow is the projected view of one .archon/workflows/<f>.yaml
// file. We deliberately only carry Name, Description, and the node
// list — fields like top-level `inputs`, `outputs`, and adapter
// hints are out of phase-1 scope.
type ArchonWorkflow struct {
	// Name is the workflow's `name:` field. Defaults to the file's
	// basename (sans .yaml) when missing so list output stays
	// useful for unnamed drafts.
	Name string

	// Description is `description:` (often a multi-line block scalar
	// in upstream defaults). May be empty.
	Description string

	// Path is the absolute path the workflow was loaded from.
	// Surfaced so phase 2 can re-open the file for execution.
	Path string

	// Nodes preserves source order. DAG topology lives in the YAML
	// (`depends_on` per node); we don't resolve it at parse time.
	Nodes []ArchonNode
}

// ArchonNode is the discriminated-union view of one workflow node.
// Kind selects which of Prompt / Bash / Loop / Command is populated;
// the others are zero. Unknown kinds are tagged "unsupported:<orig>"
// so phase 2 can wire them without re-parsing.
type ArchonNode struct {
	// ID is the node's `id:` field. Empty IDs are tolerated for
	// hand-edited drafts (Archon itself rejects them at run time).
	ID string

	// Kind is one of:
	//   "prompt"  — `prompt: ...` (AI step)
	//   "bash"    — `bash: ...`   (deterministic shell step)
	//   "loop"    — `loop: { prompt, until, max_iterations, ... }`
	//   "command" — `command: archon-<slug>` (slash-command dispatch)
	//   "unsupported:<original-key>" — recognised but not yet supported
	//     (e.g. "unsupported:parallel"). Phase 2 wires these.
	Kind string

	// Prompt is populated when Kind == "prompt".
	Prompt string

	// Bash is populated when Kind == "bash".
	Bash string

	// Loop is populated when Kind == "loop". Pointer so the zero
	// value is unambiguously "not a loop".
	Loop *ArchonLoop

	// Command is populated when Kind == "command".
	Command string

	// DependsOn mirrors the YAML `depends_on:` list. Phase 2 uses
	// this for DAG traversal; phase 1 only carries it through.
	DependsOn []string
}

// ArchonLoop is the projection of an Archon `loop:` block. Phase 2
// will translate Until / MaxIterations / FreshContext / Interactive
// into clawtool TaskWait + SendMessage primitives.
type ArchonLoop struct {
	Prompt        string
	Until         string
	MaxIterations int
	FreshContext  bool
	Interactive   bool
}

// rawWorkflow / rawNode mirror the on-disk YAML one-to-one. We
// never expose them; LoadFromDir projects them into ArchonWorkflow.
type rawWorkflow struct {
	Name        string    `yaml:"name"`
	Description string    `yaml:"description"`
	Nodes       []rawNode `yaml:"nodes"`
}

type rawNode struct {
	ID        string   `yaml:"id"`
	Prompt    string   `yaml:"prompt"`
	Bash      string   `yaml:"bash"`
	Command   string   `yaml:"command"`
	Loop      *rawLoop `yaml:"loop"`
	DependsOn []string `yaml:"depends_on"`
	// Capture the raw map so we can detect future / unsupported
	// shapes (parallel, mcp_call, sub_workflow, …) without listing
	// them ahead of time.
	Extra map[string]yaml.Node `yaml:",inline"`
}

type rawLoop struct {
	Prompt        string `yaml:"prompt"`
	Until         string `yaml:"until"`
	MaxIterations int    `yaml:"max_iterations"`
	FreshContext  bool   `yaml:"fresh_context"`
	Interactive   bool   `yaml:"interactive"`
}

// LoadFromDir walks <dir>/.archon/workflows/*.yaml (non-recursive,
// matching upstream's flat layout), parses each, and returns a
// slice sorted by workflow Name for stable list output. The
// upstream `defaults/` subdirectory is intentionally excluded —
// those are bundled samples, not the operator's playbooks.
//
// A malformed file aborts the load and returns ErrArchonYAMLParse
// wrapped with the offending path. Callers that want best-effort
// loading can filter to the un-malformed entries themselves.
func LoadFromDir(dir string) ([]ArchonWorkflow, error) {
	root := filepath.Join(dir, ".archon", "workflows")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]ArchonWorkflow, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			// Skip `defaults/` and any other nested layout —
			// phase 1 only reads the top-level workflows.
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		full := filepath.Join(root, name)
		wf, err := loadOne(full)
		if err != nil {
			return nil, err
		}
		out = append(out, wf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func loadOne(path string) (ArchonWorkflow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ArchonWorkflow{}, err
	}
	var rw rawWorkflow
	if err := yaml.Unmarshal(raw, &rw); err != nil {
		return ArchonWorkflow{}, fmt.Errorf("%w: %s: %v", ErrArchonYAMLParse, path, err)
	}
	wf := ArchonWorkflow{
		Name:        rw.Name,
		Description: rw.Description,
		Path:        path,
	}
	if wf.Name == "" {
		// Fall back to filename so list output stays useful.
		base := filepath.Base(path)
		wf.Name = base[:len(base)-len(filepath.Ext(base))]
	}
	wf.Nodes = make([]ArchonNode, 0, len(rw.Nodes))
	for _, rn := range rw.Nodes {
		wf.Nodes = append(wf.Nodes, projectNode(rn))
	}
	return wf, nil
}

// projectNode discriminates a rawNode into a typed ArchonNode.
// Precedence (matches upstream's ergonomics — a node uses exactly
// one primary kind): bash > prompt > loop > command > unsupported.
// Bash wins over prompt because Archon's deterministic-step
// convention is bash-first; a node that declares both is malformed
// in upstream's eyes, so the precedence here is a best-effort
// projection rather than a semantic claim.
func projectNode(rn rawNode) ArchonNode {
	n := ArchonNode{
		ID:        rn.ID,
		DependsOn: rn.DependsOn,
	}
	switch {
	case rn.Bash != "":
		n.Kind = "bash"
		n.Bash = rn.Bash
	case rn.Prompt != "":
		n.Kind = "prompt"
		n.Prompt = rn.Prompt
	case rn.Loop != nil:
		n.Kind = "loop"
		n.Loop = &ArchonLoop{
			Prompt:        rn.Loop.Prompt,
			Until:         rn.Loop.Until,
			MaxIterations: rn.Loop.MaxIterations,
			FreshContext:  rn.Loop.FreshContext,
			Interactive:   rn.Loop.Interactive,
		}
	case rn.Command != "":
		n.Kind = "command"
		n.Command = rn.Command
	default:
		// No recognised primary key. If Extra carries something,
		// tag the first sorted key as the unsupported kind so
		// phase 2 has the original name to dispatch on. We sort
		// to keep test output deterministic when multiple unknown
		// keys land in the same node (rare in practice).
		n.Kind = "unsupported:unknown"
		if len(rn.Extra) > 0 {
			keys := make([]string, 0, len(rn.Extra))
			for k := range rn.Extra {
				// Skip metadata keys we know about but didn't
				// promote to a primary kind (context, model,
				// etc.) — those aren't node-shape determiners.
				switch k {
				case "context", "model", "fresh_context", "timeout",
					"on_failure", "branch_name", "name", "description":
					continue
				}
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if len(keys) > 0 {
				n.Kind = "unsupported:" + keys[0]
			}
		}
	}
	return n
}

// Summary returns a one-line human banner describing the workflow.
// Format: `<name> — <node-count> nodes`. CLI list output uses this
// directly; phase 2 may extend it with status indicators.
func (w ArchonWorkflow) Summary() string {
	suffix := "nodes"
	if len(w.Nodes) == 1 {
		suffix = "node"
	}
	return fmt.Sprintf("%s — %d %s", w.Name, len(w.Nodes), suffix)
}
