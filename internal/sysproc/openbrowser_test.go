package sysproc

import (
	"runtime"
	"strings"
	"testing"
)

// browserCmd is the unit under test — we don't actually launch a
// browser in CI. We just assert that on each supported platform
// the right launcher binary + arg shape gets composed, and on
// unsupported platforms we surface ErrUnsupportedPlatform cleanly.

func TestBrowserCmd_PerPlatformShape(t *testing.T) {
	cmd, err := browserCmd("https://example.com/x?y=1")
	switch runtime.GOOS {
	case "linux":
		if err != nil {
			t.Fatalf("linux: unexpected error %v", err)
		}
		if !strings.HasSuffix(cmd.Path, "xdg-open") && cmd.Args[0] != "xdg-open" {
			t.Errorf("linux: launcher = %q (args[0]=%q), want xdg-open", cmd.Path, cmd.Args[0])
		}
		if cmd.Args[len(cmd.Args)-1] != "https://example.com/x?y=1" {
			t.Errorf("linux: url arg lost: %v", cmd.Args)
		}
	case "darwin":
		if err != nil {
			t.Fatalf("darwin: unexpected error %v", err)
		}
		if !strings.HasSuffix(cmd.Path, "open") && cmd.Args[0] != "open" {
			t.Errorf("darwin: launcher = %q (args[0]=%q), want open", cmd.Path, cmd.Args[0])
		}
	case "windows":
		if err != nil {
			t.Fatalf("windows: unexpected error %v", err)
		}
		if !strings.Contains(cmd.Path, "rundll32") && cmd.Args[0] != "rundll32" {
			t.Errorf("windows: launcher = %q (args[0]=%q), want rundll32", cmd.Path, cmd.Args[0])
		}
		if cmd.Args[1] != "url.dll,FileProtocolHandler" {
			t.Errorf("windows: shell-handler arg lost: %v", cmd.Args)
		}
	default:
		if err != ErrUnsupportedPlatform {
			t.Errorf("unsupported %s: want ErrUnsupportedPlatform, got %v", runtime.GOOS, err)
		}
	}
}
