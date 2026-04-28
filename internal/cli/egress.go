// `clawtool egress` — runs the egress allowlist proxy (ADR-029
// phase 4, task #209). Sandbox workers route their HTTP_PROXY /
// HTTPS_PROXY through this binary so model-generated network
// calls pass through an explicit allowlist before reaching the
// host network.
//
// Operator path:
//
//	clawtool egress --listen :3128 \
//	    --allow api.openai.com,api.anthropic.com,.github.com
//
// In the worker container:
//
//	docker run -e HTTP_PROXY=http://egress:3128 \
//	           -e HTTPS_PROXY=http://egress:3128 \
//	           clawtool-worker:0.21 ...
package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/cogitave/clawtool/internal/sandbox/egress"
)

const egressUsage = `Usage: clawtool egress [flags]

Run the egress allowlist proxy. Sandbox workers route their
HTTP_PROXY / HTTPS_PROXY through this binary; outbound calls to
hosts not on the allowlist get a 403 with x-deny-reason.

Flags:
  --listen <addr>    Listen address. Default ":3128".
  --allow <list>     Comma-separated host allowlist. Each entry
                     matches an exact host (e.g. "api.openai.com")
                     or a suffix when prefixed with "."
                     (e.g. ".openai.com"). Pass "*" to allow
                     everything (debug only).
  --token-file <p>   Optional bearer token file (mode 0600). When
                     set, clients must present
                     Proxy-Authorization: Bearer <token>.

Operator path:
  clawtool egress --listen :3128 \
      --allow api.openai.com,api.anthropic.com,.github.com
`

func (a *App) runEgress(argv []string) int {
	if len(argv) > 0 && (argv[0] == "--help" || argv[0] == "-h") {
		fmt.Fprint(a.Stdout, egressUsage)
		return 0
	}
	opts := egress.Options{Listen: ":3128"}
	tokenPath := ""
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--listen":
			if i+1 >= len(argv) {
				fmt.Fprintln(a.Stderr, "clawtool egress: --listen requires a value")
				return 2
			}
			opts.Listen = argv[i+1]
			i++
		case "--allow":
			if i+1 >= len(argv) {
				fmt.Fprintln(a.Stderr, "clawtool egress: --allow requires a value")
				return 2
			}
			for _, h := range strings.Split(argv[i+1], ",") {
				if h = strings.TrimSpace(h); h != "" {
					opts.Allow = append(opts.Allow, h)
				}
			}
			i++
		case "--token-file":
			if i+1 >= len(argv) {
				fmt.Fprintln(a.Stderr, "clawtool egress: --token-file requires a path")
				return 2
			}
			tokenPath = argv[i+1]
			i++
		default:
			fmt.Fprintf(a.Stderr, "clawtool egress: unknown flag %q\n%s", argv[i], egressUsage)
			return 2
		}
	}
	if tokenPath != "" {
		tok, err := readWorkerToken(tokenPath) // reuses sandbox-worker token loader
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool egress: %v\n", err)
			return 1
		}
		opts.Token = tok
	}
	if err := egress.Run(context.Background(), opts); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool egress: %v\n", err)
		return 1
	}
	return 0
}
