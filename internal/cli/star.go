package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/github"
	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/cogitave/clawtool/internal/sysproc"
)

const starUsage = `Usage:
  clawtool star                  Star cogitave/clawtool on GitHub. Walks
                                 you through the OAuth Device Flow:
                                 prints a short user-code, opens GitHub's
                                 verification page in your browser, polls
                                 until you authorise, then PUTs the star
                                 via api.github.com on your behalf.
  clawtool star --no-oauth       Skip OAuth — just open the repo's star
                                 page in your default browser. Use this
                                 when OAuth is blocked or you'd rather
                                 click Star manually.
  clawtool star --owner <o> --repo <r>
                                 Override the target. Defaults to
                                 cogitave/clawtool.

Why OAuth: clawtool only ever stars on your behalf using GitHub's
documented authenticated REST endpoint. We never replay your
github.com session cookies; the user-code + browser confirmation
is the security boundary. Token is held in the OS-typed secrets
store (~/.config/clawtool/secrets.toml, mode 0600) so re-running
` + "`clawtool star`" + ` doesn't re-authorise you.
`

// runStar is the `clawtool star` subcommand. It implements the
// OAuth Device Flow path described in ADR-031: explicit consent,
// official authenticated endpoint, no CSRF replay. Falls back to
// opening the public star page in the user's browser when OAuth
// isn't available (no client_id baked in) or the user declines
// with --no-oauth.
func (a *App) runStar(argv []string) int {
	noOAuth := false
	owner := "cogitave"
	repo := "clawtool"
	for i := 0; i < len(argv); i++ {
		v := argv[i]
		switch v {
		case "--help", "-h":
			fmt.Fprint(a.Stderr, starUsage)
			return 0
		case "--no-oauth":
			noOAuth = true
		case "--owner":
			if i+1 >= len(argv) {
				fmt.Fprintln(a.Stderr, "clawtool star: --owner requires a value")
				return 2
			}
			owner = argv[i+1]
			i++
		case "--repo":
			if i+1 >= len(argv) {
				fmt.Fprintln(a.Stderr, "clawtool star: --repo requires a value")
				return 2
			}
			repo = argv[i+1]
			i++
		default:
			fmt.Fprintf(a.Stderr, "clawtool star: unknown flag %q\n\n%s", v, starUsage)
			return 2
		}
	}

	ux := newUpgradeUX(a.Stdout)
	ux.HeaderDelta(fmt.Sprintf("⭐ %s/%s", owner, repo), "your authorised star")

	if noOAuth {
		return openStarPageFallback(a, ux, owner, repo, "user opted out of OAuth (--no-oauth)")
	}

	client := github.NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// If we already have a token from a previous run, re-use it.
	// The user is implicitly opting back in by re-running
	// `clawtool star` — we don't ask twice.
	if token, ok := loadStarToken(); ok {
		ux.PhaseStart("Using stored authorisation")
		if err := client.StarRepo(ctx, token, owner, repo); err == nil {
			ux.PhaseDone(fmt.Sprintf("%s/%s starred", owner, repo))
			ux.NextSteps([]string{
				"Thanks for the star — it actually does help us see who finds the project useful.",
				"clawtool star --owner X --repo Y    star a different repo on your behalf",
			})
			return 0
		} else {
			// Stored token failed (revoked, expired, scope
			// changed). Drop it and fall through to a fresh
			// device flow. We don't surface the reject body
			// — most likely cause is the user revoked the
			// app, and the Device Flow re-asks them anyway.
			ux.PhaseFail(err.Error(), "stored token rejected — re-running authorisation")
			deleteStarToken()
		}
	}

	ux.PhaseStart("Requesting GitHub device code")
	dc, err := client.RequestDeviceCode(ctx, "public_repo")
	if err != nil {
		if errors.Is(err, github.ErrNoClientID) {
			ux.PhaseFail("clawtool's GitHub OAuth client_id is not configured in this build",
				"falling back to browser-redirect — click Star manually on the page that opens")
			return openStarPageFallback(a, ux, owner, repo, "OAuth client_id not baked in")
		}
		ux.PhaseFail(err.Error(), "check network / GitHub status; --no-oauth opens the star page directly")
		return 1
	}
	ux.PhaseDone(fmt.Sprintf("expires in %ds, polling every %s", dc.ExpiresIn, dc.PollEvery))

	// Show the user-code + verification URL, AND launch the
	// browser to verification_uri so they don't have to
	// copy-paste. The browser launch is best-effort — a
	// headless / SSH session falls back to the printed URL.
	ux.Section("Authorise clawtool on GitHub")
	fmt.Fprintf(a.Stdout, "    Open in browser: %s\n", dc.VerificationURI)
	fmt.Fprintf(a.Stdout, "    Enter this code: %s\n", dc.UserCode)
	fmt.Fprintln(a.Stdout)
	if err := sysproc.OpenBrowser(dc.VerificationURI); err != nil {
		ux.Note(fmt.Sprintf("couldn't auto-open browser (%v) — paste the URL above manually", err))
	} else {
		ux.Note("browser launched — switch to it, paste the code, hit Authorize")
	}

	ux.PhaseStart("Waiting for you to authorise")
	token, err := client.PollAccessToken(ctx, dc)
	if err != nil {
		switch {
		case errors.Is(err, github.ErrAuthorizationDenied):
			ux.PhaseFail("authorisation denied",
				"--no-oauth opens the star page directly so you can click Star yourself")
			return 1
		case errors.Is(err, github.ErrDeviceCodeExpired):
			ux.PhaseFail("device code expired before authorisation",
				"re-run `clawtool star` to start a fresh code")
			return 1
		default:
			ux.PhaseFail(err.Error(), "")
			return 1
		}
	}
	ux.PhaseDone("token acquired")

	// Stash for next time so the user doesn't re-authorise on
	// every star. 0600 file under XDG_CONFIG_HOME (the secrets
	// package owns the path policy).
	saveStarToken(token)

	ux.PhaseStart(fmt.Sprintf("Starring %s/%s on your behalf", owner, repo))
	if err := client.StarRepo(ctx, token, owner, repo); err != nil {
		ux.PhaseFail(err.Error(), "the token was acquired but the PUT failed; try `clawtool star` again")
		return 1
	}
	ux.PhaseDone("PUT /user/starred succeeded")

	ux.NextSteps([]string{
		"Thanks for the star — it's the explicit kind, recorded against your GitHub account, not a vanity inflate.",
		"clawtool star --owner X --repo Y    star a different repo with the same authorisation",
		"Revoke any time:                    https://github.com/settings/applications",
	})
	return 0
}

// openStarPageFallback launches the user's default browser to the
// repo's star page. Used when OAuth is unavailable or the user
// opts out. The user clicks Star themselves on GitHub's UI; we
// don't touch their session.
func openStarPageFallback(a *App, ux *upgradeUX, owner, repo, reason string) int {
	url := github.StarPageURL(owner, repo)
	if reason != "" {
		ux.Note(reason)
	}
	ux.PhaseStart(fmt.Sprintf("Opening %s in your browser", url))
	if err := sysproc.OpenBrowser(url); err != nil {
		ux.PhaseFail(err.Error(), "open the URL manually: "+url)
		return 1
	}
	ux.PhaseDone("you can click Star on GitHub directly")
	ux.NextSteps([]string{
		"Click the Star button on GitHub's page — the explicit, no-replay path.",
		fmt.Sprintf("Direct link: %s", url),
	})
	return 0
}

// loadStarToken pulls the cached OAuth token from the user-scoped
// secrets file. Empty string + ok=false when no token has been
// stored yet.
func loadStarToken() (string, bool) {
	store, err := secrets.LoadOrEmpty(secrets.DefaultPath())
	if err != nil {
		return "", false
	}
	v, ok := store.Get("github", "oauth_token")
	return strings.TrimSpace(v), ok && v != ""
}

// saveStarToken caches the OAuth token under the user's secrets
// file. Best-effort — a save failure doesn't fail the star
// command (the action still happened); the user just re-authorises
// next time.
func saveStarToken(token string) {
	store, err := secrets.LoadOrEmpty(secrets.DefaultPath())
	if err != nil {
		return
	}
	store.Set("github", "oauth_token", token)
	_ = store.Save(secrets.DefaultPath())
}

// deleteStarToken removes the cached token. Called when a stored
// token is rejected (revoked / scope changed) so the next run
// starts a clean device flow.
func deleteStarToken() {
	store, err := secrets.LoadOrEmpty(secrets.DefaultPath())
	if err != nil {
		return
	}
	store.Delete("github", "oauth_token")
	_ = store.Save(secrets.DefaultPath())
}
