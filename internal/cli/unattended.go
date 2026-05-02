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
  clawtool unattended verify <id>        Verify the Ed25519 signatures on a session's
                                          audit log. Reports valid / invalid / malformed
                                          line counts so a tamper attempt surfaces
                                          deterministically.

Aliases: ` + "`clawtool yolo`" + ` is a synonym for ` + "`clawtool unattended`" + `.

Disclosure: when --unattended is first invoked from a repo without
a trust grant, clawtool prints the full per-instance flag list
(--dangerously-skip-permissions for Claude Code, etc.) and refuses
to dispatch until the operator confirms. Use this command to
inspect / pre-grant / revoke trust without going through the
disclosure flow.

Audit: every unattended dispatch appends to
  ~/.local/share/clawtool/sessions/<id>/audit.jsonl
Each line is wrapped {event, sig} — the sig is an Ed25519 signature
over the canonical JSON of event, computed with the local BIAM
identity. ` + "`clawtool unattended verify <id>`" + ` reads the log back
and reports tamper-evidence. The audit log is non-optional; it's
the only way to investigate an unattended session after the fact.
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
	case "verify":
		return a.runUnattendedVerify(argv[1:])
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

// runUnattendedVerify walks the JSONL audit log for the given
// session_id and reports the count of valid / invalid / malformed
// signed entries. Exit code 0 only when every line verifies — so
// `clawtool unattended verify <id>` doubles as a CI-friendly
// tamper-evidence check (`if !; then bail`).
func (a *App) runUnattendedVerify(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(a.Stderr, "clawtool unattended verify: session_id required")
		fmt.Fprintln(a.Stderr, "  usage: clawtool unattended verify <session_id>")
		return 2
	}
	sessionID := argv[0]
	report, err := unattended.VerifySession(sessionID, nil)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool unattended verify: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "session: %s\n", report.SessionID)
	fmt.Fprintf(a.Stdout, "audit:   %s\n", report.AuditPath)
	fmt.Fprintf(a.Stdout, "total: %d · valid: %d · invalid: %d · malformed: %d\n",
		report.Total, report.Valid, report.Invalid, report.Malformed)
	if report.Invalid > 0 || report.Malformed > 0 {
		fmt.Fprintln(a.Stdout, "✗ audit log NOT clean — at least one line failed signature verification or didn't parse")
		return 1
	}
	fmt.Fprintln(a.Stdout, "✓ every line verifies")
	return 0
}
