// Package a2a — peer inbox. The discovery half (registry.go)
// surfaces *who* is on the host; the inbox half (this file) is
// *how they talk*. Each peer has an in-memory mailbox; senders
// enqueue via POST /v1/peers/{id}/messages, recipients drain via
// GET /v1/peers/{id}/messages or `clawtool peer inbox`.
//
// Wire shape mirrors repowire/repowire/protocol/messages.py
// (Query / Response / Notification / Broadcast) so a runtime
// hook polling once per UserPromptSubmit can surface pending
// messages as additionalContext without inventing its own
// format.
//
// Persistence: each peer's inbox is mirrored to
// ~/.config/clawtool/peers.d/<peer_id>.inbox.json on every
// mutation. A daemon crash mid-flight loses at most the last
// in-flight message; the rest survive a restart. Soft cap at
// 256 messages per peer — overflow drops the OLDEST so a
// chatty sender can't OOM the daemon. New peers start empty.
package a2a

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MessageType matches repowire's protocol/messages.py taxonomy.
// Locked at v0.22; new types are additive.
type MessageType string

const (
	MsgQuery        MessageType = "query"        // expects a response
	MsgResponse     MessageType = "response"     // reply to a query (correlation_id required)
	MsgNotification MessageType = "notification" // fire-and-forget
	MsgBroadcast    MessageType = "broadcast"    // to all peers (to_peer ignored)
)

// Message is one envelope in the peer mesh.
type Message struct {
	ID            string      `json:"id"`
	Type          MessageType `json:"type"`
	FromPeer      string      `json:"from_peer"`
	ToPeer        string      `json:"to_peer,omitempty"` // omitted for broadcast
	Text          string      `json:"text"`
	CorrelationID string      `json:"correlation_id,omitempty"` // matches a prior query's ID
	Timestamp     time.Time   `json:"timestamp"`
}

// inboxCap is the soft per-peer limit. Overflow drops the
// oldest message so sustained traffic from one peer can't
// pin daemon memory.
const inboxCap = 256

// Inbox is the per-peer message queue. One Inbox per registered
// peer; created lazily on first send. Methods are safe for
// concurrent calls — mu guards both the queue and the on-disk
// snapshot.
type Inbox struct {
	mu       sync.Mutex
	peerID   string
	queue    []Message
	statePath string
}

// PeersStateDir returns the canonical ~/.config/clawtool/peers.d
// directory used by both the daemon (per-peer inbox files written
// by this package) and the CLI's `clawtool peer` verb (per-session
// id pointer files). One layout, one helper — exported so callers
// outside this package don't reinvent the path-resolution dance.
//
// On-disk layout:
//
//	peers.d/<session>.id            — CLI's session→peer_id pointer
//	peers.d/<peer_uuid>.inbox.json  — daemon's per-peer mailbox
func PeersStateDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "clawtool", "peers.d")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "clawtool", "peers.d")
	}
	return "peers.d"
}

func inboxPath(peerID string) string {
	return filepath.Join(PeersStateDir(), peerID+".inbox.json")
}

// Enqueue appends `msg` to this inbox, capping to inboxCap and
// dropping the oldest if needed. Returns the persisted message
// (with assigned ID + timestamp when the caller didn't supply
// them). Idempotent on (FromPeer, Timestamp, Text) is NOT
// attempted — duplicate sends mean the sender retried; the
// recipient sees both.
func (i *Inbox) Enqueue(msg Message) Message {
	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}
	i.mu.Lock()
	i.queue = append(i.queue, msg)
	if over := len(i.queue) - inboxCap; over > 0 {
		i.queue = i.queue[over:]
	}
	saved := append([]Message(nil), i.queue...)
	i.mu.Unlock()
	_ = persistInbox(i.statePath, saved)
	return msg
}

// Drain returns every queued message and empties the inbox.
// Pass peek=true to read without consuming — the runtime's
// UserPromptSubmit hook uses peek to avoid losing messages if
// the recipient cancels the prompt.
func (i *Inbox) Drain(peek bool) []Message {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]Message, len(i.queue))
	copy(out, i.queue)
	if !peek {
		i.queue = i.queue[:0]
		_ = persistInbox(i.statePath, nil)
	}
	return out
}

// persistInbox writes `queue` to path atomically. nil → delete.
// Best-effort; mailbox stays in-memory authoritative if write
// fails (process crash before the next persistence loses at
// most the last message).
func persistInbox(path string, queue []Message) error {
	if path == "" {
		return nil
	}
	if len(queue) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(queue, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadInbox reads a persisted queue or returns empty when the
// file is missing / corrupt. Corruption is non-fatal — we'd
// rather lose the disk copy than refuse to boot.
func loadInbox(path string) []Message {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var queue []Message
	if err := json.Unmarshal(b, &queue); err != nil {
		return nil
	}
	return queue
}

// inboxes is the daemon-wide map of peer_id → Inbox. The Registry
// owns one and exposes Enqueue / Drain on it. Nil-safe.
type inboxes struct {
	mu  sync.Mutex
	all map[string]*Inbox
}

func newInboxes() *inboxes {
	return &inboxes{all: map[string]*Inbox{}}
}

// for retrieves (or creates) the inbox for peerID.
func (im *inboxes) for_(peerID string) *Inbox {
	im.mu.Lock()
	defer im.mu.Unlock()
	if box, ok := im.all[peerID]; ok {
		return box
	}
	statePath := inboxPath(peerID)
	box := &Inbox{
		peerID:    peerID,
		statePath: statePath,
		queue:     loadInbox(statePath),
	}
	im.all[peerID] = box
	return box
}

// remove drops the inbox for peerID — invoked on explicit
// Deregister so an offline peer doesn't accumulate stale state.
func (im *inboxes) remove(peerID string) {
	im.mu.Lock()
	defer im.mu.Unlock()
	if box, ok := im.all[peerID]; ok {
		_ = os.Remove(box.statePath)
		delete(im.all, peerID)
	}
}

// SendTo enqueues `msg` into peerID's inbox. Returns the assigned
// message (with ID + timestamp). Caller must have validated peerID
// exists in the registry — the inbox creates lazily, so this would
// happily accept messages for a non-existent peer otherwise.
func (r *Registry) SendTo(peerID string, msg Message) Message {
	r.boxMu.Lock()
	if r.inboxes == nil {
		r.inboxes = newInboxes()
	}
	box := r.inboxes.for_(peerID)
	r.boxMu.Unlock()
	return box.Enqueue(msg)
}

// Broadcast enqueues `msg` into every currently-known peer's inbox
// (except the sender's own, identified by msg.FromPeer). Returns
// the count of recipients reached. Used by MsgBroadcast — one HTTP
// hit fans out to all live sessions.
func (r *Registry) Broadcast(msg Message) int {
	r.mu.RLock()
	peerIDs := make([]string, 0, len(r.peers))
	for id := range r.peers {
		if id == msg.FromPeer {
			continue
		}
		peerIDs = append(peerIDs, id)
	}
	r.mu.RUnlock()
	sort.Strings(peerIDs)

	for _, id := range peerIDs {
		copyMsg := msg
		copyMsg.ToPeer = id
		copyMsg.ID = uuid.NewString()
		copyMsg.Timestamp = time.Now().UTC()
		r.SendTo(id, copyMsg)
	}
	return len(peerIDs)
}

// DrainInbox returns the pending messages for peerID and clears
// them (or peeks, leaving them queued). Non-existent peers return
// an empty slice — the inbox is created lazily and an empty drain
// stays empty.
func (r *Registry) DrainInbox(peerID string, peek bool) []Message {
	r.boxMu.Lock()
	if r.inboxes == nil {
		r.inboxes = newInboxes()
	}
	box := r.inboxes.for_(peerID)
	r.boxMu.Unlock()
	return box.Drain(peek)
}

// dropInbox is invoked by Deregister so deregistered peers don't
// keep persisted state forever. Non-existent inbox is a no-op.
func (r *Registry) dropInbox(peerID string) {
	r.boxMu.Lock()
	if r.inboxes != nil {
		r.inboxes.remove(peerID)
	}
	r.boxMu.Unlock()
}
