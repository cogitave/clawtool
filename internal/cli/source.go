package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/catalog"
	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/secrets"
)

// SecretsPath returns the secrets-store path. Tests can shadow App.SecretsPath
// to point at a tmp file; production uses secrets.DefaultPath().
func (a *App) SecretsPath() string {
	if a.secretsPath != "" {
		return a.secretsPath
	}
	return secrets.DefaultPath()
}

// runSource dispatches `clawtool source ...` subcommands.
func (a *App) runSource(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, sourceUsage)
		return 2
	}
	switch argv[0] {
	case "add":
		return a.runSourceAdd(argv[1:])
	case "list":
		return a.runSourceList(argv[1:])
	case "catalog", "available":
		return a.runSourceCatalog(argv[1:])
	case "remove", "rm":
		return a.runSourceRemove(argv[1:])
	case "rename", "mv":
		return a.runSourceRename(argv[1:])
	case "set-secret":
		return a.runSourceSetSecret(argv[1:])
	case "check":
		return a.runSourceCheck(argv[1:])
	default:
		fmt.Fprintf(a.Stderr, "clawtool source: unknown subcommand %q\n\n%s", argv[0], sourceUsage)
		return 2
	}
}

func (a *App) runSourceAdd(argv []string) int {
	fs := flag.NewFlagSet("source add", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	asInstance := fs.String("as", "", "Instance name to use (overrides the bare catalog name).")
	// stdlib flag stops at the first non-flag; reorder so flags can come
	// after positionals (the form users actually type).
	if err := fs.Parse(reorderFlagsFirst(argv, map[string]bool{"as": true})); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprint(a.Stderr, "usage: clawtool source add <name> [--as <instance>]\n")
		return 2
	}
	name := rest[0]

	cat, err := catalog.Builtin()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source add: catalog: %v\n", err)
		return 1
	}
	entry, ok := cat.Lookup(name)
	if !ok {
		fmt.Fprintf(a.Stderr, "clawtool source add: %q is not in the built-in catalog.\n", name)
		if suggestions := cat.SuggestSimilar(name, 3); len(suggestions) > 0 {
			fmt.Fprintf(a.Stderr, "  did you mean: %s?\n", strings.Join(suggestions, ", "))
		}
		fmt.Fprintln(a.Stderr, "  run `clawtool source list` to see the built-in catalog.")
		return 1
	}

	instance := name
	if *asInstance != "" {
		instance = *asInstance
	}
	if !isKebab(instance) {
		fmt.Fprintf(a.Stderr, "clawtool source add: instance %q must be kebab-case [a-z0-9-]+\n", instance)
		return 2
	}

	cmd, err := entry.ToSourceCommand()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source add: %v\n", err)
		return 1
	}

	cfgPath := a.Path()
	cfg, err := config.LoadOrDefault(cfgPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source add: %v\n", err)
		return 1
	}
	if cfg.Sources == nil {
		cfg.Sources = map[string]config.Source{}
	}
	if _, exists := cfg.Sources[instance]; exists {
		fmt.Fprintf(a.Stderr, "clawtool source add: instance %q already exists.\n", instance)
		fmt.Fprintf(a.Stderr, "  use --as <other-name> to add a second instance, e.g.\n")
		fmt.Fprintf(a.Stderr, "    clawtool source add %s --as %s-work\n", name, name)
		fmt.Fprintf(a.Stderr, "  consider renaming the existing instance:\n")
		fmt.Fprintf(a.Stderr, "    clawtool source rename %s %s-personal\n", instance, instance)
		return 1
	}
	cfg.Sources[instance] = config.Source{
		Type:    "mcp",
		Command: cmd,
		Env:     entry.EnvTemplate(),
	}
	if err := cfg.Save(cfgPath); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source add: %v\n", err)
		return 1
	}

	fmt.Fprintf(a.Stdout, "✓ added source %q\n", instance)
	fmt.Fprintf(a.Stdout, "    powered by: %s (%s)\n", entry.Package, entry.Runtime)
	if entry.Description != "" {
		fmt.Fprintf(a.Stdout, "    description: %s\n", entry.Description)
	}
	if entry.Homepage != "" {
		fmt.Fprintf(a.Stdout, "    homepage:    %s\n", entry.Homepage)
	}

	// Check secrets and warn for missing env.
	if len(entry.RequiredEnv) > 0 {
		store, _ := secrets.LoadOrEmpty(a.SecretsPath())
		var missing []string
		for _, k := range entry.RequiredEnv {
			if _, ok := store.Get(instance, k); !ok {
				if v := os.Getenv(k); v == "" {
					missing = append(missing, k)
				}
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(a.Stdout, "\n! credentials needed: %s\n", strings.Join(missing, ", "))
			if entry.AuthHint != "" {
				fmt.Fprintf(a.Stdout, "  %s\n", entry.AuthHint)
			}
			fmt.Fprintf(a.Stdout, "  store with:\n")
			for _, k := range missing {
				fmt.Fprintf(a.Stdout, "    clawtool source set-secret %s %s --value <token>\n", instance, k)
			}
		}
	}
	return 0
}

func (a *App) runSourceList(argv []string) int {
	cfg, err := config.LoadOrDefault(a.Path())
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source list: %v\n", err)
		return 1
	}
	if len(cfg.Sources) == 0 {
		fmt.Fprintln(a.Stdout, "(no sources configured. try: clawtool source add github)")
		return 0
	}
	store, _ := secrets.LoadOrEmpty(a.SecretsPath())

	names := make([]string, 0, len(cfg.Sources))
	for n := range cfg.Sources {
		names = append(names, n)
	}
	sort.Strings(names)

	fmt.Fprintln(a.Stdout, "INSTANCE                      AUTH       PACKAGE")
	for _, name := range names {
		src := cfg.Sources[name]
		auth := "n/a"
		if len(src.Env) > 0 {
			_, missing := store.Resolve(name, src.Env)
			if len(missing) == 0 {
				auth = "✓ ready"
			} else {
				auth = "✗ missing"
			}
		}
		pkg := ""
		if len(src.Command) > 0 {
			// Heuristic: skip the runner (npx/node/uvx/docker) and report
			// the next argv that looks like a package name.
			for _, arg := range src.Command[1:] {
				if !strings.HasPrefix(arg, "-") && arg != "run" && arg != "--rm" && arg != "-i" {
					pkg = arg
					break
				}
			}
		}
		fmt.Fprintf(a.Stdout, "%-29s %-10s %s\n", name, auth, pkg)
	}
	return 0
}

func (a *App) runSourceRemove(argv []string) int {
	if len(argv) != 1 {
		fmt.Fprint(a.Stderr, "usage: clawtool source remove <instance>\n")
		return 2
	}
	instance := argv[0]
	cfgPath := a.Path()
	cfg, err := config.LoadOrDefault(cfgPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source remove: %v\n", err)
		return 1
	}
	if _, ok := cfg.Sources[instance]; !ok {
		fmt.Fprintf(a.Stderr, "clawtool source remove: no instance %q\n", instance)
		return 1
	}
	delete(cfg.Sources, instance)
	if err := cfg.Save(cfgPath); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source remove: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "✓ removed source %q (config only; secrets retained)\n", instance)
	fmt.Fprintf(a.Stdout, "  to also drop secrets: edit %s\n", a.SecretsPath())
	return 0
}

func (a *App) runSourceRename(argv []string) int {
	if len(argv) != 2 {
		fmt.Fprint(a.Stderr, "usage: clawtool source rename <old-instance> <new-instance>\n")
		return 2
	}
	oldName, newName := argv[0], argv[1]
	if oldName == newName {
		fmt.Fprintln(a.Stderr, "clawtool source rename: old and new instance are the same")
		return 2
	}
	if !isKebab(newName) {
		fmt.Fprintf(a.Stderr, "clawtool source rename: instance %q must be kebab-case [a-z0-9-]+\n", newName)
		return 2
	}

	cfgPath := a.Path()
	cfg, err := config.LoadOrDefault(cfgPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source rename: %v\n", err)
		return 1
	}
	src, ok := cfg.Sources[oldName]
	if !ok {
		fmt.Fprintf(a.Stderr, "clawtool source rename: no instance %q\n", oldName)
		return 1
	}
	if _, exists := cfg.Sources[newName]; exists {
		fmt.Fprintf(a.Stderr, "clawtool source rename: instance %q already exists; remove it first or pick another name\n", newName)
		return 1
	}

	cfg.Sources[newName] = src
	delete(cfg.Sources, oldName)
	if err := cfg.Save(cfgPath); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source rename: %v\n", err)
		return 1
	}

	// Migrate secrets scope if any. Collisions can't happen here:
	// the new scope must be empty since the config-side check above
	// rejected the rename when newName already existed (and a stray
	// orphaned secrets scope without a matching source means the
	// user manually edited secrets.toml — overwriting is the
	// pragmatic call, but we keep that codepath unreachable from
	// the CLI by failing earlier).
	store, sErr := secrets.LoadOrEmpty(a.SecretsPath())
	movedSecrets := false
	if sErr == nil && store != nil {
		movedSecrets = store.Rename(oldName, newName)
		if movedSecrets {
			if err := store.Save(a.SecretsPath()); err != nil {
				fmt.Fprintf(a.Stderr, "clawtool source rename: secrets save: %v\n", err)
				// Config already saved — partial success. Surface
				// the failure but don't roll back: the rename of
				// the source itself succeeded, the secrets are
				// still readable under the OLD scope, and the
				// next `set-secret` invocation can re-stage them.
				return 1
			}
		}
	}

	fmt.Fprintf(a.Stdout, "✓ renamed source %q → %q\n", oldName, newName)
	if movedSecrets {
		fmt.Fprintln(a.Stdout, "    secrets scope migrated")
	}
	return 0
}

func (a *App) runSourceSetSecret(argv []string) int {
	fs := flag.NewFlagSet("source set-secret", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	value := fs.String("value", "", "Value to store. If empty and stdin is a pipe, reads from stdin.")
	if err := fs.Parse(reorderFlagsFirst(argv, map[string]bool{"value": true})); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 2 {
		fmt.Fprint(a.Stderr, "usage: clawtool source set-secret <instance> <KEY> [--value <value>]\n")
		return 2
	}
	instance, key := rest[0], rest[1]
	if !isKebab(instance) && instance != "global" {
		fmt.Fprintf(a.Stderr, "clawtool source set-secret: instance %q must be kebab-case (or 'global')\n", instance)
		return 2
	}

	v := *value
	if v == "" {
		raw, err := io.ReadAll(a.stdin())
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool source set-secret: read stdin: %v\n", err)
			return 1
		}
		v = strings.TrimRight(string(raw), "\n\r")
		if v == "" {
			fmt.Fprintln(a.Stderr, "clawtool source set-secret: empty value (use --value or pipe a non-empty string)")
			return 2
		}
	}

	store, err := secrets.LoadOrEmpty(a.SecretsPath())
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source set-secret: %v\n", err)
		return 1
	}
	store.Set(instance, key, v)
	if err := store.Save(a.SecretsPath()); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source set-secret: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "✓ stored secret %s for %s (%d chars)\n", key, instance, len(v))
	return 0
}

func (a *App) runSourceCheck(_ []string) int {
	cfgPath := a.Path()
	cfg, err := config.LoadOrDefault(cfgPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source check: %v\n", err)
		return 1
	}
	if len(cfg.Sources) == 0 {
		fmt.Fprintln(a.Stdout, "(no sources configured)")
		return 0
	}
	store, _ := secrets.LoadOrEmpty(a.SecretsPath())
	names := make([]string, 0, len(cfg.Sources))
	for n := range cfg.Sources {
		names = append(names, n)
	}
	sort.Strings(names)

	allOK := true
	for _, name := range names {
		src := cfg.Sources[name]
		_, missing := store.Resolve(name, src.Env)
		if len(missing) == 0 {
			fmt.Fprintf(a.Stdout, "%-30s ✓ ready\n", name)
			continue
		}
		allOK = false
		fmt.Fprintf(a.Stdout, "%-30s ✗ missing: %s\n", name, strings.Join(missing, ", "))
	}
	if !allOK {
		return 1
	}
	return 0
}

func (a *App) stdin() io.Reader {
	if a.Stdin != nil {
		return a.Stdin
	}
	return os.Stdin
}

const sourceUsage = `Usage:
  clawtool source add <name> [--as <instance>]
                              Resolve <name> from the built-in catalog and
                              register it as a source. Use --as to pick an
                              instance name (required for adding a second
                              instance of the same source).

  clawtool source list        List configured sources, auth status, package.
  clawtool source catalog     Browse the built-in catalog of MCP servers
                              (alias: 'available'). Pick a name from the
                              output and run 'clawtool source add <name>'.
  clawtool source remove <instance>
                              Delete an instance from config (secrets retained).
  clawtool source rename <old-instance> <new-instance>
                              Rename an instance — moves the [sources.<old>]
                              block in config.toml AND the matching
                              [scopes."<old>"] block in secrets.toml to the
                              new name. Refuses when <new-instance> already
                              exists. Alias: 'mv'.
  clawtool source set-secret <instance> <KEY> [--value <value>]
                              Store a credential. If --value is omitted, the
                              value is read from stdin.
  clawtool source check       Report which configured sources have all their
                              required credentials.
`

// Look for runtime errors here as well as the App-level helpers.
var _ = errors.New

// reorderFlagsFirst takes argv and returns it with all flags (with their
// values) moved to the front, so stdlib flag.FlagSet.Parse() picks them up
// even when users type the natural `subcommand <positional> --flag value`
// form. valueFlags maps flag names to whether they consume the next argv
// as a value (e.g. {"as": true, "value": true}). Flags written as
// --flag=value are passed through untouched.
func reorderFlagsFirst(argv []string, valueFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// --flag=value: value already attached.
			if strings.Contains(a, "=") {
				continue
			}
			name := strings.TrimLeft(a, "-")
			if valueFlags[name] && i+1 < len(argv) {
				flags = append(flags, argv[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}
