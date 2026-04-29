// Package server starts the clawtool MCP server.
//
// Per ADR-004, clawtool exposes itself as one MCP server over stdio.
// Per ADR-006, core tools use PascalCase names (Bash, Read, Edit, ...).
// Per ADR-008, configured sources spawn as child MCP servers and their
// tools are aggregated under `<instance>__<tool>` wire names.
//
// Boot order on every `clawtool serve`:
//  1. Load config + secrets.
//  2. Build sources.Manager and start each configured source. Failures on
//     individual sources are non-fatal; their tools just don't show up.
//  3. Build a search.Index from descriptors of every tool we plan to
//     register: enabled core tools + ToolSearch + aggregated source tools.
//     This index powers the ToolSearch primitive — see ADR-005 for why
//     search-first is the prerequisite that lets a 50+ tool catalog scale.
//  4. Register all tools on the parent MCP server. ToolSearch closes over
//     the index reference; aggregated source-tool handlers route via the
//     manager.
package server

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/hooks"
	"github.com/cogitave/clawtool/internal/observability"
	"github.com/cogitave/clawtool/internal/sandbox/worker"
	"github.com/cogitave/clawtool/internal/search"
	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/cogitave/clawtool/internal/sources"
	"github.com/cogitave/clawtool/internal/telemetry"
	"github.com/cogitave/clawtool/internal/tools/core"
	"github.com/cogitave/clawtool/internal/tools/registry"
	"github.com/cogitave/clawtool/internal/version"
	"github.com/mark3labs/mcp-go/server"

	// Pull every recipe subpackage's init() so the setup registry
	// is populated before RegisterRecipeTools wires the MCP surface.
	_ "github.com/cogitave/clawtool/internal/setup/recipes"
)

// ServeStdio runs clawtool as an MCP server speaking over stdio. It blocks
// until stdin closes (the conventional MCP shutdown signal) or an
// unrecoverable error occurs.
func ServeStdio(ctx context.Context) error {
	bootedAt := time.Now()
	s, mgr, _, _, err := buildMCPServer(ctx, "stdio")
	if err != nil {
		return err
	}
	defer mgr.Stop()
	err = server.ServeStdio(s)
	// Always emit on_server_stop so user log/telemetry hooks see the
	// shutdown even if ServeStdio errors out.
	if mgr := hooks.Get(); mgr != nil {
		_ = mgr.Emit(ctx, hooks.EventOnServerStop, map[string]any{
			"version": version.Resolved(),
			"pid":     os.Getpid(),
		})
	}
	// Telemetry: server.stop with uptime + outcome. Pairs with
	// the server.start event the boot path emits. transport=stdio
	// surfaces the respawn-per-call pattern in PostHog when a host
	// is mis-claimed in stdio mode (the spam-debug case operator
	// caught at v0.22.22).
	if tc := telemetry.Get(); tc != nil && tc.Enabled() {
		outcome := "success"
		if err != nil {
			outcome = "error"
		}
		tc.Track("server.stop", map[string]any{
			"version":      version.Resolved(),
			"duration_ms":  time.Since(bootedAt).Milliseconds(),
			"outcome":      outcome,
			"transport":    "stdio",
			"$session_end": true,
		})
		_ = tc.Close()
	}
	if err != nil {
		return fmt.Errorf("stdio serve: %w", err)
	}
	return nil
}

// buildMCPServer wires the full MCP server (config, secrets, sources,
// search index, every tool registration). Returned to the caller so a
// transport other than stdio (e.g. the Phase 2 HTTP gateway) can run
// the same server. The Manager is returned alongside so callers can
// Stop() it on shutdown.
func buildMCPServer(ctx context.Context, transport string) (*server.MCPServer, *sources.Manager, config.Config, *secrets.Store, error) {
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		return nil, nil, config.Config{}, nil, fmt.Errorf("load config: %w", err)
	}
	sec, err := secrets.LoadOrEmpty(secrets.DefaultPath())
	if err != nil {
		return nil, nil, cfg, nil, fmt.Errorf("load secrets: %w", err)
	}

	// Observability — wires OTLP/HTTP exporter and registers the
	// process-wide observer agents.NewSupervisor picks up
	// automatically. Disabled-by-default: zero overhead when off.
	// Init failures are logged but non-fatal — clawtool keeps serving.
	obs := observability.New()
	if err := obs.Init(ctx, cfg.Observability); err != nil {
		fmt.Fprintf(os.Stderr, "clawtool: observability init failed (continuing without traces): %v\n", err)
	} else if cfg.Observability.Enabled {
		agents.SetGlobalObserver(obs)
		fmt.Fprintf(os.Stderr, "clawtool: observability enabled (exporter=%s)\n", cfg.Observability.ExporterURL)
	}

	// Auto-lint guardrails (ADR-014 T2). Default = on; explicit
	// AutoLint.Enabled = false flips the package-level flag in
	// internal/tools/core. The Runner detects the linter binary
	// per-call so missing tools (e.g. ruff on a Go-only repo) are a
	// silent skip, not an error.
	if cfg.AutoLint.Enabled != nil {
		core.SetAutoLintEnabled(*cfg.AutoLint.Enabled)
	}

	// Hooks subsystem (F3). Register the process-wide manager once
	// so every callsite can emit without threading a handle through.
	hookMgr := hooks.New(cfg.Hooks)
	hooks.SetGlobal(hookMgr)
	_ = hookMgr.Emit(ctx, hooks.EventOnServerStart, map[string]any{
		"version": version.Resolved(),
		"pid":     os.Getpid(),
	})

	// Telemetry (F5). Anonymous, opt-in. Env-var kill switch always
	// wins over config so an operator can disable temporarily without
	// editing files.
	if !telemetry.SilentDisabled() {
		tc := telemetry.New(cfg.Telemetry)
		telemetry.SetGlobal(tc)
		tc.Track("server.start", map[string]any{
			"version":        version.Resolved(),
			"transport":      transport,
			"$session_start": true,
		})
		// Fresh-host install event — fires once per host (marker
		// file lives at $XDG_DATA_HOME/clawtool/install-emitted).
		// Subsequent daemon boots are no-ops. Source attribution
		// comes from $CLAWTOOL_INSTALL_METHOD set by install.sh /
		// brew formula / go-install wrapper at install time;
		// missing maps to "unknown" so we still get the event.
		telemetry.EmitInstallOnce(tc, version.Resolved())
	}

	// BIAM Phase 1 (ADR-015): bring up the per-instance identity +
	// SQLite store, register a process-wide async runner so
	// `mcp__clawtool__SendMessage --bidi` and `clawtool send --async`
	// can return task IDs immediately. Init failures are logged but
	// non-fatal (synchronous send keeps working).
	id, err := biam.LoadOrCreateIdentity("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawtool: biam identity init failed: %v\n", err)
	} else if store, err := biam.OpenStore(""); err != nil {
		fmt.Fprintf(os.Stderr, "clawtool: biam store init failed: %v\n", err)
	} else {
		// Sweep orphan tasks left behind by a previous daemon
		// crash. Pending older than 1 minute is presumed dead
		// (state machine flips pending → active in
		// milliseconds when the runner picks it up). Active
		// older than 1 hour is the hard ceiling that matches
		// TaskNotify's max wait — beyond that, the upstream
		// agent is almost certainly hung and the row is just
		// noise in `task list`.
		if n, rerr := store.ReapStaleTasks(ctx, time.Minute, time.Hour); rerr != nil {
			fmt.Fprintf(os.Stderr, "clawtool: biam reap stale tasks: %v\n", rerr)
		} else if n > 0 {
			fmt.Fprintf(os.Stderr, "clawtool: biam reaped %d orphan task(s) from a prior daemon\n", n)
		}
		runner := biam.NewRunner(store, id, func(ctx context.Context, instance, prompt string, opts map[string]any) (io.ReadCloser, error) {
			// Cast through the package var to avoid an import cycle.
			return agents.NewSupervisor().Send(ctx, instance, prompt, opts)
		})
		agents.SetGlobalBiamRunner(runner)
		core.SetBiamStore(store)

		// Shutdown order matters: cancel the runner FIRST so its
		// in-flight goroutines stop touching the store, then
		// close the store. Ctx cancellation only fires Stop here;
		// the build-flow's defer mgr.Stop() handles source-process
		// teardown separately. Without runner.Stop, in-flight
		// dispatches keep writing during teardown and either race
		// store.Close (nil-deref pre-d96d23b) or get killed by
		// process exit, leaving rows stuck `active`.
		go func() {
			<-ctx.Done()
			runner.Stop()
			_ = store.Close()
		}()

		// The next three goroutines (watchsocket, dispatchsocket,
		// version poller) are daemon-lifetime services. Running
		// them inside short-lived stdio respawns is a triple
		// problem: (1) Unix sockets clobber any other clawtool
		// daemon's bind, (2) the version poller's first tick fires
		// CheckForUpdate immediately, so every stdio respawn emits
		// a `clawtool.update_check` event — operator caught this
		// as "telemetry spam" against PostHog (~2.2 events/sec
		// against a host that mis-claimed clawtool over stdio MCP
		// instead of dialing the persistent HTTP daemon), (3) goroutine
		// teardown is implicit on process exit, which is cheap but
		// pointless work in a 400ms-lived child. Gate them on
		// transport=="http" so only the long-running daemon path
		// runs them. stdio child processes still serve every MCP
		// tool call correctly via the parent server.MCPServer; they
		// just don't spam the daemon-only side channels.
		if transport == "http" {
			// Push-based task watch — Unix socket peer of the in-process
			// WatchHub. `clawtool task watch` dials this and ditches
			// SQLite polling. Failures are non-fatal: watchers fall back
			// to polling automatically when the socket is missing.
			go func() {
				if err := biam.ServeWatchSocket(ctx, store, biam.Watch, ""); err != nil {
					fmt.Fprintf(os.Stderr, "clawtool: biam watchsocket: %v\n", err)
				}
			}()

			// Dispatch socket — sister of the watch socket. Lets
			// `clawtool send --async` (a separate CLI process) hand
			// the dispatch off to THIS daemon's runner so the
			// goroutine that drains codex/gemini/etc. lives in this
			// process. Result: every StreamFrame the runner
			// broadcasts hits this daemon's WatchHub, which is what
			// the orchestrator's socket subscribers read. Without
			// this, CLI-side dispatches leak frames into a separate
			// process's hub and the orchestrator stays empty.
			go func() {
				if err := biam.ServeDispatchSocket(ctx, runner, ""); err != nil {
					fmt.Fprintf(os.Stderr, "clawtool: biam dispatchsocket: %v\n", err)
				}
			}()

			// Update poller — hourly GitHub-releases probe. On a
			// transition into "update available" the poller pushes a
			// SystemNotification onto the WatchHub; orchestrator /
			// dashboard / `task watch` subscribers render an inline
			// banner immediately. SessionStart still injects the
			// same banner into the very first Claude turn, but the
			// push channel keeps already-open sessions in the loop
			// without re-checking on every prompt.
			go func() {
				pub := func(kind, severity, title, body, actionHint string) {
					biam.Watch.BroadcastSystem(biam.SystemNotification{
						Kind:       kind,
						Severity:   severity,
						Title:      title,
						Body:       body,
						ActionHint: actionHint,
						TS:         time.Now().UTC(),
					})
				}
				track := func(outcome string) {
					if tc := telemetry.Get(); tc != nil && tc.Enabled() {
						tc.Track("clawtool.update_check", map[string]any{
							"version":        version.Resolved(),
							"update_outcome": outcome,
							"transport":      "http",
						})
					}
				}
				poller := version.NewPoller(pub, version.PollerConfig{}, track)
				poller.Run(ctx)
			}()
		}
	}

	// Sandbox-worker wire-up (ADR-029 phase 2). When config sets
	// sandbox_worker.mode != "off", we instantiate the daemon-side
	// client and register it process-wide. Bash / Read / Edit /
	// Write tool handlers consult worker.Global() per call and
	// route through the worker when present (host fallback when
	// nil). Failures here are non-fatal — the daemon keeps serving
	// with host execution.
	wireSandboxWorker(cfg)

	mgr := sources.NewManager(cfg, sec)
	if err := mgr.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "clawtool: some sources failed to start: %v\n", err)
	}

	// Build the search-index descriptors before any registration so the
	// final corpus reflects what we're actually about to serve.
	docs := buildIndexDocs(cfg, mgr)
	idx, err := search.Build(docs)
	if err != nil {
		mgr.Stop()
		return nil, nil, cfg, sec, fmt.Errorf("build search index: %w", err)
	}

	// version.Resolved() picks the goreleaser-baked ldflags string when
	// present, then debug.ReadBuildInfo, then the const. Pre-fix
	// the const escaped through to MCP `serverInfo.version` and
	// `/v1/health` JSON, so a binary built from main showed an
	// older const value to every host. Caught at v0.22.23 during a
	// Docker e2e probe (host saw "0.21.7" in /v1/health while CLI
	// said 0.22.23).
	s := server.NewMCPServer(
		version.Name,
		version.Resolved(),
		server.WithToolCapabilities(true),
		server.WithLogging(),
	)

	// Manifest-driven registration (#173 Step 4). The 28 hand-
	// maintained core.RegisterX(s) calls that used to live here
	// collapsed into a single Apply walk over the typed
	// internal/tools/core.BuildManifest() — see ADR-005 / ADR-006
	// for the gating policy and docs/feature-shipping-contract.md
	// for the four-plane invariant the registry enforces.
	//
	// Multi-tool wrappers (Recipe / Bridge / Agent / Task / Portal
	// / Mcp / Sandbox) follow the "first spec invokes" pattern:
	// each wrapper's first ToolSpec carries the Register fn that
	// registers the whole bundle; companion specs (RecipeStatus
	// after RecipeList, etc.) have Register=nil and Apply skips
	// them silently.
	manifest := core.BuildManifest()
	manifest.Apply(s, registry.Runtime{Index: idx, Secrets: sec},
		func(name string) bool { return cfg.IsEnabled(name).Enabled })

	// Portal aliases are dynamic (one per configured portal) so
	// they can't fit the static manifest shape — register
	// imperatively. ADR-018.
	core.RegisterPortalAliases(s, cfg)

	// Aggregated source tools — one entry per (running instance × tool),
	// already named in wire form `<instance>__<tool>`.
	for _, st := range mgr.AggregatedTools() {
		s.AddTool(st.Tool, st.Handler)
	}
	return s, mgr, cfg, sec, nil
}

// wireSandboxWorker reads cfg.SandboxWorker and registers a
// process-wide worker.Client if Mode != "off". Tool handlers see
// it via worker.Global(); nil = fall back to host. Mirror of
// observability + biam wiring above.
func wireSandboxWorker(cfg config.Config) {
	mode := cfg.SandboxWorker.Mode
	if mode == "" || mode == "off" {
		worker.SetGlobal(nil)
		return
	}
	url := cfg.SandboxWorker.URL
	if url == "" {
		fmt.Fprintln(os.Stderr,
			"clawtool: sandbox_worker.mode != off but URL empty; falling back to host execution")
		worker.SetGlobal(nil)
		return
	}
	tokenPath := cfg.SandboxWorker.TokenFile
	if tokenPath == "" {
		tokenPath = worker.DefaultTokenPath()
	}
	tok, err := worker.LoadToken(tokenPath)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"clawtool: sandbox_worker token load failed (%v); falling back to host. Generate one via `clawtool sandbox-worker --init-token`\n",
			err)
		worker.SetGlobal(nil)
		return
	}
	worker.SetGlobal(worker.NewClient(url, tok))
	fmt.Fprintf(os.Stderr,
		"clawtool: sandbox-worker wired (mode=%s, url=%s)\n", mode, url)
}

// buildIndexDocs flattens the manifest into search.Doc entries
// for the bleve indexer + appends the dynamic per-source-instance
// aggregated tools.
//
// Gating is delegated to manifest.SearchDocs(pred) where pred
// reads cfg.IsEnabled(spec.Gate). Empty-Gate specs always pass —
// keeps always-on tools (Verify, SemanticSearch, Recipe*, …)
// indexed even when the operator disables every gateable tool.
//
// The Bash companions (BashOutput, BashKill) are gated on "Bash"
// at manifest construction time (see internal/tools/core/manifest.go),
// so this function doesn't need a separate alias map any more.
func buildIndexDocs(cfg config.Config, mgr *sources.Manager) []search.Doc {
	docs := core.BuildManifest().SearchDocs(func(gate string) bool {
		return cfg.IsEnabled(gate).Enabled
	})

	// Aggregated source tools. We index name + description from the child's
	// own MCP advertisement — that's the canonical source of truth.
	for _, st := range mgr.AggregatedTools() {
		instance, _, _ := sources.SplitWireName(st.Tool.Name)
		docs = append(docs, search.Doc{
			Name:        st.Tool.Name,
			Description: st.Tool.Description,
			Type:        "sourced",
			Instance:    instance,
		})
	}
	return docs
}
