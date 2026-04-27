//go:build linux

package sandbox

import (
	"strings"
	"testing"
)

// Audit fix #203 — bwrap engine refuses profiles whose policy it
// cannot enforce, instead of degrading to no-policy. Three regression
// guards: allowlist network policy, memory limit, cpu_shares.

func TestBuildBwrapArgs_AllowlistRejected(t *testing.T) {
	p := &Profile{Name: "strict", Network: NetworkPolicy{Mode: "allowlist", Allow: []string{"api.openai.com"}}}
	_, err := buildBwrapArgs(p)
	if err == nil {
		t.Fatal("expected error refusing allowlist; got nil")
	}
	if !strings.Contains(err.Error(), "allowlist") || !strings.Contains(err.Error(), "Refusing") {
		t.Errorf("error should call out allowlist + refuse; got: %v", err)
	}
}

func TestBuildBwrapArgs_MemoryLimitRejected(t *testing.T) {
	p := &Profile{Name: "strict", Limits: Limits{MemoryBytes: 512 * 1024 * 1024}}
	_, err := buildBwrapArgs(p)
	if err == nil {
		t.Fatal("expected error refusing memory limit; got nil")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("error should mention memory; got: %v", err)
	}
}

func TestBuildBwrapArgs_CPUSharesRejected(t *testing.T) {
	p := &Profile{Name: "strict", Limits: Limits{CPUShares: 512}}
	_, err := buildBwrapArgs(p)
	if err == nil {
		t.Fatal("expected error refusing cpu_shares; got nil")
	}
	if !strings.Contains(err.Error(), "cpu_shares") {
		t.Errorf("error should mention cpu_shares; got: %v", err)
	}
}

func TestBuildBwrapArgs_ProcessCountRejected(t *testing.T) {
	p := &Profile{Name: "strict", Limits: Limits{ProcessCount: 32}}
	_, err := buildBwrapArgs(p)
	if err == nil {
		t.Fatal("expected error refusing process_count; got nil")
	}
}

func TestBuildBwrapArgs_LoopbackTreatedAsNone(t *testing.T) {
	// Loopback fail-closed semantics: still emits --unshare-net.
	p := &Profile{Name: "strict", Network: NetworkPolicy{Mode: "loopback"}}
	args, err := buildBwrapArgs(p)
	if err != nil {
		t.Fatalf("loopback should be accepted (treated as unshare-net), got: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--unshare-net") {
		t.Errorf("loopback should still pass --unshare-net; got: %v", args)
	}
	if strings.Contains(joined, "--share-net") {
		t.Errorf("loopback must not enable --share-net; got: %v", args)
	}
}

func TestBuildBwrapArgs_OpenAndNoneStillWork(t *testing.T) {
	// Sanity: the two policies bwrap CAN enforce keep working.
	for _, mode := range []string{"open", "none", ""} {
		p := &Profile{Name: "strict", Network: NetworkPolicy{Mode: mode}}
		if _, err := buildBwrapArgs(p); err != nil {
			t.Errorf("mode %q should succeed; got: %v", mode, err)
		}
	}
}
