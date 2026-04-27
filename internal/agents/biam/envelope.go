package biam

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Address points at one peer instance. Format: `host_id/instance_id`.
type Address struct {
	HostID     string `json:"host_id"`
	InstanceID string `json:"instance_id"`
}

func (a Address) String() string { return a.HostID + "/" + a.InstanceID }

// EnvelopeKind enumerates what a message represents in a BIAM thread.
type EnvelopeKind string

const (
	KindPrompt        EnvelopeKind = "prompt"
	KindReply         EnvelopeKind = "reply"
	KindClarification EnvelopeKind = "clarification"
	KindResult        EnvelopeKind = "result"
	KindError         EnvelopeKind = "error"
	KindCancel        EnvelopeKind = "cancel"
)

// Body is the per-message payload. `Text` is the agent-readable
// content; `Extras` carries opt-in structured data without forcing a
// schema bump.
type Body struct {
	Text   string         `json:"text,omitempty"`
	Extras map[string]any `json:"extras,omitempty"`
}

// Envelope is the wire shape every BIAM message takes. Locked at
// `v: biam-v1` per ADR-015. Field rules in the ADR's "Wire envelope"
// section.
type Envelope struct {
	Version        string       `json:"v"`
	TaskID         string       `json:"task_id"`
	MessageID      string       `json:"message_id"`
	ParentID       string       `json:"parent_id,omitempty"`
	CorrelationID  string       `json:"correlation_id,omitempty"`
	From           Address      `json:"from"`
	To             Address      `json:"to"`
	ReplyTo        Address      `json:"reply_to"`
	Kind           EnvelopeKind `json:"kind"`
	Body           Body         `json:"body"`
	HopCount       int          `json:"hop_count"`
	MaxHops        int          `json:"max_hops"`
	Trace          []string     `json:"trace"`
	CreatedAt      time.Time    `json:"created_at"`
	TTLSeconds     int64        `json:"ttl_seconds"`
	IdempotencyKey string       `json:"idempotency_key"`
	Signature      string       `json:"signature,omitempty"`
}

// NewEnvelope stamps the routine fields a fresh envelope needs and
// leaves the caller to set Body / ParentID / Kind. Trace seeds with
// the sender's address so cycle detection works on hop 1.
func NewEnvelope(from, to Address, taskID string, kind EnvelopeKind, body Body) *Envelope {
	if taskID == "" {
		taskID = uuid.NewString()
	}
	return &Envelope{
		Version:        "biam-v1",
		TaskID:         taskID,
		MessageID:      uuid.NewString(),
		From:           from,
		To:             to,
		ReplyTo:        from,
		Kind:           kind,
		Body:           body,
		HopCount:       0,
		MaxHops:        10,
		Trace:          []string{from.String()},
		CreatedAt:      time.Now().UTC(),
		TTLSeconds:     86400,
		IdempotencyKey: uuid.NewString(),
	}
}

// Sign computes the Ed25519 signature over the canonical JSON form
// (every field except Signature itself) and stores it on the envelope.
func (e *Envelope) Sign(id *Identity) error {
	if id == nil {
		return errors.New("biam: identity is nil")
	}
	canonical, err := e.canonical()
	if err != nil {
		return err
	}
	sig := id.Sign(canonical)
	e.Signature = "ed25519:" + hexEncode(sig)
	return nil
}

// Verify decodes the envelope's signature and checks it against the
// sender's known public key. Receivers must call this before trusting
// any field on the envelope.
func (e *Envelope) Verify(pub ed25519.PublicKey) error {
	if e.Signature == "" {
		return errors.New("biam: envelope unsigned")
	}
	const prefix = "ed25519:"
	if !strings.HasPrefix(e.Signature, prefix) {
		return fmt.Errorf("biam: signature missing %q prefix", prefix)
	}
	sig, err := hexDecode(e.Signature[len(prefix):])
	if err != nil {
		return fmt.Errorf("biam: decode signature: %w", err)
	}
	canonical, err := e.canonical()
	if err != nil {
		return err
	}
	if !Verify(pub, canonical, sig) {
		return errors.New("biam: signature mismatch")
	}
	return nil
}

// canonical returns the JSON form used for signing/verifying. Strips
// the Signature field so signing is reversible.
func (e *Envelope) canonical() ([]byte, error) {
	clone := *e
	clone.Signature = ""
	return json.Marshal(&clone)
}

// HasCycle reports whether `peer` already appears in the envelope's
// trace — a clean way to detect "this came back to me, drop it."
func (e *Envelope) HasCycle(peer Address) bool {
	target := peer.String()
	for _, t := range e.Trace {
		if t == target {
			return true
		}
	}
	return false
}

// Hop bumps the hop count + appends `me` to the trace. Returns the
// fresh max-hops error when the cap is exceeded.
func (e *Envelope) Hop(me Address) error {
	if e.HopCount+1 > e.MaxHops {
		return fmt.Errorf("biam: hop_count exceeded max %d", e.MaxHops)
	}
	e.HopCount++
	e.Trace = append(e.Trace, me.String())
	return nil
}

// hexEncode/hexDecode are inlined to avoid pulling encoding/hex into
// every consumer; the cost is negligible.
func hexEncode(b []byte) string {
	const hexchars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexchars[v>>4]
		out[i*2+1] = hexchars[v&0x0f]
	}
	return string(out)
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("biam: hex length odd")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		hi := hexNibble(s[i])
		lo := hexNibble(s[i+1])
		if hi < 0 || lo < 0 {
			return nil, fmt.Errorf("biam: bad hex byte at %d", i)
		}
		out[i/2] = byte(hi<<4 | lo)
	}
	return out, nil
}

func hexNibble(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
