// Package cli implements clawtool's user-facing subcommands.
//
// Subcommand layout (ADR-004 §4):
//
//	clawtool init              interactive wizard: pick recipes, apply to repo
//	clawtool serve             run as MCP server (delegates to internal/server)
//	clawtool version           print version
//	clawtool help              print top-level usage
//	clawtool tools list        list known tools and resolved enabled state
//	clawtool tools enable <s>  set tools.<selector>.enabled = true
//	clawtool tools disable <s> set tools.<selector>.enabled = false
//	clawtool tools status <s>  print resolved state and the rule that won
//
// Source / profile / group subcommands are scaffolded in main.go usage but
// not wired in v0.2 — they land alongside the source-instance feature.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/telemetry"
	"github.com/cogitave/clawtool/internal/tools/core"
)

// emitCommandEvent fires the per-dispatch telemetry event. Strict
// allow-list: command name + first sub-arg + duration + exit code.
// Errors derive from rc (1=runtime, 2=usage); 0=success. The
// telemetry package no-ops when disabled, so the call site stays
// unconditional.
func emitCommandEvent(argv []string, rc int, dur time.Duration) {
	tc := telemetry.Get()
	if tc == nil || !tc.Enabled() {
		return
	}
	cmd := ""
	if len(argv) > 0 {
		cmd = argv[0]
	}
	sub := ""
	if len(argv) > 1 && !strings.HasPrefix(argv[1], "-") {
		sub = argv[1]
	}
	outcome := "success"
	errorClass := ""
	switch rc {
	case 0:
		outcome = "success"
	case 2:
		outcome = "usage_error"
		errorClass = "usage"
	default:
		outcome = "error"
		errorClass = "runtime"
	}
	props := map[string]any{
		"command":     cmd,
		"subcommand":  sub,
		"duration_ms": dur.Milliseconds(),
		"exit_code":   rc,
		"outcome":     outcome,
	}
	if errorClass != "" {
		props["error_class"] = errorClass
	}
	tc.Track("cli.command", props)
}

// App holds CLI dependencies. Stdout/stderr are injected so tests can capture.
type App struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	// ConfigPath overrides the default config location. Empty = config.DefaultPath().
	ConfigPath string
	// secretsPath overrides the default secrets store path. Used by tests.
	secretsPath string
}

// SetSecretsPath lets tests redirect the secrets store to a tmp file.
func (a *App) SetSecretsPath(p string) { a.secretsPath = p }

// New returns an App writing to the process's stdout/stderr and using the
// default config path.
func New() *App {
	return &App{Stdout: os.Stdout, Stderr: os.Stderr}
}

// Path returns the resolved config path (override > default).
func (a *App) Path() string {
	if a.ConfigPath != "" {
		return a.ConfigPath
	}
	return config.DefaultPath()
}

// Init writes a default config to disk if the file does not already exist.
// Returns an "already exists" error if it does — callers can ignore that.
func (a *App) Init() error {
	path := a.Path()
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(a.Stdout, "config already exists at %s\n", path)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	cfg := config.Default()
	if err := cfg.Save(path); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "✓ wrote default config to %s\n", path)
	return nil
}

// ToolsList prints every shipped tool — both the file/exec/web
// primitives in config.KnownCoreTools and the dispatch/agent/task/
// recipe/bridge surface registered via core.BuildManifest().
//
// Pre-v0.22.20 this only listed config.KnownCoreTools (9 entries),
// which created a confusing UX gap: SendMessage / AgentList /
// TaskGet / etc. WERE registered with the MCP server at daemon
// boot (host CLIs see them as `mcp__clawtool__SendMessage`) but
// `clawtool tools list` never showed them — operators couldn't
// confirm what surface their hosts actually had access to. Now
// the union of both sources is rendered, deduped on Name, sorted
// alphabetically. Resolution still flows through cfg.IsEnabled so
// per-selector overrides work for every tool — even ones that
// don't have an explicit core_tools.X entry.
func (a *App) ToolsList() error {
	cfg, err := config.LoadOrDefault(a.Path())
	if err != nil {
		return err
	}
	w := a.Stdout

	// Union: config.KnownCoreTools + manifest names.
	seen := map[string]bool{}
	type row struct {
		selector string
		res      config.Resolution
	}
	var rows []row
	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		rows = append(rows, row{selector: name, res: cfg.IsEnabled(name)})
	}
	for _, name := range config.KnownCoreTools {
		add(name)
	}
	for _, name := range core.BuildManifest().SortedNames() {
		add(name)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].selector < rows[j].selector })

	fmt.Fprintln(w, "TOOL                          STATE      RULE")
	for _, r := range rows {
		state := "enabled"
		if !r.res.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(w, "%-29s %-10s %s\n", r.selector, state, r.res.Rule)
	}
	// v0.2 doesn't yet enumerate sourced tools — note that explicitly so
	// users know the full picture is coming.
	if len(cfg.Sources) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "(%d source instance(s) configured but their tools are not yet enumerated; v0.3.)\n", len(cfg.Sources))
	}
	return nil
}

// ToolsEnable writes tools.<selector>.enabled = true.
func (a *App) ToolsEnable(selector string) error {
	return a.toolsSet(selector, true)
}

// ToolsDisable writes tools.<selector>.enabled = false.
func (a *App) ToolsDisable(selector string) error {
	return a.toolsSet(selector, false)
}

func (a *App) toolsSet(selector string, enabled bool) error {
	if err := validateSelector(selector); err != nil {
		return err
	}
	path := a.Path()
	cfg, err := config.LoadOrDefault(path)
	if err != nil {
		return err
	}
	cfg.SetToolEnabled(selector, enabled)
	if err := cfg.Save(path); err != nil {
		return err
	}
	state := "enabled"
	if !enabled {
		state = "disabled"
	}
	fmt.Fprintf(a.Stdout, "✓ %s %s (rule: tools.%s)\n", selector, state, quoteIfDot(selector))
	return nil
}

// ToolsStatus prints the resolved enabled state for a selector and the rule
// that won, per ADR-004 §4.
func (a *App) ToolsStatus(selector string) error {
	if err := validateSelector(selector); err != nil {
		return err
	}
	cfg, err := config.LoadOrDefault(a.Path())
	if err != nil {
		return err
	}
	r := cfg.IsEnabled(selector)
	state := "enabled"
	if !r.Enabled {
		state = "disabled"
	}
	fmt.Fprintf(a.Stdout, "%s %s (rule: %s)\n", selector, state, r.Rule)
	return nil
}

// Run dispatches argv (excluding program name) to the right subcommand.
// Returns the exit code; 0 = success, 2 = usage error, 1 = runtime failure.
//
// Every dispatch is timed and emitted as a `cli.command` telemetry
// event (when telemetry is opted in) — command, subcommand, exit_code,
// duration_ms, error_class. Long-running verbs (`serve`, `dashboard`,
// `daemon` foreground) emit on dispatcher exit so a 2-hour `serve`
// session lands as one event with the full uptime.
func (a *App) Run(argv []string) int {
	rc := a.dispatch(argv)
	emitCommandEvent(argv, rc, time.Since(cliStart))
	return rc
}

// cliStart is captured at package-init time so the timer covers the
// dispatcher entry, not just the inner switch. Run() may be called
// repeatedly inside a single process (tests, daemon foreground), but
// the wall-clock since boot is the most useful "this verb took how
// long" anchor regardless.
var cliStart = time.Now()

func (a *App) dispatch(argv []string) int {
	if len(argv) == 0 {
		// No-args invocation: drop into the friendly TUI menu so
		// users who'd rather not memorise subcommands have a
		// landing UI. Falls back to topUsage on non-TTY stdin/out.
		return a.runMenu()
	}
	switch argv[0] {
	case "init":
		return a.runInit(argv[1:])
	case "tools":
		return a.runTools(argv[1:])
	case "source":
		return a.runSource(argv[1:])
	case "agents":
		return a.runAgents(argv[1:])
	case "agent":
		return a.runAgent(argv[1:])
	case "bridge":
		return a.runBridge(argv[1:])
	case "send":
		return a.runSend(argv[1:])
	case "autonomous":
		return a.runAutonomous(argv[1:])
	case "worktree":
		return a.runWorktree(argv[1:])
	case "task":
		return a.runTask(argv[1:])
	case "star":
		return a.runStar(argv[1:])
	case "upgrade":
		return a.runUpgrade(argv[1:])
	case "onboard":
		return a.runOnboard(argv[1:])
	case "telemetry":
		return a.runTelemetry(argv[1:])
	case "setup":
		return a.runSetup(argv[1:])
	case "hooks":
		return a.runHooks(argv[1:])
	case "portal":
		return a.runPortal(argv[1:])
	case "recipe":
		return a.runRecipe(argv[1:])
	case "doctor":
		return a.runDoctor(argv[1:])
	case "overview":
		return a.runOverview(argv[1:])
	case "skill":
		return a.runSkill(argv[1:])
	case "mcp":
		return a.runMcp(argv[1:])
	case "uninstall":
		return a.runUninstall(argv[1:])
	case "sandbox":
		return a.runSandbox(argv[1:])
	case "unattended", "yolo":
		return a.runUnattended(argv[1:])
	case "a2a":
		return a.runA2A(argv[1:])
	case "peer":
		return a.runPeer(argv[1:])
	case "dashboard", "tui", "orchestrator", "orch":
		return a.runOrchestrator(argv[1:])
	case "rules":
		return a.runRules(argv[1:])
	case "daemon":
		return a.runDaemon(argv[1:])
	case "sandbox-worker":
		return a.runSandboxWorker(argv[1:])
	case "egress":
		return a.runEgress(argv[1:])
	case "claude-bootstrap":
		return a.runClaudeBootstrap(argv[1:])
	case "bootstrap":
		return a.runBootstrap(argv[1:])
	case "apm":
		return a.runApm(argv[1:])
	case "playbook":
		return a.runPlaybook(argv[1:])
	case "version", "--version", "-v":
		// Version printed by caller (it owns the version package import to
		// avoid an import cycle with cli — keeps cli a leaf package).
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(a.Stdout, topUsage)
		return 0
	default:
		fmt.Fprintf(a.Stderr, "clawtool: unknown command %q\n\n%s", argv[0], topUsage)
		return 2
	}
}

func (a *App) runTools(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, toolsUsage)
		return 2
	}
	switch argv[0] {
	case "list":
		if err := a.ToolsList(); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool tools list: %v\n", err)
			return 1
		}
	case "enable":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool tools enable <selector>\n")
			return 2
		}
		if err := a.ToolsEnable(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool tools enable: %v\n", err)
			return 1
		}
	case "disable":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool tools disable <selector>\n")
			return 2
		}
		if err := a.ToolsDisable(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool tools disable: %v\n", err)
			return 1
		}
	case "status":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool tools status <selector>\n")
			return 2
		}
		if err := a.ToolsStatus(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool tools status: %v\n", err)
			return 1
		}
	case "export-typescript":
		out := "./clawtool-stubs"
		// Tiny argparser — only one optional flag for now.
		for i := 1; i < len(argv); i++ {
			switch argv[i] {
			case "--output", "-o":
				if i+1 >= len(argv) {
					fmt.Fprint(a.Stderr, "clawtool tools export-typescript: --output requires a value\n")
					return 2
				}
				out = argv[i+1]
				i++
			default:
				fmt.Fprintf(a.Stderr, "clawtool tools export-typescript: unknown flag %q\n", argv[i])
				return 2
			}
		}
		if err := a.ToolsExportTypeScript(out); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool tools export-typescript: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintf(a.Stderr, "clawtool tools: unknown subcommand %q\n\n%s", argv[0], toolsUsage)
		return 2
	}
	return 0
}

// ToolsExportTypeScript emits the manifest as a TypeScript module
// tree under outDir. One .ts per tool plus an index.ts barrel. The
// underlying generator (registry.Manifest.ExportTypeScript) is the
// single source of truth — this method just wires the manifest +
// stdout chatter.
func (a *App) ToolsExportTypeScript(outDir string) error {
	manifest := core.BuildManifest()
	written, err := manifest.ExportTypeScript(outDir)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "✓ wrote %d files to %s/\n", len(written), outDir)
	for _, f := range written {
		fmt.Fprintf(a.Stdout, "  %s\n", f)
	}
	fmt.Fprintf(a.Stdout, "\nA code-mode host can `import { Bash, Read, Edit } from %q` instead of\n", outDir)
	fmt.Fprintf(a.Stdout, "round-tripping every tools/call. Re-run after a manifest change to refresh.\n")
	return nil
}

// validateSelector enforces the ADR-006 charset rules at the user's first
// touchpoint. We do not yet implement tag:/group:/profile-aware selectors;
// rejecting them up front prevents silent no-ops.
func validateSelector(s string) error {
	if s == "" {
		return errors.New("selector is empty")
	}
	if strings.HasPrefix(s, "tag:") || strings.HasPrefix(s, "group:") {
		return fmt.Errorf("selector %q uses tag:/group: prefix — supported in v0.3, not yet wired", s)
	}
	// Cheap validation: tools must be either a PascalCase core tool name OR
	// `<instance>.<tool>` with kebab-case instance and snake_case tool.
	if isCoreLooking(s) {
		return nil
	}
	dot := strings.IndexByte(s, '.')
	if dot < 0 {
		return fmt.Errorf("selector %q is not a known shape (expected PascalCase core tool name or `<instance>.<tool>`)", s)
	}
	instance, tool := s[:dot], s[dot+1:]
	if !isKebab(instance) {
		return fmt.Errorf("selector %q: instance %q must be kebab-case [a-z0-9-]+", s, instance)
	}
	if !isSnake(tool) {
		return fmt.Errorf("selector %q: tool %q must be snake_case [a-z0-9_]+", s, tool)
	}
	return nil
}

func isCoreLooking(s string) bool {
	if s == "" {
		return false
	}
	if c := s[0]; c < 'A' || c > 'Z' {
		return false
	}
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func isKebab(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

func isSnake(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func quoteIfDot(s string) string {
	if strings.Contains(s, ".") {
		return `"` + s + `"`
	}
	return s
}

const topUsage = `clawtool — canonical tool layer for AI coding agents

Usage:
  clawtool serve            Run as an MCP server over stdio (default).
  clawtool serve --listen :8080 [--token-file <path>]
                            Run the HTTP gateway. Bearer-token auth at the
                            edge. Endpoints: /v1/health, /v1/agents,
                            /v1/send_message. TLS via reverse proxy.
  clawtool serve init-token [<path>]
                            Generate + write a fresh listener token.
  clawtool init [--yes]     Interactive wizard: pick recipes per category
                            (license, dependabot, release-please, etc.) and
                            inject them into the current repo. --yes / non-TTY:
                            apply Stable defaults non-interactively.
  clawtool tools list       List known tools and their resolved enabled state.
  clawtool tools export-typescript [--output <dir>]
                            Emit one .ts file per registered tool plus an
                            index.ts barrel. A code-mode host can then
                            'import { Bash, Read, ... }' and write code
                            instead of round-tripping each tools/call --
                            see Anthropic's "Code execution with MCP".
  clawtool tools enable <selector>
  clawtool tools disable <selector>
  clawtool tools status <selector>
  clawtool source add <name> [--as <instance>]
                            Add a source from the built-in catalog (e.g. github,
                            slack, postgres). See: clawtool source --help.
  clawtool source list      List configured sources and auth status.
  clawtool source catalog   Browse the built-in catalog of MCP servers.
  clawtool source remove <instance>
  clawtool source set-secret <instance> <KEY> [--value <v>]
  clawtool source check     Verify required credentials per source.
  clawtool agents claim <agent>
                            Disable the agent's native Bash/Read/Edit/Write/
                            Grep/Glob/WebFetch/WebSearch so only the
                            mcp__clawtool__* equivalents are exposed.
  clawtool agents release <agent>
  clawtool agents status [<agent>]
  clawtool agents list      List known agent adapters.
  clawtool bridge add <family>
                            Install the canonical bridge for the family
                            (codex / opencode / gemini). Wraps the upstream's
                            published Claude Code plugin or built-in
                            subcommand — clawtool never re-implements
                            the bridge.
  clawtool bridge list      Show installed bridges + status.
  clawtool bridge upgrade <family>
                            Re-run the install (idempotent; pulls the
                            latest plugin version).
  clawtool send [--agent <i>] [--session <sid>] [--model <m>] [--format <f>] "<prompt>"
                            Stream a prompt to the resolved agent's
                            upstream CLI. Output streams to stdout
                            verbatim. Resolution: --agent flag >
                            CLAWTOOL_AGENT env > sticky default >
                            single-instance fallback.
  clawtool send --list      Print the supervisor's agent registry.
  clawtool autonomous "<goal>" [--agent <i>] [--max-iterations N] [--cooldown <d>] [--dry-run]
                            Self-paced single-message dev loop. The CLI
                            builds a session prompt from <goal> + iteration
                            metadata and dispatches it to the chosen BIAM
                            peer until the agent emits DONE: <summary>,
                            --max-iterations is hit, or Ctrl-C. Hint: pair
                            with OnboardStatus + InitApply for "one
                            message, full pipeline".
  clawtool bootstrap [--agent <family>] [--workdir <path>] [--dry-run]
                            Zero-click onboarding. Spawns the chosen BIAM
                            peer's CLI with its elevation flag, pipes a
                            bootstrap prompt that asks the agent to run
                            OnboardWizard + InitApply via MCP, and streams
                            the agent's reply back. Default agent: claude.
                            Pair with 'clawtool install' for hands-off
                            setup of a fresh repo.
  clawtool agent use <i>    Set the sticky default agent (singular
                            'agent' = relay runtime; plural 'agents' =
                            adapter ownership for native tool replacement).
  clawtool agent which      Show the currently-resolved default agent.
  clawtool agent unset      Clear the sticky default.
  clawtool portal add/list/remove/use/which/unset/ask
                            Manage saved web-UI targets. A portal pairs a
                            base URL with login cookies + selectors + a
                            'response done' predicate. Full guide:
                            docs/portals.md.
  clawtool worktree list    List isolated worktrees with marker info.
  clawtool worktree show <taskID>
                            Print path + marker JSON for one worktree.
  clawtool worktree gc [--min-age 24h]
                            Reap orphan worktrees (dead PID + age cutoff).
  clawtool recipe list [--category <c>]
                            List project-setup recipes (governance/commits/
                            release/ci/quality/supply-chain/knowledge/agents/
                            runtime) and their state in the current repo.
  clawtool recipe status [<name>]
                            Detect output for one recipe or all of them.
  clawtool apm import [<path>] [--dry-run] [--repo <p>]
                            Import a microsoft/apm manifest (apm.yml). MCP
                            servers are registered via 'clawtool source add';
                            skills + playbooks are recorded in
                            <repo>/.clawtool/apm-imported-manifest.toml for
                            phase-2 recipe wiring.
  clawtool playbook list-archon [--dir <p>] [--format <text|json>]
                            List Archon (coleam00/Archon) DAG workflows
                            under <p>/.archon/workflows/. Read-only:
                            phase 1 parses and surfaces, phase 2 will
                            wire execution.
  clawtool recipe apply <name> [key=value ...]
                            Inject the recipe into the current working
                            directory. Examples:
                              clawtool recipe apply license holder="Jane Doe"
                              clawtool recipe apply codeowners owners=@me,@team
                              clawtool recipe apply dependabot
  clawtool doctor           One-command diagnostic — surveys binary,
                            agents, sources, and recipes; suggests fixes.
  clawtool skill new <name> --description "..." [--triggers "a,b,c"] [--local] [--force]
                            Scaffold an Agent Skill folder per the
                            agentskills.io standard (SKILL.md + scripts/
                            references/ assets/). MCP equivalent:
                            mcp__clawtool__SkillNew.
  clawtool mcp new <project> [--output <dir>] [--yes]
                            Scaffold a new MCP server (Go / Python /
                            TypeScript). mcp = MCP server source code;
                            skill = Agent Skill folder.
  clawtool mcp list / run / build / install
                            Walk / run / compile / register MCP server
                            projects. See 'clawtool mcp --help'.
  clawtool skill list       Enumerate installed skills (~/.claude/skills
                            and ./.claude/skills).
  clawtool skill path [<name>]
                            Print the on-disk path of a skill.
  clawtool uninstall [--yes] [--dry-run] [--purge-binary] [--keep-config]
                            Remove every artifact clawtool drops on the host
                            (config, secrets, caches, data, BIAM, sticky
                            pointers). Useful when test installs pile up.
  clawtool sandbox list/show/doctor/run
                            Sandbox profiles for dispatch isolation.
                            Per-profile [sandboxes.X] in config.toml.
                            Engines: bwrap (Linux), sandbox-exec (macOS),
                            docker (anywhere fallback).
  clawtool star [--no-oauth] [--owner <o> --repo <r>]
                            Star cogitave/clawtool on GitHub (or a
                            different repo with overrides). Walks you
                            through GitHub's OAuth Device Flow: prints
                            a short user-code, opens the verification
                            page in your browser, polls until you
                            authorise, then PUTs the star via the
                            documented authenticated REST endpoint.
                            --no-oauth opens the repo's star page so
                            you can click Star yourself instead.
                            Token cached in ~/.config/clawtool/secrets.toml
                            (mode 0600); revoke any time at
                            github.com/settings/applications.
  clawtool telemetry status / on / off
                            Show or flip the anonymous-telemetry opt-in
                            stored in config.toml. Allow-listed payload
                            (command + version + duration + exit_code +
                            agent family + recipe/engine/bridge names);
                            never prompts, paths, secrets, env values.
                            Takes effect at next CLI / daemon start.
  clawtool version          Print the build version.
  clawtool help             Show this help.

Selector forms:
  Bash                      A core tool (PascalCase).
  github-personal.create_issue
                            A sourced tool: <instance>.<tool>. Instance is
                            kebab-case, tool is snake_case.
`

const toolsUsage = `Usage:
  clawtool tools list
  clawtool tools enable <selector>
  clawtool tools disable <selector>
  clawtool tools status <selector>
`
