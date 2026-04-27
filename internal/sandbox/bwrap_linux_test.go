//go:build linux

package sandbox

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestBwrap_AvailableOnHost(t *testing.T) {
	if !(bwrapEngine{}).Available() {
		t.Skip("bwrap not on PATH; integration test skipped")
	}
}

func TestBwrap_BuildArgs_NoNetByDefault(t *testing.T) {
	args, err := buildBwrapArgs(&Profile{
		Network: NetworkPolicy{Mode: "none"},
		Paths: []PathRule{
			{Path: "/usr", Mode: ModeReadOnly},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--unshare-net") {
		t.Errorf("none policy should --unshare-net; got %s", joined)
	}
	if !strings.Contains(joined, "--die-with-parent") {
		t.Errorf("baseline must include --die-with-parent: %s", joined)
	}
	if !strings.Contains(joined, "--ro-bind-try /usr /usr") {
		t.Errorf("ro path missing: %s", joined)
	}
}

func TestBwrap_BuildArgs_OpenSharesNet(t *testing.T) {
	args, err := buildBwrapArgs(&Profile{
		Network: NetworkPolicy{Mode: "open"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--share-net") {
		t.Errorf("open policy should --share-net: %s", joined)
	}
}

func TestBwrap_BuildArgs_RWBind(t *testing.T) {
	args, _ := buildBwrapArgs(&Profile{
		Network: NetworkPolicy{Mode: "none"},
		Paths: []PathRule{
			{Path: "/tmp/work", Mode: ModeReadWrite},
		},
	})
	if !strings.Contains(strings.Join(args, " "), "--bind-try /tmp/work /tmp/work") {
		t.Errorf("rw bind missing: %v", args)
	}
}

func TestBwrap_BuildArgs_EnvAllowAndDeny(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("AWS_SECRET", "do-not-leak")
	t.Setenv("HOME", "/home/test")

	args, _ := buildBwrapArgs(&Profile{
		Network: NetworkPolicy{Mode: "none"},
		Env: EnvPolicy{
			Allow: []string{"PATH", "HOME", "AWS_*"},
			Deny:  []string{"AWS_*"},
		},
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--setenv PATH /usr/bin") {
		t.Errorf("PATH should pass through: %s", joined)
	}
	if !strings.Contains(joined, "--setenv HOME /home/test") {
		t.Errorf("HOME should pass through: %s", joined)
	}
	if strings.Contains(joined, "AWS_SECRET") {
		t.Errorf("AWS_SECRET must be denied even though AWS_* is allowed: %s", joined)
	}
}

// TestBwrap_LiveCat actually runs a sandboxed `cat`. Skipped
// when bwrap isn't on PATH.
func TestBwrap_LiveCat(t *testing.T) {
	if !(bwrapEngine{}).Available() {
		t.Skip("bwrap not available")
	}
	cmd := exec.Command("/bin/cat", "/etc/hostname")
	profile := &Profile{
		Network: NetworkPolicy{Mode: "none"},
		Paths: []PathRule{
			{Path: "/usr", Mode: ModeReadOnly},
			{Path: "/bin", Mode: ModeReadOnly},
			{Path: "/lib", Mode: ModeReadOnly},
			{Path: "/lib64", Mode: ModeReadOnly},
			{Path: "/etc", Mode: ModeReadOnly},
		},
		Env: EnvPolicy{Allow: []string{"PATH", "LANG"}},
	}
	if err := (bwrapEngine{}).Wrap(context.Background(), cmd, profile); err != nil {
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandboxed cat failed: %v\n%s", err, out)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		t.Errorf("expected hostname output, got empty")
	}
}

// TestBwrap_LiveNetUnshare verifies network is actually
// disabled — `cat /etc/resolv.conf` should still work (file
// access) but a network call should fail.
func TestBwrap_LiveNetUnshare(t *testing.T) {
	if !(bwrapEngine{}).Available() {
		t.Skip("bwrap not available")
	}
	// Use bash to attempt a TCP connect via /dev/tcp; bash is
	// usually present and the failure is a clear signal the
	// network namespace is empty.
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH; skipping live net test")
	}
	cmd := exec.Command(bashPath, "-c", "echo > /dev/tcp/1.1.1.1/53")
	profile := &Profile{
		Network: NetworkPolicy{Mode: "none"},
		Paths: []PathRule{
			{Path: "/usr", Mode: ModeReadOnly},
			{Path: "/bin", Mode: ModeReadOnly},
			{Path: "/lib", Mode: ModeReadOnly},
			{Path: "/lib64", Mode: ModeReadOnly},
		},
		Env: EnvPolicy{Allow: []string{"PATH"}},
	}
	if err := (bwrapEngine{}).Wrap(context.Background(), cmd, profile); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(); err == nil {
		t.Error("expected sandboxed bash to fail TCP connect (network unshared) but it succeeded")
	}
}
