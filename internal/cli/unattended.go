// Package cli — `clawtool unattended` subcommand. Operator-facing
// trust management for ADR-023's --unattended dispatch mode.
//
// Two surfaces:
//
//	clawtool unattended status [<repo>]    show whether <repo> (or cwd) is trusted
//	clawtool unattended grant  [<repo>]    explicitly trust <repo> for unattended dispatch
//	clawtool unattended revoke [<repo>]    remove the trust grant
//	clawtool unattended list               list every granted repo
//	clawtool unattended path                print the trust file location
//
// `clawtool yolo` is a deliberately-jokey alias so operators
// searching docs / muscle-memory the Cline term find it.
package cli

import (
	"fmt"
	"os"

	"github.com/cogitave/clawtool/internal/unattended"
)

const unattendedUsage = `Usage:
  clawtool unattended status [<repo>]    Show whether <repo> (or cwd) is trusted.
  clawtool unattended grant  [<repo>]    Explicitly trust <repo> for unattended dispatch.
                                          Subsequent ` + "`clawtool send --unattended`" + ` calls from
                                          this repo skip the disclosure prompt.
  clawtool unattended revoke [<repo>]    Remove the trust grant.
  clawtool unattended list               List every trusted repo.
  clawtool unattended path               Print the trust file location.

Aliases: ` + "`clawtool yolo`" + ` is a synonym for ` + "`clawtool unattended`" + `.

Disclosure: when --unattended is first invoked from a repo without
a trust grant, clawtool prints the full per-instance flag list
(--dangerously-skip-permissions for Claude Code, etc.) and refuses
to dispatch until the operator confirms. Use this command to
inspect / pre-grant / revoke trust without going through the
disclosure flow.

Audit: every unattended dispatch appends to
  ~/.local/share/clawtool/sessions/<id>/audit.jsonl
The audit log is non-optional; it's the only way to investigate
an unattended session after the fact.
`

func (a *App) runUnattended(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, unattendedUsage)
		return 2
	}
	switch argv[0] {
	case "status":
		return a.runUnattendedStatus(argv[1:])
	case "grant":
		return a.runUnattendedGrant(argv[1:])
	case "revoke":
		return a.runUnattendedRevoke(argv[1:])
	case "list":
		return a.runUnattendedList(argv[1:])
	case "path":
		fmt.Fprintln(a.Stdout, unattended.TrustFilePath())
		return 0
	default:
		fmt.Fprintf(a.Stderr, "clawtool unattended: unknown subcommand %q\n\n%s",
			argv[0], unattendedUsage)
		return 2
	}
}

func (a *App) repoArg(argv []string) (string, error) {
	if len(argv) > 0 {
		return argv[0], nil
	}
	return os.Getwd()
}

func (a *App) runUnattendedStatus(argv []string) int {
	repo, err := a.repoArg(argv)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool unattended status: %v\n", err)
		return 1
	}
	trusted, err := unattended.IsTrusted(repo)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool unattended status: %v\n", err)
		return 1
	}
	if trusted {
		fmt.Fprintf(a.Stdout, "✓ trusted: %s\n", repo)
		return 0
	}
	fmt.Fprintf(a.Stdout, "✗ NOT trusted: %s\n", repo)
	fmt.Fprintln(a.Stdout, "  run `clawtool unattended grant` to trust this repo without going through the disclosure flow")
	return 0
}

func (a *App) runUnattendedGrant(argv []string) int {
	repo, err := a.repoArg(argv)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool unattended grant: %v\n", err)
		return 1
	}
	// Print the disclosure panel synchronously so a `grant` call
	// is also a sober moment, not a silent toggle.
	fmt.Fprint(a.Stderr, unattended.DisclosurePanel(repo))
	if err := unattended.Grant(repo, "granted via `clawtool unattended grant`"); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool unattended grant: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "✓ trust granted: %s\n", repo)
	return 0
}

func (a *App) runUnattendedRevoke(argv []string) int {
	repo, err := a.repoArg(argv)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool unattended revoke: %v\n", err)
		return 1
	}
	gone, err := unattended.Revoke(repo)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool unattended revoke: %v\n", err)
		return 1
	}
	if !gone {
		fmt.Fprintf(a.Stdout, "(no grant for %s — nothing to revoke)\n", repo)
		return 0
	}
	fmt.Fprintf(a.Stdout, "✓ trust revoked: %s\n", repo)
	return 0
}

func (a *App) runUnattendedList(_ []string) int {
	// We don't expose the parsed slice publicly — print the
	// trust file directly so the operator sees the canonical
	// shape (path, granted_at, optional note).
	body, err := os.ReadFile(unattended.TrustFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(a.Stdout, "(no grants yet — `clawtool unattended grant` to add one)")
			return 0
		}
		fmt.Fprintf(a.Stderr, "clawtool unattended list: %v\n", err)
		return 1
	}
	if _, err := a.Stdout.Write(body); err != nil {
		return 1
	}
	return 0
}
