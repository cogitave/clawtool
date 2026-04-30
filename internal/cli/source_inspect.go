package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/secrets"
)

// ErrSourceNotFound is returned by `source inspect` when the operator
// names an instance that isn't in the local config. Stable error so
// scripts can branch on it via errors.Is.
var ErrSourceNotFound = errors.New("source not found")

// inspectorRunner is the seam tests stub so they don't shell out to
// real npx during a unit test. Production path execs npx directly and
// returns the inspector's stdout (a JSON tools listing). On non-zero
// exit it surfaces the combined stderr in the error.
//
// argv is the full invocation including the leading runner ("npx"
// in production); env is the resolved env-var map for the source —
// passed through untouched, the inspector inherits it via os/exec.
var inspectorRunner = func(ctx context.Context, argv []string, env map[string]string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty inspector argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("npx @modelcontextprotocol/inspector failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// inspectorTool is a slim subset of the inspector's `--cli`
// tools/list response. We only render name+description; the
// inspector's full schema is preserved verbatim by --format json.
type inspectorTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// runSourceInspect implements `clawtool source inspect <name>`.
//
// It looks up the named source, resolves its env-var template via
// the secrets store, builds an `npx -y @modelcontextprotocol/inspector
// --cli <command...>` invocation, runs it, and prints the discovered
// tool surface.
//
// Flags:
//
//	--dry-run        Print the npx invocation that would run; don't exec.
//	--format text|json
//	                 text (default): NAME / DESCRIPTION table.
//	                 json: passthrough the inspector's raw stdout so
//	                 pipelines see the full tool schema.
//
// Errors:
//   - ErrSourceNotFound when <name> is not configured.
//   - "source has no command" when neither a stdio command nor an
//     HTTP url is set on the source (config-shape guardrail).
func (a *App) runSourceInspect(argv []string) int {
	fs := flag.NewFlagSet("source inspect", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	dryRun := fs.Bool("dry-run", false, "Print the npx command without executing it.")
	format := fs.String("format", "text", "Output format: text | json.")
	if err := fs.Parse(reorderFlagsFirst(argv, map[string]bool{"format": true})); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprint(a.Stderr, "usage: clawtool source inspect <instance> [--dry-run] [--format text|json]\n")
		return 2
	}
	name := rest[0]
	if *format != "text" && *format != "json" {
		fmt.Fprintf(a.Stderr, "clawtool source inspect: --format must be text|json (got %q)\n", *format)
		return 2
	}

	cfg, err := config.LoadOrDefault(a.Path())
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source inspect: %v\n", err)
		return 1
	}
	src, ok := cfg.Sources[name]
	if !ok {
		fmt.Fprintf(a.Stderr, "clawtool source inspect: %v: %q\n", ErrSourceNotFound, name)
		return 1
	}
	if len(src.Command) == 0 {
		fmt.Fprintf(a.Stderr, "clawtool source inspect: source %q has no stdio command configured (HTTP-only sources are not yet supported)\n", name)
		return 1
	}

	// argv shape: npx -y @modelcontextprotocol/inspector --cli <source-cmd...>
	// `-y` auto-accepts the npm install prompt so non-interactive
	// runs (CI, hooks) don't hang on the y/N gate.
	argvOut := append([]string{"npx", "-y", "@modelcontextprotocol/inspector", "--cli"}, src.Command...)
	argvOut = append(argvOut, "--method", "tools/list")

	store, _ := secrets.LoadOrEmpty(a.SecretsPath())
	resolved, _ := store.Resolve(name, src.Env)

	if *dryRun {
		fmt.Fprintf(a.Stdout, "(dry-run) would run: %s\n", strings.Join(argvOut, " "))
		if len(resolved) > 0 {
			keys := make([]string, 0, len(resolved))
			for k := range resolved {
				keys = append(keys, k)
			}
			fmt.Fprintf(a.Stdout, "  with env: %s\n", strings.Join(keys, ", "))
		}
		return 0
	}

	out, err := inspectorRunner(context.Background(), argvOut, resolved)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source inspect: %v\n", err)
		return 1
	}

	if *format == "json" {
		// Passthrough the raw JSON so callers see the full
		// inspector schema (inputSchema, annotations, …) the
		// summary view drops on the floor.
		fmt.Fprintln(a.Stdout, strings.TrimRight(string(out), "\n"))
		return 0
	}

	// Text path: parse `{"tools":[{name,description,...}, ...]}`
	// and render a minimal NAME / DESCRIPTION listing. Unknown
	// shapes degrade to dumping the raw output so the operator
	// still sees something useful.
	var parsed struct {
		Tools []inspectorTool `json:"tools"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil || parsed.Tools == nil {
		fmt.Fprintln(a.Stdout, strings.TrimRight(string(out), "\n"))
		return 0
	}
	if len(parsed.Tools) == 0 {
		fmt.Fprintf(a.Stdout, "(source %q exposes no tools)\n", name)
		return 0
	}
	fmt.Fprintf(a.Stdout, "tools exposed by %q (%d):\n", name, len(parsed.Tools))
	for _, t := range parsed.Tools {
		desc := strings.SplitN(strings.TrimSpace(t.Description), "\n", 2)[0]
		if desc == "" {
			fmt.Fprintf(a.Stdout, "  %s\n", t.Name)
			continue
		}
		fmt.Fprintf(a.Stdout, "  %s — %s\n", t.Name, desc)
	}
	return 0
}
