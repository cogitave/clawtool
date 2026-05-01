// Package cli — `clawtool peer` subcommand. Phase 1 surface for
// ADR-024 peer discovery: the runtime-side primitive every hook
// (claude-code, codex, gemini, opencode) calls to register the
// running session into the daemon's peer registry.
//
// Three verbs:
//
//	clawtool peer register --backend X [--display-name Y] [--session ID]
//	clawtool peer heartbeat [--session ID] [--status busy|online]
//	clawtool peer deregister [--session ID]
//
// State: each register writes the assigned peer_id to a session-
// keyed file under ~/.config/clawtool/peers.d/<session>.id, so the
// downstream heartbeat / deregister calls find the right peer
// without the hook having to thread the id explicitly. Session IDs
// come from the runtime's hook payload (claude-code's transcript_path
// already has one); when --session is omitted, falls back to
// "default" — single-session-per-host hosts work out of the box.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cogitave/clawtool/internal/a2a"
	"github.com/cogitave/clawtool/internal/daemon"
)

const peerUsage = `Usage:
  clawtool peer register --backend <claude-code|codex|gemini|opencode|clawtool>
                         [--display-name <text>] [--session <id>]
                         [--circle <name>] [--path <abs-path>]
                         [--role agent|orchestrator] [--tmux-pane <id>]
                                           POST /v1/peers/register; persist the
                                           assigned peer_id under the session
                                           key for later heartbeat/deregister.
  clawtool peer heartbeat [--session <id>] [--status online|busy|offline]
                                           POST /v1/peers/{id}/heartbeat using
                                           the saved peer_id.
  clawtool peer deregister [--session <id>]
                                           DELETE /v1/peers/{id} and remove the
                                           session-keyed state file.
  clawtool peer send <peer_id|--name N|--broadcast> "<text>"
                                           POST /v1/peers/{id}/messages —
                                           enqueue a notification into the
                                           target peer's inbox. --name resolves
                                           via display_name; --broadcast
                                           fans out to every other peer.
  clawtool peer inbox [--session <id>] [--peek] [--format table|json|tsv]
                                           GET /v1/peers/{id}/messages — drain
                                           pending messages (or peek without
                                           consuming).
  clawtool peer drain [--session <id>] [--format text|json|context|hook-json]
                                           Like 'inbox' but always consumes.
                                           --format context emits each message
                                           as a system-prompt-shaped block
                                           ready to splice into the live
                                           agent's turn. --format hook-json
                                           emits a Claude Code UserPromptSubmit
                                           hookSpecificOutput envelope so the
                                           rendered messages get injected as
                                           additionalContext into the agent's
                                           next turn (empty inbox = ` + "`{}`" + `).
                                           Empty inbox in text/json/context
                                           = silent exit 0 (chainable from a
                                           session-tick hook).
  clawtool peer list [--circle <name>] [--backend <name>] [--status <s>]
                     [--format text|json|tsv]
                                           GET /v1/peers — operator-facing
                                           snapshot of every peer the daemon
                                           knows about. Sorted by last_seen
                                           desc. Filters narrow the set
                                           server-side via query params.

This is the runtime-side primitive — claude-code's bundled hooks fire it
automatically; for codex / gemini / opencode wire it from your runtime's
session hook (see ` + "`clawtool hooks install <runtime>`" + ` for the snippet).
`

// runPeer dispatches `clawtool peer ...`.
func (a *App) runPeer(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, peerUsage)
		return 2
	}
	switch argv[0] {
	case "register":
		return a.runPeerRegister(argv[1:])
	case "heartbeat":
		return a.runPeerHeartbeat(argv[1:])
	case "deregister":
		return a.runPeerDeregister(argv[1:])
	case "send":
		return a.runPeerSend(argv[1:])
	case "inbox":
		return a.runPeerInbox(argv[1:])
	case "drain":
		return a.runPeerDrain(argv[1:])
	case "list":
		return a.runPeerList(argv[1:])
	default:
		fmt.Fprintf(a.Stderr, "clawtool peer: unknown subcommand %q\n\n%s", argv[0], peerUsage)
		return 2
	}
}

func (a *App) runPeerSend(argv []string) int {
	fs := flag.NewFlagSet("peer send", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	name := fs.String("name", "", "Resolve target by display_name (instead of bare peer_id positional).")
	broadcast := fs.Bool("broadcast", false, "Fan out to every other peer (ignores positional peer_id).")
	fromSession := fs.String("from-session", defaultSessionKey(), "Sender session id (resolves to from_peer).")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	rest := fs.Args()
	if !*broadcast && *name == "" && len(rest) < 2 {
		fmt.Fprintln(a.Stderr, "usage: clawtool peer send <peer_id|--name N|--broadcast> \"<text>\"")
		return 2
	}
	var text, target string
	if *broadcast {
		if len(rest) < 1 {
			fmt.Fprintln(a.Stderr, "usage: clawtool peer send --broadcast \"<text>\"")
			return 2
		}
		text = strings.Join(rest, " ")
	} else if *name != "" {
		text = strings.Join(rest, " ")
		id, err := resolvePeerByName(*name)
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool peer send: %v\n", err)
			return 1
		}
		target = id
	} else {
		target = rest[0]
		text = strings.Join(rest[1:], " ")
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(a.Stderr, "clawtool peer send: text is required")
		return 2
	}

	// Best-effort: derive from_peer from the sender's saved session.
	from, _ := readPeerIDFile(*fromSession)
	msg := a2a.Message{Text: text, FromPeer: from}
	if *broadcast {
		body, _ := json.Marshal(msg)
		var out struct {
			DeliveredTo int `json:"delivered_to"`
		}
		if err := daemon.HTTPRequest(http.MethodPost, "/v1/peers/broadcast", bytes.NewReader(body), &out); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool peer send: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Stdout, "broadcast → %d peer(s)\n", out.DeliveredTo)
		return 0
	}
	body, _ := json.Marshal(msg)
	var saved a2a.Message
	if err := daemon.HTTPRequest(http.MethodPost, "/v1/peers/"+target+"/messages", bytes.NewReader(body), &saved); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool peer send: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.Stdout, saved.ID)
	return 0
}

func (a *App) runPeerInbox(argv []string) int {
	fs := flag.NewFlagSet("peer inbox", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	session := fs.String("session", defaultSessionKey(), "Session identifier (resolves to peer_id).")
	peek := fs.Bool("peek", false, "Don't consume — leave messages in the inbox.")
	format := fs.String("format", "table", "Output format: table | json | tsv.")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *session == "default" {
		if id := readSessionFromStdin(a.stdin()); id != "" {
			*session = id
		}
	}
	peerID, err := readPeerIDFile(*session)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool peer inbox: %v\n", err)
		return 1
	}
	url := "/v1/peers/" + peerID + "/messages"
	if *peek {
		url += "?peek=1"
	}
	var out struct {
		PeerID   string        `json:"peer_id"`
		Messages []a2a.Message `json:"messages"`
		Count    int           `json:"count"`
		Peek     bool          `json:"peek"`
	}
	if err := daemon.HTTPRequest(http.MethodGet, url, nil, &out); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool peer inbox: %v\n", err)
		return 1
	}
	switch *format {
	case "json":
		body, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(a.Stdout, string(body))
		return 0
	case "tsv":
		fmt.Fprintln(a.Stdout, "ID\tFROM\tTYPE\tWHEN\tTEXT")
		for _, m := range out.Messages {
			fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\t%s\n",
				m.ID, m.FromPeer, m.Type, m.Timestamp.Format(time.RFC3339), m.Text)
		}
		return 0
	}
	if out.Count == 0 {
		fmt.Fprintln(a.Stdout, "(inbox empty)")
		return 0
	}
	for _, m := range out.Messages {
		fmt.Fprintf(a.Stdout, "[%s] %s → %s\n  %s\n",
			m.Timestamp.Format(time.RFC3339), shortenPath(m.FromPeer, 12), m.Type, m.Text)
	}
	return 0
}

// runPeerDrain consumes the saved session's inbox and renders it
// for downstream consumers. Unlike `peer inbox` it has no --peek
// (consume is the whole point), and it adds --format context: a
// system-prompt-shaped block a session-tick hook can splice into
// the live agent's turn so peer messages reach the AGENT, not just
// the file.
//
// Empty inbox MUST exit 0 with NO output — the bundled Stop hook
// chains the verb's stdout into the agent's context, so any noise
// (a "(empty)" placeholder or banner) would pollute every turn.
//
// Atomicity: the daemon dequeues server-side on GET (peek=0). One
// successful HTTPRequest = the message left the queue; a daemon
// crash mid-flight loses at most the in-flight batch, never replays
// it. The CLI does no client-side rebuffering.
func (a *App) runPeerDrain(argv []string) int {
	fs := flag.NewFlagSet("peer drain", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	session := fs.String("session", defaultSessionKey(), "Session identifier (resolves to peer_id).")
	format := fs.String("format", "text", "Output format: text | json | context | hook-json.")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	switch *format {
	case "text", "json", "context", "hook-json":
	default:
		fmt.Fprintf(a.Stderr, "clawtool peer drain: unknown --format %q (want text|json|context|hook-json)\n", *format)
		return 2
	}
	if *session == "default" {
		if id := readSessionFromStdin(a.stdin()); id != "" {
			*session = id
		}
	}
	peerID, err := readPeerIDFile(*session)
	if err != nil {
		// No registered session = no inbox to drain. Hooks fire
		// before `peer register` lands on the very first session-
		// tick of a brand-new install; treat that as silently
		// empty so the chained `>>` redirection in hooks.json
		// doesn't surface a spurious error every turn.
		if errors.Is(err, os.ErrNotExist) {
			if *format == "hook-json" {
				// UserPromptSubmit hook contract: empty
				// object = "no additional context" — Claude
				// Code processes the prompt unchanged.
				fmt.Fprintln(a.Stdout, "{}")
			}
			return 0
		}
		fmt.Fprintf(a.Stderr, "clawtool peer drain: %v\n", err)
		return 1
	}
	var out struct {
		PeerID   string        `json:"peer_id"`
		Messages []a2a.Message `json:"messages"`
		Count    int           `json:"count"`
		Peek     bool          `json:"peek"`
	}
	if err := daemon.HTTPRequest(http.MethodGet, "/v1/peers/"+peerID+"/messages", nil, &out); err != nil {
		// hook-json: a daemon-down moment must not jam the
		// UserPromptSubmit pipeline — emit empty envelope and
		// route the diagnostic to stderr so the host's hook
		// log captures it without corrupting the agent's turn.
		if *format == "hook-json" {
			fmt.Fprintf(a.Stderr, "clawtool peer drain: %v\n", err)
			fmt.Fprintln(a.Stdout, "{}")
			return 0
		}
		fmt.Fprintf(a.Stderr, "clawtool peer drain: %v\n", err)
		return 1
	}
	// Empty inbox = silent exit 0 for the chainable formats; for
	// hook-json the contract is `{}` (so Claude Code's parser
	// always sees a valid envelope, never an empty stdout).
	if out.Count == 0 {
		if *format == "hook-json" {
			fmt.Fprintln(a.Stdout, "{}")
		}
		return 0
	}
	switch *format {
	case "json":
		body, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(a.Stdout, string(body))
		return 0
	case "context":
		// Resolve peer_id → display_name once so the context
		// block names the human, not the UUID. Best-effort:
		// when the lookup fails (daemon down between fetches,
		// peer deregistered between send and drain) we fall
		// back to the raw peer_id rather than dropping the
		// message.
		names := resolvePeerNames(out.Messages)
		for _, m := range out.Messages {
			label := names[m.FromPeer]
			if label == "" {
				label = m.FromPeer
				if label == "" {
					label = "unknown"
				}
			}
			fmt.Fprintf(a.Stdout, "\n[clawtool peer message from %s]: %s\n", label, m.Text)
		}
		return 0
	case "hook-json":
		// Claude Code UserPromptSubmit hook contract: stdout
		// is parsed as JSON; if hookSpecificOutput.hookEventName
		// == "UserPromptSubmit" and additionalContext is set,
		// that string is spliced into the prompt the agent
		// sees on its NEXT turn. This is how peer messages
		// reach the live agent automatically — no manual
		// `peer inbox --peek` required.
		//
		// Render: each message wrapped as a labeled system-
		// prompt block, joined with `\n\n---\n\n` so the agent
		// sees a clean sequence even when N peers broadcast at
		// once.
		names := resolvePeerNames(out.Messages)
		blocks := make([]string, 0, len(out.Messages))
		for _, m := range out.Messages {
			label := names[m.FromPeer]
			if label == "" {
				label = m.FromPeer
				if label == "" {
					label = "unknown"
				}
			}
			ts := m.Timestamp.Format(time.RFC3339)
			blocks = append(blocks, fmt.Sprintf("[clawtool peer message — from %s, %s]\n%s", label, ts, m.Text))
		}
		envelope := map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     "UserPromptSubmit",
				"additionalContext": strings.Join(blocks, "\n\n---\n\n"),
			},
		}
		body, _ := json.Marshal(envelope)
		fmt.Fprintln(a.Stdout, string(body))
		return 0
	}
	// text — same shape as `peer inbox` table mode minus the
	// "(inbox empty)" branch (handled above).
	for _, m := range out.Messages {
		fmt.Fprintf(a.Stdout, "[%s] %s → %s\n  %s\n",
			m.Timestamp.Format(time.RFC3339), shortenPath(m.FromPeer, 12), m.Type, m.Text)
	}
	return 0
}

// resolvePeerNames batch-fetches /v1/peers and builds a peer_id
// → display_name map for every distinct sender in `msgs`. Returns
// an empty map on any error so the caller transparently falls
// back to bare peer_ids; the daemon being unreachable here is not
// a reason to lose a delivered message.
func resolvePeerNames(msgs []a2a.Message) map[string]string {
	want := make(map[string]struct{}, len(msgs))
	for _, m := range msgs {
		if m.FromPeer != "" {
			want[m.FromPeer] = struct{}{}
		}
	}
	if len(want) == 0 {
		return map[string]string{}
	}
	var out struct {
		Peers []a2a.Peer `json:"peers"`
	}
	if err := daemon.HTTPRequest(http.MethodGet, "/v1/peers", nil, &out); err != nil {
		return map[string]string{}
	}
	names := make(map[string]string, len(want))
	for _, p := range out.Peers {
		if _, ok := want[p.PeerID]; ok && p.DisplayName != "" {
			names[p.PeerID] = p.DisplayName
		}
	}
	return names
}

// resolvePeerByName looks up the daemon's peer list and returns
// the peer_id whose display_name matches `name`. Errors when zero
// or two-or-more peers match — the caller passed an ambiguous
// label, force them to use the bare peer_id instead.
func resolvePeerByName(name string) (string, error) {
	var out struct {
		Peers []a2a.Peer `json:"peers"`
	}
	if err := daemon.HTTPRequest(http.MethodGet, "/v1/peers", nil, &out); err != nil {
		return "", err
	}
	var matches []a2a.Peer
	for _, p := range out.Peers {
		if p.DisplayName == name {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no peer named %q", name)
	case 1:
		return matches[0].PeerID, nil
	default:
		return "", fmt.Errorf("ambiguous: %d peers named %q — pass the bare peer_id instead", len(matches), name)
	}
}

func (a *App) runPeerRegister(argv []string) int {
	fs := flag.NewFlagSet("peer register", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	backend := fs.String("backend", "", "Runtime family (claude-code|codex|gemini|opencode|clawtool). Required.")
	displayName := fs.String("display-name", "", "Human-friendly label (defaults to user@host).")
	session := fs.String("session", defaultSessionKey(), "Session identifier — keys the saved peer_id.")
	circle := fs.String("circle", "", "Group name (defaults to tmux session or 'default').")
	path := fs.String("path", "", "Project root path (defaults to cwd).")
	role := fs.String("role", "", "agent | orchestrator (default agent).")
	pane := fs.String("tmux-pane", os.Getenv("TMUX_PANE"), "tmux pane id (auto-detected from $TMUX_PANE).")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *backend == "" {
		fmt.Fprintln(a.Stderr, "clawtool peer register: --backend is required")
		return 2
	}
	// Fallback: pull session id from the runtime's hook event JSON
	// when neither --session nor the env var was supplied. Claude
	// Code, for instance, ships {"session_id": "..."} on stdin for
	// every hook fire — so a one-line shell hook (`clawtool peer
	// register --backend claude-code`) gets correct keying for free.
	if *session == "default" {
		if id := readSessionFromStdin(a.stdin()); id != "" {
			*session = id
		}
	}
	if *displayName == "" {
		*displayName = defaultDisplayName(*backend)
	}
	if *path == "" {
		if cwd, err := os.Getwd(); err == nil {
			*path = cwd
		}
	}

	in := a2a.RegisterInput{
		DisplayName: *displayName,
		Path:        *path,
		Backend:     *backend,
		Circle:      *circle,
		SessionID:   *session,
		TmuxPane:    *pane,
		PID:         os.Getpid(),
	}
	if *role != "" {
		in.Role = a2a.PeerRole(*role)
	}
	body, _ := json.Marshal(in)

	var peer a2a.Peer
	if err := daemon.HTTPRequest(http.MethodPost, "/v1/peers/register", bytes.NewReader(body), &peer); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool peer register: %v\n", err)
		return 1
	}
	if err := writePeerIDFile(*session, peer.PeerID); err != nil {
		// Non-fatal: the peer registered, we just couldn't persist
		// the id locally. Surface the warning so the operator can
		// fix permissions but don't fail the hook.
		fmt.Fprintf(a.Stderr, "clawtool peer register: warning: persist peer_id: %v\n", err)
	}
	fmt.Fprintln(a.Stdout, peer.PeerID)
	return 0
}

func (a *App) runPeerHeartbeat(argv []string) int {
	fs := flag.NewFlagSet("peer heartbeat", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	session := fs.String("session", defaultSessionKey(), "Session identifier (matches the register call).")
	status := fs.String("status", "", "Optional: online | busy | offline.")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *session == "default" {
		if id := readSessionFromStdin(a.stdin()); id != "" {
			*session = id
		}
	}
	peerID, err := readPeerIDFile(*session)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool peer heartbeat: %v\n", err)
		return 1
	}
	body, _ := json.Marshal(map[string]string{"status": *status})
	var got a2a.Peer
	if err := daemon.HTTPRequest(http.MethodPost, "/v1/peers/"+peerID+"/heartbeat", bytes.NewReader(body), &got); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool peer heartbeat: %v\n", err)
		return 1
	}
	return 0
}

func (a *App) runPeerDeregister(argv []string) int {
	fs := flag.NewFlagSet("peer deregister", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	session := fs.String("session", defaultSessionKey(), "Session identifier (matches the register call).")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *session == "default" {
		if id := readSessionFromStdin(a.stdin()); id != "" {
			*session = id
		}
	}
	peerID, err := readPeerIDFile(*session)
	if err != nil {
		// Already deregistered or never registered — silent success
		// so SessionEnd hooks don't surface noise on idempotent runs.
		if errors.Is(err, os.ErrNotExist) {
			return 0
		}
		fmt.Fprintf(a.Stderr, "clawtool peer deregister: %v\n", err)
		return 1
	}
	var got a2a.Peer
	if err := daemon.HTTPRequest(http.MethodDelete, "/v1/peers/"+peerID, nil, &got); err != nil {
		// Best-effort: still try to remove the local state file
		// so the next session doesn't inherit a stale id.
		_ = removePeerIDFile(*session)
		fmt.Fprintf(a.Stderr, "clawtool peer deregister: %v\n", err)
		return 1
	}
	_ = removePeerIDFile(*session)
	return 0
}

// runPeerList renders an operator-facing snapshot of every peer
// the daemon knows about. The daemon's GET /v1/peers is the only
// authoritative source — peers.json on disk is a debounced
// persistence side-effect, not a query surface — so this verb
// dials the listener using the same auth/timeout path peer send
// uses. Filters (--circle / --backend / --status) are forwarded as
// query params; the daemon does the matching in a.Registry.List
// so the operator never sees stale rows.
//
// Default sort is last_seen desc (most-recently-active first); the
// table truncates display_name + circle so a 24-peer registry on a
// dev laptop renders without wrapping in a tmux split.
func (a *App) runPeerList(argv []string) int {
	fs := flag.NewFlagSet("peer list", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	circle := fs.String("circle", "", "Filter to peers in this circle.")
	backend := fs.String("backend", "", "Filter to peers with this backend (claude-code|codex|gemini|opencode|clawtool).")
	status := fs.String("status", "", "Filter to peers with this status (online|busy|offline).")
	format := fs.String("format", "text", "Output format: text | json | tsv.")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(a.Stderr, "clawtool peer list: unexpected positional arg %q\n", fs.Arg(0))
		return 2
	}

	q := url.Values{}
	if *circle != "" {
		q.Set("circle", *circle)
	}
	if *backend != "" {
		q.Set("backend", *backend)
	}
	if *status != "" {
		q.Set("status", *status)
	}
	path := "/v1/peers"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}

	var out struct {
		Peers []a2a.Peer `json:"peers"`
		Count int        `json:"count"`
		AsOf  time.Time  `json:"as_of"`
	}
	if err := daemon.HTTPRequest(http.MethodGet, path, nil, &out); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool peer list: %v\n", err)
		return 1
	}
	// Sort last_seen desc — most recent first. Tie-break on
	// peer_id so the order is deterministic in tests.
	sort.SliceStable(out.Peers, func(i, j int) bool {
		if out.Peers[i].LastSeen.Equal(out.Peers[j].LastSeen) {
			return out.Peers[i].PeerID < out.Peers[j].PeerID
		}
		return out.Peers[i].LastSeen.After(out.Peers[j].LastSeen)
	})

	switch *format {
	case "json":
		body, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(a.Stdout, string(body))
		return 0
	case "tsv":
		fmt.Fprintln(a.Stdout, "PEER_ID\tBACKEND\tDISPLAY_NAME\tSTATUS\tLAST_SEEN\tROLE\tCIRCLE")
		for _, p := range out.Peers {
			fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				p.PeerID, p.Backend, p.DisplayName, p.Status,
				p.LastSeen.UTC().Format(time.RFC3339), p.Role, p.Circle)
		}
		return 0
	case "text", "":
		// fall through
	default:
		fmt.Fprintf(a.Stderr, "clawtool peer list: unknown --format %q (want text|json|tsv)\n", *format)
		return 2
	}

	if out.Count == 0 {
		fmt.Fprintln(a.Stdout, "(no peers registered)")
		return 0
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PEER_ID\tBACKEND\tDISPLAY_NAME\tSTATUS\tLAST_SEEN\tROLE\tCIRCLE")
	now := time.Now()
	for _, p := range out.Peers {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			shortenPath(p.PeerID, 16),
			p.Backend,
			shortenPath(p.DisplayName, 28),
			p.Status,
			humanAge(now, p.LastSeen),
			p.Role,
			shortenPath(p.Circle, 16),
		)
	}
	_ = tw.Flush()
	return 0
}

// humanAge renders a coarse "Nm ago" / "Ns ago" string for the
// list view. RFC3339 is right for tsv/json (machine-readable),
// but a 14-line table is unreadable when each row is a 25-char
// timestamp — so the text path collapses it into a relative age.
func humanAge(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// peerIDFile resolves the on-disk pointer for a session's saved
// peer_id. Lives under a2a.PeersStateDir() so daemon's inbox files
// and the CLI's session pointers share one directory.
func peerIDFile(session string) string {
	if session == "" {
		session = "default"
	}
	return filepath.Join(a2a.PeersStateDir(), sanitizeSession(session)+".id")
}

func writePeerIDFile(session, peerID string) error {
	if err := os.MkdirAll(a2a.PeersStateDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(peerIDFile(session), []byte(peerID+"\n"), 0o600)
}

func readPeerIDFile(session string) (string, error) {
	b, err := os.ReadFile(peerIDFile(session))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func removePeerIDFile(session string) error {
	if err := os.Remove(peerIDFile(session)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// sanitizeSession strips path separators / weird chars from the
// session key so a malicious or malformed value can't escape
// peers.d. Whitelist [A-Za-z0-9._-]; everything else collapses
// to '-'.
func sanitizeSession(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

// defaultSessionKey resolves a key from the env (CLAWTOOL_PEER_SESSION
// preferred, then CLAUDE_SESSION_ID for claude-code parity), falling
// back to "default" for single-session hosts.
func defaultSessionKey() string {
	for _, k := range []string{"CLAWTOOL_PEER_SESSION", "CLAUDE_SESSION_ID"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "default"
}

func defaultDisplayName(backend string) string {
	user := firstNonEmpty(os.Getenv("USER"), os.Getenv("USERNAME"), "user")
	host, _ := os.Hostname()
	if host == "" {
		host = "host"
	}
	return fmt.Sprintf("%s@%s/%s", user, host, backend)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// readSessionFromStdin best-effort decodes a single Claude-Code-
// style hook event from stdin and returns its session_id. Empty
// string when stdin is empty / not JSON / has no session_id —
// callers fall back to "default" in that case.
//
// Capped at 64 KiB so a runaway producer can't OOM the hook.
func readSessionFromStdin(r io.Reader) string {
	limited := io.LimitReader(r, 64*1024)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) == 0 {
		return ""
	}
	var ev struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return ""
	}
	return strings.TrimSpace(ev.SessionID)
}
