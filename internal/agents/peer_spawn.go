// Package agents — production PeerSpawner adapter.
//
// Bridges the auto-spawn seam in peer_route.go to the in-process
// a2a.Registry + a tmux `new-window` invocation. When SendMessage
// finds no live peer for `family`, the supervisor calls
// EnsurePeer; this adapter (a) opens a fresh tmux pane running
// the family's CLI in elevated mode and (b) registers the new
// agent in the BIAM peer registry so the next FindOnlinePeer
// call hits.
//
// Why not call internal/cli/spawn.go: that path is the user-facing
// `clawtool spawn` verb and dials the daemon over HTTP — fine for
// a one-shot CLI, wrong shape for an in-process auto-spawn from
// inside Supervisor.Send. We bypass the HTTP loopback and write
// directly to the registry the daemon already initialised.
//
// Test seam: tmuxBin + tmuxNewWindow are package-level vars tests
// rebind to deterministic stubs so the suite never spawns a real
// pane. Production wires both to real `tmux new-window` calls.

package agents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/cogitave/clawtool/internal/a2a"
)

// a2aPeerSpawner is the production PeerSpawner. Wraps an
// *a2a.Registry so EnsurePeer can persist the freshly-spawned
// agent without going through HTTP.
type a2aPeerSpawner struct {
	reg *a2a.Registry
}

// NewA2APeerSpawner returns a PeerSpawner backed by the given
// registry. Returns nil when reg is nil — the supervisor
// gracefully falls through (no auto-spawn) in that case.
func NewA2APeerSpawner(reg *a2a.Registry) PeerSpawner {
	if reg == nil {
		return nil
	}
	return &a2aPeerSpawner{reg: reg}
}

// tmuxBin is the path to the tmux binary. Tests rebind to a noop
// or a path that doesn't exist to simulate "no tmux on PATH".
var tmuxBin = func() string {
	p, err := exec.LookPath("tmux")
	if err != nil {
		return ""
	}
	return p
}

// tmuxNewWindow runs `tmux new-window` for the given command line
// in the given cwd. Test seam — production wires it to the real
// exec call; tests substitute a recorder.
var tmuxNewWindow = func(ctx context.Context, cwd, cmdline string) error {
	args := []string{"new-window"}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, cmdline)
	c := exec.CommandContext(ctx, "tmux", args...)
	return c.Start()
}

// TmuxAvailable reports whether $TMUX is set AND a `tmux` binary
// is on PATH. Both signals are required: $TMUX without the binary
// is a stale env (we couldn't `tmux send-keys` even if we wanted),
// the binary without $TMUX means we'd have to start a server +
// session ourselves, which is intrusive for the operator.
func (a *a2aPeerSpawner) TmuxAvailable() bool {
	if a == nil {
		return false
	}
	if strings.TrimSpace(os.Getenv("TMUX")) == "" {
		return false
	}
	if tmuxBin() == "" {
		return false
	}
	return true
}

// EnsurePeer opens a fresh tmux pane running the family's elevated
// CLI and registers the new agent in the BIAM peer registry. The
// agent's own startup hook may also re-register; the registry's
// identity tuple collapses dupes.
//
// Cooldown is enforced one layer up by shouldAutoSpawn — this
// method is unconditional from its own POV. fromPeerID is wired
// into the Metadata map for downstream attribution / audit.
func (a *a2aPeerSpawner) EnsurePeer(family, fromPeerID string) (string, string, bool, error) {
	if a == nil || a.reg == nil {
		return "", "", false, fmt.Errorf("auto-spawn: registry not initialised")
	}
	bin, argv := autoSpawnArgvForFamily(family)
	if bin == "" {
		return "", "", false, fmt.Errorf("auto-spawn: no argv mapping for family %q", family)
	}
	cwd, _ := os.Getwd()
	cmdline := strings.Join(append([]string{bin}, argv...), " ")
	ctx, cancel := contextWithDefaultDeadline()
	defer cancel()
	if err := tmuxNewWindow(ctx, cwd, cmdline); err != nil {
		return "", "", false, fmt.Errorf("auto-spawn: tmux new-window: %w", err)
	}
	displayName := fmt.Sprintf("%s:auto-spawn", family)
	peer, err := a.reg.Register(a2a.RegisterInput{
		DisplayName: displayName,
		Path:        cwd,
		Backend:     familyToBackend(family),
		Role:        a2a.RoleAgent,
		Metadata: map[string]string{
			"spawned_by":      "clawtool",
			"spawn_terminal":  "tmux",
			"spawn_bin":       bin,
			"spawn_initiator": fromPeerID,
			"spawn_trigger":   "sendmessage-auto",
		},
	})
	if err != nil {
		return "", "", false, fmt.Errorf("auto-spawn: register peer: %w", err)
	}
	return peer.PeerID, peer.DisplayName, true, nil
}

// autoSpawnArgvForFamily returns the binary + argv for spawning
// `family` in elevated headless mode. Mirrors setuptools'
// spawnArgvForFamily; duplicated here to avoid pulling tools/setup
// into the agents package's import graph.
func autoSpawnArgvForFamily(family string) (string, []string) {
	switch family {
	case "claude":
		return "claude", []string{"--dangerously-skip-permissions"}
	case "codex":
		return "codex", []string{"--dangerously-bypass-approvals-and-sandbox"}
	case "gemini":
		return "gemini", []string{"--yolo"}
	case "opencode":
		return "opencode", []string{"--yolo"}
	}
	return "", nil
}

// contextWithDefaultDeadline returns a context with a short
// deadline so a stuck `tmux new-window` (rare but possible when
// the server is wedged) doesn't hang the SendMessage call. 5s is
// generous for a local pane spawn and tight enough that the
// caller sees a typed error instead of a hung handler.
func contextWithDefaultDeadline() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), autoSpawnDeadline)
}
