// Package sysproc — small cross-platform process helpers used by
// the CLI surface. OpenBrowser launches the user's default browser
// to a URL via the OS-native handler (xdg-open on Linux, open on
// macOS, rundll32 on Windows). Used by `clawtool star` when the
// OAuth flow needs the user to authorise + by `--no-oauth` mode
// when we just want to land them on the official action page.
//
// The function is intentionally non-blocking: it kicks the OS
// handler and returns. The handler then forks the user-space
// browser process; we never inherit that process's exit code,
// which is the point — the user's browser shouldn't tie up the
// CLI.
package sysproc

import (
	"errors"
	"os/exec"
	"runtime"
)

// ErrUnsupportedPlatform is returned when OpenBrowser doesn't have
// a launcher recipe for the current GOOS. Callers can surface a
// "copy this URL into your browser" fallback instead of failing
// hard.
var ErrUnsupportedPlatform = errors.New("sysproc: no browser launcher for this OS")

// OpenBrowser asks the OS to open url in the user's default
// browser. Returns nil if the launcher process started cleanly
// (the actual browser may take a moment to render); returns the
// launcher's error otherwise. Does NOT validate the URL — the
// caller is responsible for the value's safety.
func OpenBrowser(url string) error {
	cmd, err := browserCmd(url)
	if err != nil {
		return err
	}
	// Detached start; we don't Wait. The browser may keep
	// running long after the CLI exits; reaping it would block
	// the CLI on a window the user is actively using.
	return cmd.Start()
}

// browserCmd builds the *exec.Cmd for the current OS. Split out so
// the OS dispatch is testable on each platform without touching the
// network or actually launching anything.
func browserCmd(url string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("xdg-open", url), nil
	case "darwin":
		return exec.Command("open", url), nil
	case "windows":
		// rundll32 is the conventional way to invoke the
		// Windows shell URL handler without spawning a cmd.exe
		// window. Equivalent to double-clicking a .url shortcut.
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url), nil
	default:
		return nil, ErrUnsupportedPlatform
	}
}
