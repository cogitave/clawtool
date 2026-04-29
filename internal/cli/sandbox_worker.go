// `clawtool sandbox-worker` — runs the sandbox worker (ADR-029
// phase 1). Mirrors `clawtool serve --listen` semantics but for the
// worker leg of the orchestrator+worker pair: bearer-auth'd
// WebSocket endpoint that the daemon dials to route Bash / Read /
// Edit / Write tool calls into an isolated container.
//
// Operator runs this inside a docker / runsc container; the daemon
// is the only trusted dialer. Auth is a shared bearer token; the
// worker reads it from a file or stdin so it never lands in argv.
package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cogitave/clawtool/internal/sandbox/worker"
	"github.com/cogitave/clawtool/internal/xdg"
)

const sandboxWorkerUsage = `Usage: clawtool sandbox-worker [flags]

Runs the sandbox worker on this host (typically inside a docker /
runsc container). The clawtool daemon dials this worker over a
bearer-auth'd WebSocket; tool calls (Bash / Read / Edit / Write)
route here so model-generated code never touches the host process.

Flags:
  --listen <addr>      Listen address. Default ":2024".
  --token-file <path>  Bearer-token file (mode 0600). Default
                       $XDG_CONFIG_HOME/clawtool/worker-token.
  --workdir <path>     Filesystem root the worker resolves paths
                       against. Default "/workspace".
  --init-token         Generate a fresh 32-byte token at the
                       token-file path, print it to stdout, exit.

Operator path:
  clawtool sandbox-worker --init-token
  # ... print token, configure daemon's [sandbox.worker] block ...
  docker run --rm -v $(pwd):/workspace -p 2024:2024 \
    -v $XDG_CONFIG_HOME/clawtool/worker-token:/etc/worker-token:ro \
    clawtool-worker:latest \
    clawtool sandbox-worker --token-file /etc/worker-token
`

func (a *App) runSandboxWorker(argv []string) int {
	if len(argv) > 0 && (argv[0] == "--help" || argv[0] == "-h") {
		fmt.Fprint(a.Stdout, sandboxWorkerUsage)
		return 0
	}

	opts := worker.ServerOptions{
		Listen:  ":2024",
		Workdir: "/workspace",
	}
	tokenPath := defaultWorkerTokenPath()
	initOnly := false

	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--listen":
			if i+1 >= len(argv) {
				fmt.Fprintln(a.Stderr, "clawtool sandbox-worker: --listen requires a value")
				return 2
			}
			opts.Listen = argv[i+1]
			i++
		case "--token-file":
			if i+1 >= len(argv) {
				fmt.Fprintln(a.Stderr, "clawtool sandbox-worker: --token-file requires a path")
				return 2
			}
			tokenPath = argv[i+1]
			i++
		case "--workdir":
			if i+1 >= len(argv) {
				fmt.Fprintln(a.Stderr, "clawtool sandbox-worker: --workdir requires a path")
				return 2
			}
			opts.Workdir = argv[i+1]
			i++
		case "--init-token":
			initOnly = true
		default:
			fmt.Fprintf(a.Stderr, "clawtool sandbox-worker: unknown flag %q\n%s", argv[i], sandboxWorkerUsage)
			return 2
		}
	}

	if initOnly {
		tok, err := initWorkerToken(tokenPath)
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool sandbox-worker: init-token: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Stderr, "wrote worker token to %s (chmod 0600)\n", tokenPath)
		fmt.Fprintln(a.Stdout, tok)
		return 0
	}

	tok, err := readWorkerToken(tokenPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool sandbox-worker: %v\n", err)
		fmt.Fprintln(a.Stderr, "      → clawtool sandbox-worker --init-token (to generate one)")
		return 1
	}
	opts.Token = tok

	if err := worker.Run(context.Background(), opts); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool sandbox-worker: %v\n", err)
		return 1
	}
	return 0
}

func defaultWorkerTokenPath() string {
	return filepath.Join(xdg.ConfigDir(), "worker-token")
}

func readWorkerToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token file %s: %w", path, err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	return tok, nil
}

func initWorkerToken(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}
