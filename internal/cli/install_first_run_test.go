package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// installStubs captures the calls a test wants to assert on, and
// installs deterministic replacements for the package-level seams
// (installLookPath, installDispatcher, installDaemonStarter,
// installPeerRegister, installInitAll, installTmuxRunner,
// installAttachExec). Mirrors the bootstrap_test pattern
// (stubBootstrap).
type installStubs struct {
	mu              sync.Mutex
	hostsPresent    map[string]bool // which binaries LookPath should "find"
	dispatchCalls   [][]string      // every argv the dispatcher was asked to run
	daemonStarted   bool
	daemonPort      int
	daemonErr       error
	peerCalls       []peerCall // every (backend, tmuxPane) pair
	initAllCalled   bool
	initAllApplied  int
	initAllErr      error
	dispatchExitMap map[string]int // first-arg → rc override (default 0)
	// tmuxCalls is the ordered argv list installTmuxRunner saw.
	tmuxCalls [][]string
	// tmuxHasSession controls what `has-session` returns: true →
	// rc 0 (session exists, skip new-session), false → rc 1.
	tmuxHasSession bool
	// tmuxNextPaneID dispenses a synthetic pane id per
	// new-window call so tests can assert it was wired into the
	// peer register payload. %0, %1, %2…
	tmuxNextPaneID int
	// attachCalls records every session name installAttachExec
	// was invoked with, in order. Empty when --attach wasn't
	// passed.
	attachCalls []string
	// subprocCalls is a flat ordered log of every subprocess-shaped
	// call the install verb makes through the seams: dispatch
	// (joined argv prefixed "dispatch:"), tmux (prefixed "tmux:"),
	// and attach (prefixed "tmux-attach:"). Lets the attach test
	// assert ordering against everything else without juggling
	// three slices.
	subprocCalls []string
}

// peerCall is a (backend, tmux pane) pair captured from the
// installPeerRegister stub. Empty pane = the pre-step-7.5 shell
// registration; non-empty = a step-7.5 spawned-agent registration.
type peerCall struct {
	backend  string
	tmuxPane string
}

func newInstallStubs() *installStubs {
	return &installStubs{
		hostsPresent:    map[string]bool{},
		dispatchExitMap: map[string]int{},
	}
}

// installStubsApply rebinds every install_first_run.go test seam to
// the stubs and registers a t.Cleanup to restore the originals.
func installStubsApply(t *testing.T, s *installStubs) {
	t.Helper()
	prevLook := installLookPath
	prevDispatch := installDispatcher
	prevDaemon := installDaemonStarter
	prevPeer := installPeerRegister
	prevInit := installInitAll
	prevTmux := installTmuxRunner
	prevAttach := installAttachExec

	installLookPath = func(bin string) (string, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.hostsPresent[bin] {
			return "/usr/bin/" + bin, nil
		}
		return "", errors.New("not found")
	}
	installDispatcher = func(_ *App, argv []string) (int, string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.dispatchCalls = append(s.dispatchCalls, append([]string(nil), argv...))
		s.subprocCalls = append(s.subprocCalls, "dispatch:"+strings.Join(argv, " "))
		key := ""
		if len(argv) > 0 {
			key = argv[0]
			if len(argv) > 1 {
				key = argv[0] + " " + argv[1]
			}
		}
		if rc, ok := s.dispatchExitMap[key]; ok {
			return rc, ""
		}
		return 0, ""
	}
	installDaemonStarter = func(_ context.Context) (int, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.daemonStarted = true
		if s.daemonErr != nil {
			return 0, s.daemonErr
		}
		port := s.daemonPort
		if port == 0 {
			port = 47823
		}
		return port, nil
	}
	installPeerRegister = func(_ *App, backend, tmuxPane string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.peerCalls = append(s.peerCalls, peerCall{backend: backend, tmuxPane: tmuxPane})
		return nil
	}
	installInitAll = func(_ *App, _ string) (int, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.initAllCalled = true
		return s.initAllApplied, s.initAllErr
	}
	installTmuxRunner = func(args []string) (string, int, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.tmuxCalls = append(s.tmuxCalls, append([]string(nil), args...))
		s.subprocCalls = append(s.subprocCalls, "tmux:"+strings.Join(args, " "))
		// has-session: rc 0 when tmuxHasSession, else 1.
		if len(args) >= 1 && args[0] == "has-session" {
			if s.tmuxHasSession {
				return "", 0, nil
			}
			return "", 1, nil
		}
		// new-window -P -F '#{pane_id}': dispense a synthetic id.
		if len(args) >= 1 && args[0] == "new-window" {
			id := s.tmuxNextPaneID
			s.tmuxNextPaneID++
			return paneIDFor(id) + "\n", 0, nil
		}
		// new-session, kill-session, anything else: rc 0.
		return "", 0, nil
	}
	installAttachExec = func(session string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.attachCalls = append(s.attachCalls, session)
		s.subprocCalls = append(s.subprocCalls, "tmux-attach:"+session)
		return nil
	}

	t.Cleanup(func() {
		installLookPath = prevLook
		installDispatcher = prevDispatch
		installDaemonStarter = prevDaemon
		installPeerRegister = prevPeer
		installInitAll = prevInit
		installTmuxRunner = prevTmux
		installAttachExec = prevAttach
	})
}

// paneIDFor renders the synthetic pane id %0, %1, %2…
func paneIDFor(n int) string {
	return "%" + itoa(n)
}

// TestInstall_DryRun: --dry-run prints the 10-step plan and does
// NO side effects (no daemon spawn, no peer register, no init).
func TestInstall_DryRun(t *testing.T) {
	stubs := newInstallStubs()
	stubs.hostsPresent["claude"] = true
	stubs.hostsPresent["codex"] = true
	installStubsApply(t, stubs)

	app, out, _, _ := newApp(t)
	rc := app.Run([]string{"install", "--dry-run"})
	if rc != 0 {
		t.Fatalf("install --dry-run rc = %d, want 0", rc)
	}
	got := out.String()
	for _, want := range []string{
		"clawtool install — dry-run plan",
		"1. daemon health",
		"2. host detection",
		"3. bridge install",
		"4. agent claim",
		"5. MCP config",
		"6. hooks install",
		"7. peer register",
		"8. init --all",
		"9. verify",
		"10. exit",
		"claude",
		"codex",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run plan missing %q\n--- got ---\n%s", want, got)
		}
	}
	if stubs.daemonStarted {
		t.Errorf("dry-run must NOT spawn the daemon")
	}
	if len(stubs.peerCalls) != 0 {
		t.Errorf("dry-run must NOT register peer; calls=%v", stubs.peerCalls)
	}
	if stubs.initAllCalled {
		t.Errorf("dry-run must NOT run init --all")
	}
}

// TestInstall_DaemonAlreadyRunning: when the daemon starter returns
// a port without an error, step 1 logs success and the run proceeds
// (idempotent — the starter wraps daemon.Ensure which is itself
// no-op when healthy).
func TestInstall_DaemonAlreadyRunning(t *testing.T) {
	stubs := newInstallStubs()
	stubs.daemonPort = 51234
	// No host CLIs detected — step 2 logs warn, but the run still
	// completes. Skip the optional init step since the test cwd
	// isn't a git repo (the seam returns 0 applied).
	installStubsApply(t, stubs)

	app, out, errb, _ := newApp(t)
	rc := app.Run([]string{"install", "--skip-init"})
	if rc != 0 {
		t.Fatalf("install rc = %d, want 0\nstderr=%s\nstdout=%s", rc, errb.String(), out.String())
	}
	if !stubs.daemonStarted {
		t.Errorf("step 1 should have called the daemon starter")
	}
	final := out.String()
	if !strings.Contains(final, "clawtool kuruldu") {
		t.Errorf("final stdout must contain the kuruldu summary, got: %q", final)
	}
	if !strings.Contains(final, "127.0.0.1:51234") {
		t.Errorf("summary must contain the daemon port; got: %q", final)
	}
	if !strings.Contains(errb.String(), "step 1.") {
		t.Errorf("stderr must show step-1 line; got: %q", errb.String())
	}
}

// TestInstall_NoHostsDetected: zero detected agents → daemon starts,
// step 2 warns, exit succeeds with the kuruldu summary showing 0
// agents claimed.
func TestInstall_NoHostsDetected(t *testing.T) {
	stubs := newInstallStubs()
	stubs.daemonPort = 47900
	installStubsApply(t, stubs)

	app, out, errb, _ := newApp(t)
	rc := app.Run([]string{"install", "--skip-init"})
	if rc != 0 {
		t.Fatalf("install rc = %d, want 0\nstderr=%s\nstdout=%s", rc, errb.String(), out.String())
	}
	final := out.String()
	for _, want := range []string{
		"clawtool kuruldu",
		"0 agent(s) registered",
		"127.0.0.1:47900",
	} {
		if !strings.Contains(final, want) {
			t.Errorf("final summary missing %q\n--- got ---\n%s", want, final)
		}
	}
	if !strings.Contains(errb.String(), "no agent CLIs found on PATH") {
		t.Errorf("step 2 should warn about no hosts; got stderr: %q", errb.String())
	}
}

// TestInstall_HostDetectionDispatchesHooks: with claude+codex on
// PATH, the dispatcher must receive the bridge-add (codex), agents-
// claim (both), and hooks-install (both) invocations. Verifies the
// step 3/4/6 routing.
func TestInstall_HostDetectionDispatchesHooks(t *testing.T) {
	stubs := newInstallStubs()
	stubs.daemonPort = 51000
	stubs.hostsPresent["claude"] = true
	stubs.hostsPresent["codex"] = true
	installStubsApply(t, stubs)

	app, _, errb, _ := newApp(t)
	rc := app.Run([]string{"install", "--skip-init"})
	if rc != 0 {
		t.Fatalf("install rc = %d, want 0\nstderr=%s", rc, errb.String())
	}

	// The dispatcher should have been called with each of these
	// argv prefixes. Order is not strictly asserted (claude vs.
	// codex per-step) — only presence.
	want := map[string]bool{
		"bridge add codex":          false,
		"agents claim claude-code":  false,
		"agents claim codex":        false,
		"hooks install claude-code": false,
		"hooks install codex":       false,
	}
	for _, argv := range stubs.dispatchCalls {
		key := strings.Join(argv, " ")
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("dispatcher never received %q. all calls:\n%s", k, formatDispatchCalls(stubs.dispatchCalls))
		}
	}

	// Step 4 should NOT dispatch agents-claim for claude when
	// claude is NOT on PATH — but when it IS, it dispatches with
	// the adapter name "claude-code". This is asserted above.

	// Step 7 should fire peer register with claude-code (highest
	// priority among detected hosts).
	if len(stubs.peerCalls) != 1 {
		t.Fatalf("peer register call count = %d, want 1; calls=%v", len(stubs.peerCalls), stubs.peerCalls)
	}
	if stubs.peerCalls[0].backend != "claude-code" {
		t.Errorf("peer backend = %q, want claude-code", stubs.peerCalls[0].backend)
	}
}

// TestInstall_OpencodeClaimSkipped: when opencode is detected, step 4
// must NOT dispatch agents-claim for it (opencode mcp add path is
// broken upstream — recorded as a known recovery case).
func TestInstall_OpencodeClaimSkipped(t *testing.T) {
	stubs := newInstallStubs()
	stubs.daemonPort = 51100
	stubs.hostsPresent["opencode"] = true
	installStubsApply(t, stubs)

	app, _, errb, _ := newApp(t)
	rc := app.Run([]string{"install", "--skip-init"})
	if rc != 0 {
		t.Fatalf("install rc = %d, want 0\nstderr=%s", rc, errb.String())
	}
	for _, argv := range stubs.dispatchCalls {
		if strings.Join(argv, " ") == "agents claim opencode" {
			t.Errorf("step 4 must skip opencode claim; dispatcher saw it")
		}
	}
	if !strings.Contains(errb.String(), "skipped") {
		t.Errorf("stderr must mention opencode skip; got: %q", errb.String())
	}
}

// TestInstall_DaemonFailureAborts: if step 1's daemon starter
// returns an error, the verb must exit 1 without continuing.
func TestInstall_DaemonFailureAborts(t *testing.T) {
	stubs := newInstallStubs()
	stubs.daemonErr = errors.New("port bind failed")
	installStubsApply(t, stubs)

	app, out, errb, _ := newApp(t)
	rc := app.Run([]string{"install"})
	if rc != 1 {
		t.Fatalf("install rc = %d, want 1", rc)
	}
	if strings.Contains(out.String(), "clawtool kuruldu") {
		t.Errorf("daemon failure must NOT print the kuruldu summary")
	}
	if !strings.Contains(errb.String(), "aborting") {
		t.Errorf("stderr should explain the abort; got: %q", errb.String())
	}
	if stubs.initAllCalled {
		t.Errorf("init --all must not run after daemon failure")
	}
}

// TestInstall_HelpFlag: --help prints usage to stdout and exits 0.
func TestInstall_HelpFlag(t *testing.T) {
	app, out, _, _ := newApp(t)
	rc := app.Run([]string{"install", "--help"})
	if rc != 0 {
		t.Fatalf("install --help rc = %d, want 0", rc)
	}
	if !strings.Contains(out.String(), "Zero-touch first-run setup") {
		t.Errorf("help should describe the verb; got: %q", out.String())
	}
}

// TestInstall_UnknownFlagUsageError: unknown flags must usage-error
// (rc 2) without firing any side effects.
func TestInstall_UnknownFlagUsageError(t *testing.T) {
	stubs := newInstallStubs()
	installStubsApply(t, stubs)

	app, _, errb, _ := newApp(t)
	rc := app.Run([]string{"install", "--frob"})
	if rc != 2 {
		t.Fatalf("rc = %d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "unknown flag") {
		t.Errorf("stderr should explain the bad flag; got: %q", errb.String())
	}
	if stubs.daemonStarted {
		t.Errorf("usage error must not start the daemon")
	}
}

// TestInstall_FirstRunAlias: the `clawtool first-run` alias routes
// to the same dispatcher as `clawtool install`.
func TestInstall_FirstRunAlias(t *testing.T) {
	stubs := newInstallStubs()
	stubs.daemonPort = 51500
	installStubsApply(t, stubs)

	app, out, _, _ := newApp(t)
	rc := app.Run([]string{"first-run", "--skip-init"})
	if rc != 0 {
		t.Fatalf("first-run rc = %d, want 0", rc)
	}
	if !strings.Contains(out.String(), "clawtool kuruldu") {
		t.Errorf("first-run alias must reach the same summary; got: %q", out.String())
	}
}

// TestInstall_GitRepoTriggersInitAll: when cwd is a git repo and
// --skip-init is NOT passed, step 8 fires the init --all hook.
func TestInstall_GitRepoTriggersInitAll(t *testing.T) {
	stubs := newInstallStubs()
	stubs.daemonPort = 52000
	stubs.initAllApplied = 5
	installStubsApply(t, stubs)

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	app, out, _, _ := newApp(t)
	rc := app.Run([]string{"install", "--workdir", repo})
	if rc != 0 {
		t.Fatalf("install rc = %d, want 0; stdout=%s", rc, out.String())
	}
	if !stubs.initAllCalled {
		t.Errorf("step 8 should have called installInitAll for a git repo")
	}
	if !strings.Contains(out.String(), "5 recipe(s) applied") {
		t.Errorf("summary should report 5 recipes; got: %q", out.String())
	}
}

// TestParseInstallArgs_Help: parser surfaces help with the
// errInstallHelp sentinel; runInstall converts that to a stdout
// usage print + rc 0.
func TestParseInstallArgs_Help(t *testing.T) {
	if _, err := parseInstallArgs([]string{"-h"}); !errors.Is(err, errInstallHelp) {
		t.Errorf("-h should return errInstallHelp; got %v", err)
	}
	if _, err := parseInstallArgs([]string{"--help"}); !errors.Is(err, errInstallHelp) {
		t.Errorf("--help should return errInstallHelp; got %v", err)
	}
}

// TestParseInstallArgs_Workdir: --workdir requires a value.
func TestParseInstallArgs_Workdir(t *testing.T) {
	args, err := parseInstallArgs([]string{"--workdir", "/tmp/x"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if args.workdir != "/tmp/x" {
		t.Errorf("workdir = %q, want /tmp/x", args.workdir)
	}
	if _, err := parseInstallArgs([]string{"--workdir"}); err == nil {
		t.Errorf("--workdir without value should error")
	}
}

// TestDetectInstallHosts_Stable: with claude+gemini fake-present,
// detectInstallHosts returns those two in canonical order.
func TestDetectInstallHosts_Stable(t *testing.T) {
	stubs := newInstallStubs()
	stubs.hostsPresent["claude"] = true
	stubs.hostsPresent["gemini"] = true
	installStubsApply(t, stubs)

	got := detectInstallHosts()
	want := []string{"claude", "gemini"}
	if len(got) != len(want) {
		t.Fatalf("detect = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("detect[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestInstall_AutoSpawnNoTmux: --auto-spawn with no tmux on PATH
// must log a warning + skip step 7.5 cleanly. Daemon still up,
// summary still printed, exit 0.
func TestInstall_AutoSpawnNoTmux(t *testing.T) {
	stubs := newInstallStubs()
	stubs.daemonPort = 47900
	stubs.hostsPresent["claude"] = true
	// tmux NOT on PATH — the install verb should detect that and
	// log a warning instead of dispatching tmux subprocess calls.
	installStubsApply(t, stubs)

	app, out, errb, _ := newApp(t)
	rc := app.Run([]string{"install", "--skip-init", "--auto-spawn"})
	if rc != 0 {
		t.Fatalf("install rc = %d, want 0; stderr=%s", rc, errb.String())
	}
	if len(stubs.tmuxCalls) != 0 {
		t.Errorf("tmux runner must not be called when tmux missing; got %d calls", len(stubs.tmuxCalls))
	}
	if !strings.Contains(errb.String(), "tmux not on PATH") {
		t.Errorf("stderr must explain tmux skip; got: %q", errb.String())
	}
	if !strings.Contains(out.String(), "clawtool kuruldu") {
		t.Errorf("summary must still print; got: %q", out.String())
	}
}

// TestInstall_AutoSpawnCreatesPanes: with tmux + N hosts present,
// step 7.5 must call tmux new-window once per detected host.
func TestInstall_AutoSpawnCreatesPanes(t *testing.T) {
	stubs := newInstallStubs()
	stubs.daemonPort = 48000
	stubs.hostsPresent["tmux"] = true
	stubs.hostsPresent["claude"] = true
	stubs.hostsPresent["codex"] = true
	stubs.hostsPresent["gemini"] = true
	installStubsApply(t, stubs)

	app, out, errb, _ := newApp(t)
	rc := app.Run([]string{"install", "--skip-init", "--auto-spawn"})
	if rc != 0 {
		t.Fatalf("install rc = %d, want 0; stderr=%s", rc, errb.String())
	}
	// Three detected hosts → three new-window calls.
	newWindows := 0
	for _, c := range stubs.tmuxCalls {
		if len(c) > 0 && c[0] == "new-window" {
			newWindows++
		}
	}
	if newWindows != 3 {
		t.Errorf("new-window count = %d, want 3 (one per host); calls=%v", newWindows, stubs.tmuxCalls)
	}
	// Default session name is "clawtool"; new-session must have
	// been called once (has-session returned non-zero).
	hasNewSession := false
	for _, c := range stubs.tmuxCalls {
		if len(c) >= 4 && c[0] == "new-session" && c[3] == "clawtool" {
			hasNewSession = true
			break
		}
	}
	if !hasNewSession {
		t.Errorf("new-session for 'clawtool' missing; calls=%v", stubs.tmuxCalls)
	}
	// Summary must reflect the 3 panes + session name.
	if !strings.Contains(out.String(), "tmux session 'clawtool'") {
		t.Errorf("summary must mention tmux session; got: %q", out.String())
	}
}

// TestInstall_AutoSpawnRegistersPaneIDs: every step-7.5 spawned
// agent's peer-register call must carry the captured pane id (the
// synthetic "%0", "%1", "%2"… ids the tmux stub dispenses).
func TestInstall_AutoSpawnRegistersPaneIDs(t *testing.T) {
	stubs := newInstallStubs()
	stubs.daemonPort = 48100
	stubs.hostsPresent["tmux"] = true
	stubs.hostsPresent["claude"] = true
	stubs.hostsPresent["codex"] = true
	installStubsApply(t, stubs)

	app, _, errb, _ := newApp(t)
	rc := app.Run([]string{"install", "--skip-init", "--auto-spawn", "--tmux-session-name", "alpha"})
	if rc != 0 {
		t.Fatalf("install rc = %d, want 0; stderr=%s", rc, errb.String())
	}
	// peerCalls has one shell-register (empty pane) + one per
	// detected host (non-empty pane). Filter to the non-empty ones.
	var paned []peerCall
	for _, p := range stubs.peerCalls {
		if p.tmuxPane != "" {
			paned = append(paned, p)
		}
	}
	if len(paned) != 2 {
		t.Fatalf("paned peer-register count = %d, want 2; all=%v", len(paned), stubs.peerCalls)
	}
	// Pane ids must be the synthetic %0, %1 in order.
	want := []string{"%0", "%1"}
	for i, p := range paned {
		if p.tmuxPane != want[i] {
			t.Errorf("paned[%d].tmuxPane = %q, want %q", i, p.tmuxPane, want[i])
		}
	}
	// Backends must be claude-code + codex (claimNameFor mapping).
	wantBackends := map[string]bool{"claude-code": false, "codex": false}
	for _, p := range paned {
		if _, ok := wantBackends[p.backend]; ok {
			wantBackends[p.backend] = true
		}
	}
	for b, ok := range wantBackends {
		if !ok {
			t.Errorf("paned peer-register missing backend %q; got %v", b, paned)
		}
	}
}

// TestInstall_AttachFlagInvokesTmuxAttach: --auto-spawn --attach
// must call installAttachExec exactly once with the session name,
// AFTER all dispatch + tmux subprocess calls. Verifies the
// "operator drops into the session" handoff is the last thing the
// install verb does.
func TestInstall_AttachFlagInvokesTmuxAttach(t *testing.T) {
	stubs := newInstallStubs()
	stubs.daemonPort = 48200
	stubs.hostsPresent["tmux"] = true
	stubs.hostsPresent["claude"] = true
	installStubsApply(t, stubs)

	app, _, errb, _ := newApp(t)
	rc := app.Run([]string{"install", "--skip-init", "--auto-spawn", "--attach", "--tmux-session-name", "ops"})
	if rc != 0 {
		t.Fatalf("install rc = %d, want 0; stderr=%s", rc, errb.String())
	}
	if len(stubs.attachCalls) != 1 {
		t.Fatalf("attachCalls = %d, want 1; got %v", len(stubs.attachCalls), stubs.attachCalls)
	}
	if stubs.attachCalls[0] != "ops" {
		t.Errorf("attach session = %q, want %q", stubs.attachCalls[0], "ops")
	}
	// The attach must be the LAST subprocess call — every other
	// dispatch / tmux call must come before it.
	if len(stubs.subprocCalls) == 0 {
		t.Fatalf("subprocCalls is empty — install ran no subprocs?")
	}
	last := stubs.subprocCalls[len(stubs.subprocCalls)-1]
	if last != "tmux-attach:ops" {
		t.Errorf("last subprocess call = %q, want tmux-attach:ops; full log=\n%s",
			last, strings.Join(stubs.subprocCalls, "\n"))
	}
}

// formatDispatchCalls renders the dispatcher's recorded argvs as a
// numbered list for failing test output. Compact and stable so a
// regression diff is readable.
func formatDispatchCalls(calls [][]string) string {
	var sb strings.Builder
	for i, c := range calls {
		sb.WriteString("  ")
		sb.WriteString(itoa(i))
		sb.WriteString(". ")
		sb.WriteString(strings.Join(c, " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// itoa avoids the fmt dependency for the tiny dispatch-call dump
// helper. Three-digit cap is enough for any realistic install run.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
