// Package biam — Bidirectional Inter-Agent Messaging substrate
// (ADR-015 Phase 1). identity.go owns the per-instance Ed25519
// keypair: every clawtool listener generates one on first launch
// at ~/.config/clawtool/identity.ed25519 and exchanges public keys
// with peers via the trust file (peers.toml). Signed envelopes use
// the private key; receivers verify against the trust map.
//
// The identity file is mode 0600 + 32-byte raw seed; the public key
// is derived deterministically. We don't ship a CA or PKI — peer
// trust is operator-managed (one-line `clawtool peer add`).
package biam

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Identity carries the Ed25519 keypair plus the human-friendly host /
// instance label every signed envelope's `from` field uses.
type Identity struct {
	HostID     string
	InstanceID string
	Public     ed25519.PublicKey
	private    ed25519.PrivateKey // never exported; signing happens through Sign()
}

// LoadOrCreateIdentity reads the seed file at path; creates a new
// keypair on first launch. The host_id and instance_id default to
// the host's hostname + "default" when not set in the seed metadata.
func LoadOrCreateIdentity(path string) (*Identity, error) {
	if path == "" {
		path = DefaultIdentityPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("biam: mkdir identity dir: %w", err)
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return createIdentity(path)
	}
	if err != nil {
		return nil, fmt.Errorf("biam: read identity: %w", err)
	}
	return parseIdentity(body)
}

// DefaultIdentityPath honours XDG_CONFIG_HOME, falls back to HOME.
func DefaultIdentityPath() string {
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		return filepath.Join(v, "clawtool", "identity.ed25519")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "clawtool", "identity.ed25519")
	}
	return "identity.ed25519"
}

// Sign produces the signature for the canonical-JSON envelope.
func (i *Identity) Sign(message []byte) []byte {
	if i == nil || i.private == nil {
		return nil
	}
	return ed25519.Sign(i.private, message)
}

// Verify checks a signature against a peer's known public key.
func Verify(pub ed25519.PublicKey, message, signature []byte) bool {
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(pub, message, signature)
}

// PublicKeyB64 returns the public key encoded as `ed25519:<hex>` —
// the format the peers.toml file stores.
func (i *Identity) PublicKeyB64() string {
	return "ed25519:" + hex.EncodeToString(i.Public)
}

// ParsePublicKey decodes the `ed25519:<hex>` form back into a key.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "ed25519:") {
		return nil, fmt.Errorf("biam: public key missing ed25519: prefix: %q", s)
	}
	raw, err := hex.DecodeString(s[len("ed25519:"):])
	if err != nil {
		return nil, fmt.Errorf("biam: decode public key hex: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("biam: public key wrong length: got %d, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// ── internals ──────────────────────────────────────────────────────

// createIdentity generates a fresh keypair, writes it 0600, returns the
// loaded Identity. Host / instance default to hostname + "default" but
// can be overridden later via SetLabel.
func createIdentity(path string) (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("biam: generate keypair: %w", err)
	}
	id := &Identity{
		HostID:     defaultHostID(),
		InstanceID: "default",
		Public:     pub,
		private:    priv,
	}
	if err := writeIdentity(path, id); err != nil {
		return nil, err
	}
	return id, nil
}

// parseIdentity decodes the identity file body (private-key-seed +
// optional metadata). On-disk format is intentionally minimal:
//
//	host_id=<host>
//	instance_id=<instance>
//	private=<hex 64 bytes>
//
// Lines starting with `#` are ignored.
func parseIdentity(body []byte) (*Identity, error) {
	id := &Identity{HostID: defaultHostID(), InstanceID: "default"}
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "host_id":
			id.HostID = v
		case "instance_id":
			id.InstanceID = v
		case "private":
			seed, err := hex.DecodeString(v)
			if err != nil || len(seed) != ed25519.PrivateKeySize {
				return nil, fmt.Errorf("biam: malformed private key (want %d bytes hex, got %d)", ed25519.PrivateKeySize, len(seed))
			}
			id.private = ed25519.PrivateKey(seed)
			id.Public = id.private.Public().(ed25519.PublicKey)
		}
	}
	if id.private == nil {
		return nil, errors.New("biam: identity file missing private= line")
	}
	return id, nil
}

func writeIdentity(path string, id *Identity) error {
	body := fmt.Sprintf("# clawtool BIAM identity (ADR-015) — keep mode 0600\nhost_id=%s\ninstance_id=%s\nprivate=%s\n",
		id.HostID, id.InstanceID, hex.EncodeToString(id.private),
	)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return fmt.Errorf("biam: write identity: %w", err)
	}
	return os.Rename(tmp, path)
}

func defaultHostID() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		// strip dots so the address form `claw://host/instance` stays
		// filesystem-friendly.
		return strings.ReplaceAll(h, ".", "-")
	}
	return "localhost"
}
