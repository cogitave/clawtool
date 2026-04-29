// Package server — `/v1/peers` REST surface (ADR-024 Phase 1).
//
// Four endpoints, all bearer-authed by the same authMiddleware
// every other /v1/* path uses:
//
//	GET    /v1/peers                       — list with status / backend / circle / path filters
//	POST   /v1/peers/register              — body: a2a.RegisterInput; returns the assigned Peer
//	POST   /v1/peers/{peer_id}/heartbeat   — refresh last_seen + status
//	DELETE /v1/peers/{peer_id}             — explicit deregister on session end
//
// Wire shape mirrors prassanna-ravishankar/repowire's
// /peers + /peers/by-pane endpoints so an existing repowire
// dashboard can be re-pointed at a clawtool daemon with a one-line
// URL change. Difference: clawtool's auth model is bearer-token
// (the daemon-wide token in ~/.config/clawtool/listener-token),
// not repowire's per-peer auth_token; we already have the
// daemon-shared token so a second layer is unnecessary at this
// phase.
//
// Registry lifecycle: the handlers fetch a2a.GetGlobal() on every
// request. buildMCPServer's Phase-1 boot installs a registry into
// the global slot (with persistence at ~/.config/clawtool/peers.json);
// daemon shutdown clears it. Handlers return 503 when the global
// is nil so a misconfigured boot doesn't 500 — operator gets a
// clear "registry not initialised" hint instead.
package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/a2a"
)

// handlePeers dispatches GET /v1/peers + POST /v1/peers/register
// + POST /v1/peers/{id}/heartbeat + DELETE /v1/peers/{id} based
// on method + path shape.
func handlePeers(w http.ResponseWriter, r *http.Request) {
	reg := a2a.GetGlobal()
	if reg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "peer registry not initialised — was clawtool daemon started with --listen?",
		})
		return
	}

	// Path-after-prefix: /v1/peers, /v1/peers/register, /v1/peers/<id>, /v1/peers/<id>/heartbeat
	tail := strings.TrimPrefix(r.URL.Path, "/v1/peers")
	tail = strings.TrimPrefix(tail, "/")

	switch {
	case tail == "" && r.Method == http.MethodGet:
		listPeers(w, r, reg)

	case tail == "register" && r.Method == http.MethodPost:
		registerPeer(w, r, reg)

	case tail == "broadcast" && r.Method == http.MethodPost:
		broadcastMessage(w, r, reg)

	case strings.HasSuffix(tail, "/heartbeat") && r.Method == http.MethodPost:
		peerID := strings.TrimSuffix(tail, "/heartbeat")
		heartbeatPeer(w, r, reg, peerID)

	case strings.HasSuffix(tail, "/messages") && r.Method == http.MethodPost:
		peerID := strings.TrimSuffix(tail, "/messages")
		sendMessage(w, r, reg, peerID)

	case strings.HasSuffix(tail, "/messages") && r.Method == http.MethodGet:
		peerID := strings.TrimSuffix(tail, "/messages")
		drainMessages(w, r, reg, peerID)

	case tail != "" && !strings.Contains(tail, "/") && r.Method == http.MethodDelete:
		deregisterPeer(w, r, reg, tail)

	case tail != "" && !strings.Contains(tail, "/") && r.Method == http.MethodGet:
		getPeer(w, r, reg, tail)

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": "unsupported method or path under /v1/peers",
			"endpoints": []string{
				"GET    /v1/peers",
				"GET    /v1/peers/{peer_id}",
				"POST   /v1/peers/register",
				"POST   /v1/peers/broadcast",
				"POST   /v1/peers/{peer_id}/heartbeat",
				"POST   /v1/peers/{peer_id}/messages",
				"GET    /v1/peers/{peer_id}/messages[?peek=1]",
				"DELETE /v1/peers/{peer_id}",
			},
		})
	}
}

// sendMessage enqueues a Message into peerID's inbox. Body is the
// a2a.Message shape with `text` + optional `from_peer` /
// `correlation_id` / `type`. peer_id / id / timestamp are
// server-assigned. Unknown peerID → 404.
func sendMessage(w http.ResponseWriter, r *http.Request, reg *a2a.Registry, peerID string) {
	if reg.Get(peerID) == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error":  "no peer with that id",
			"got_id": peerID,
		})
		return
	}
	var in a2a.Message
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body: " + err.Error()})
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "text is required"})
		return
	}
	if in.Type == "" {
		in.Type = a2a.MsgNotification
	}
	in.ToPeer = peerID
	saved := reg.SendTo(peerID, in)
	writeJSON(w, http.StatusOK, saved)
}

// drainMessages returns + clears peerID's inbox. ?peek=1 leaves
// messages in place — used by UserPromptSubmit hooks that want
// to surface unread messages without losing them on prompt
// cancellation. Unknown peerID is NOT 404 here: a peer may be
// polling its own inbox before any sender has hit it; an empty
// drain is a valid steady state.
func drainMessages(w http.ResponseWriter, r *http.Request, reg *a2a.Registry, peerID string) {
	peek := r.URL.Query().Get("peek") != ""
	msgs := reg.DrainInbox(peerID, peek)
	writeJSON(w, http.StatusOK, map[string]any{
		"peer_id":  peerID,
		"messages": msgs,
		"count":    len(msgs),
		"peek":     peek,
	})
}

// broadcastMessage fans `text` out to every registered peer except
// the sender. Body shape: { from_peer, text }. Peers' inboxes are
// updated in registry order.
func broadcastMessage(w http.ResponseWriter, r *http.Request, reg *a2a.Registry) {
	var in a2a.Message
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body: " + err.Error()})
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "text is required"})
		return
	}
	in.Type = a2a.MsgBroadcast
	count := reg.Broadcast(in)
	writeJSON(w, http.StatusOK, map[string]any{
		"delivered_to": count,
	})
}

func listPeers(w http.ResponseWriter, r *http.Request, reg *a2a.Registry) {
	q := r.URL.Query()
	filter := a2a.ListFilter{
		Status:  a2a.PeerStatus(q.Get("status")),
		Path:    q.Get("path"),
		Backend: q.Get("backend"),
		Circle:  q.Get("circle"),
	}
	peers := reg.List(filter)
	writeJSON(w, http.StatusOK, map[string]any{
		"peers": peers,
		"count": len(peers),
		"as_of": time.Now().UTC(),
	})
}

func registerPeer(w http.ResponseWriter, r *http.Request, reg *a2a.Registry) {
	var in a2a.RegisterInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid JSON body: " + err.Error(),
		})
		return
	}
	peer, err := reg.Register(in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	// Fire-and-forget save — best-effort persistence so a daemon
	// crash within seconds doesn't lose the row. List() also
	// flushes via markDirty so a stale-sweep persistence catches
	// up regardless.
	go func() { _ = reg.Save() }()
	writeJSON(w, http.StatusOK, peer)
}

func heartbeatPeer(w http.ResponseWriter, r *http.Request, reg *a2a.Registry, peerID string) {
	var in struct {
		Status a2a.PeerStatus `json:"status,omitempty"`
	}
	// Body is optional — empty body is "just bump last_seen".
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	peer, err := reg.Heartbeat(peerID, in.Status)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if peer == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error":  "no peer with that id — call POST /v1/peers/register first",
			"hint":   "peer_id changes when a session ends + re-registers; don't cache it across daemon restarts",
			"got_id": peerID,
		})
		return
	}
	go func() { _ = reg.Save() }()
	writeJSON(w, http.StatusOK, peer)
}

func deregisterPeer(w http.ResponseWriter, r *http.Request, reg *a2a.Registry, peerID string) {
	peer, err := reg.Deregister(peerID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if peer == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error":  "no peer with that id",
			"got_id": peerID,
		})
		return
	}
	go func() { _ = reg.Save() }()
	writeJSON(w, http.StatusOK, peer)
}

func getPeer(w http.ResponseWriter, r *http.Request, reg *a2a.Registry, peerID string) {
	peer := reg.Get(peerID)
	if peer == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error":  "no peer with that id",
			"got_id": peerID,
		})
		return
	}
	writeJSON(w, http.StatusOK, peer)
}
