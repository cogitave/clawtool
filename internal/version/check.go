// Package version — `clawtool version --check` runner.
//
// Wraps CheckForUpdate with CLI-shaped output + exit codes so a
// monitoring script (CI cron, system-package wrapper, fleet
// inventory) can pipeline-detect out-of-date clawtool installs
// without parsing human banners. The cache + GitHub-API logic
// lives in update.go; this file is purely the rendering /
// exit-code layer.
//
// Exit-code contract (stable, scripts will gate on it):
//
//	0 — up-to-date (running binary's version >= latest tag)
//	1 — newer release available
//	2 — check itself failed (network, parse, rate-limit)
//
// Anyone changing these values needs to rev the wiki/decisions
// doc that promises them — they're now part of the public CLI
// contract.
package version

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// CheckResult is the structured payload of a `version --check`
// invocation. snake_case JSON tags follow the project-wide wire
// convention (mirrors BuildInfo / agents.Status / agentListEntry).
//
// `Up` collapses HasUpdate + Err into the binary "are we current?"
// signal a script actually wants — a check failure is *not* an
// affirmative "up to date" answer (Up=false, Error!="" → exit 2).
type CheckResult struct {
	Up        bool      `json:"up_to_date"`
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
	Error     string    `json:"error,omitempty"`
}

// ExitCode classifies a result into the documented CLI exit codes
// (0 / 1 / 2). Centralised here so both RunCheck and any future
// callers (HTTP probe, scripted wrapper) agree on the mapping.
func (r CheckResult) ExitCode() int {
	if r.Error != "" {
		return 2
	}
	if r.Up {
		return 0
	}
	return 1
}

// RunCheck performs an update probe, renders the result to w,
// and returns the CLI exit code. When jsonOutput is true the
// rendered form is the indented JSON marshal of CheckResult;
// otherwise a short human banner that names the current + latest
// versions and points at `clawtool upgrade` when there's drift.
//
// The probe routes through CheckForUpdate, which honours the
// 5-minute on-disk cache so scripts that gate on this every loop
// don't hammer the GitHub API.
func RunCheck(ctx context.Context, jsonOutput bool, w io.Writer) int {
	info := CheckForUpdate(ctx)
	res := CheckResult{
		Current:   info.Current,
		Latest:    info.Latest,
		FetchedAt: info.FetchedAt.UTC(),
		Up:        info.Err == nil && !info.HasUpdate,
	}
	if info.Err != nil {
		res.Error = info.Err.Error()
	}

	if jsonOutput {
		body, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(w, `{"error":%q}`+"\n", err.Error())
			return 2
		}
		fmt.Fprintln(w, string(body))
		return res.ExitCode()
	}

	switch {
	case res.Error != "":
		fmt.Fprintf(w, "could not check for updates: %s\n", res.Error)
	case res.Up:
		fmt.Fprintf(w, "up to date: clawtool %s", res.Current)
		if res.Latest != "" {
			fmt.Fprintf(w, " (latest %s)", res.Latest)
		}
		fmt.Fprintln(w)
	default:
		fmt.Fprintf(w, "update available: latest %s, current %s\n", res.Latest, res.Current)
		fmt.Fprintln(w, "run `clawtool upgrade` to install.")
	}
	return res.ExitCode()
}
