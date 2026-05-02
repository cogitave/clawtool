// Package cli — `clawtool watchers list / tail` verbs.
//
// Tag-after-ship watchers historically ran as bare bash scripts
// under `/tmp/clawtool-tag-watcher*.{sh,log}`. They worked, but
// were invisible to every clawtool UI surface — operator could not
// see what the loop was waiting for from Claude Code, dashboard,
// or `clawtool overview`. The autodev Stop-hook prompt also
// couldn't tell the model "v0.22.X is on its way" because the
// state lived in raw log files no one was reading.
//
// `clawtool watchers list` parses every `/tmp/clawtool-tag-watcher*.log`,
// extracts the latest pollN status + target tag + ship time, and
// renders a table. `clawtool watchers tail [N]` returns the last
// N log lines for a single watcher (default: latest). The same
// data feeds the autodev hook prompt so the model reads it on
// every self-trigger.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// tagPattern catches every clawtool tag form across watcher log
// generations: `tag=v0.22.154`, `tagged v0.22.154 @ ...`,
// `[wN] DONE — v0.22.154 pushed`, etc. Last match wins so the
// most recent tag in the log surfaces.
var tagPattern = regexp.MustCompile(`v\d+\.\d+\.\d+`)

const watchersUsage = `Usage:
  clawtool watchers list           Table of every /tmp/clawtool-tag-watcher*.log
                                   with target tag, latest poll status, and
                                   tagged-or-still-polling state.
  clawtool watchers tail [<N>] [--id <num>]
                                   Tail the last N (default 30) lines of one
                                   watcher's log. --id picks the watcher
                                   number; omit to use the most recent.

Watchers are bash scripts under /tmp/ that poll a CI run via
'gh run view --json status' and tag origin/main when CI lands green.
Each watcher gets a numbered log: /tmp/clawtool-tag-watcher<N>.log.
Read-only — these verbs never spawn or kill watchers.
`

// runWatchers dispatches the verb. Args after the subcommand are
// passed through.
func (a *App) runWatchers(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, watchersUsage)
		return 2
	}
	switch argv[0] {
	case "list":
		return a.runWatchersList()
	case "tail":
		return a.runWatchersTail(argv[1:])
	case "--help", "-h", "help":
		fmt.Fprint(a.Stdout, watchersUsage)
		return 0
	default:
		fmt.Fprintf(a.Stderr, "clawtool watchers: unknown subcommand %q\n\n%s", argv[0], watchersUsage)
		return 2
	}
}

// WatcherSnapshot is the parsed state of one tag-watcher log.
// Exported because the autodev hook prompt also reads these.
type WatcherSnapshot struct {
	ID         int    // watcher number (e.g. 55 for tag-watcher55.log)
	LogPath    string // /tmp/clawtool-tag-watcher55.log
	Tag        string // v0.22.154 (parsed from "tag=vX.Y.Z" or "tagged ... @")
	Target     string // commit SHA the watcher's RUN_ID belongs to (best-effort)
	Status     string // last poll status: in_progress / completed / no-poll-yet / tagged / refused
	Conclusion string // last poll conclusion: success / failure / cancelled / "" / tagged
	Tagged     bool   // saw "tagged X @ Y" line
	LastLine   string // last non-empty line, for one-glance triage
}

// listWatchers globs and parses every tag-watcher log. Returns
// snapshots sorted by ID ascending.
func listWatchers() []WatcherSnapshot {
	matches, _ := filepath.Glob("/tmp/clawtool-tag-watcher*.log")
	var snaps []WatcherSnapshot
	for _, p := range matches {
		s := parseWatcherLog(p)
		snaps = append(snaps, s)
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].ID < snaps[j].ID })
	return snaps
}

// parseWatcherLog reads one log and extracts the structured snapshot.
// Tolerates partial / mid-write logs — every field defaults to empty.
func parseWatcherLog(path string) WatcherSnapshot {
	s := WatcherSnapshot{LogPath: path, Status: "no-poll-yet"}
	base := filepath.Base(path)
	// Extract digits from "clawtool-tag-watcher55.log".
	if n := extractWatcherID(base); n > 0 {
		s.ID = n
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s.LastLine = line
		// Capture every vX.Y.Z occurrence — last one wins so the
		// most recent tag in the log surfaces (works across watcher
		// generations: tag=, tagged, [wN] DONE).
		if m := tagPattern.FindString(line); m != "" {
			s.Tag = m
		}
		switch {
		case strings.Contains(line, "tag="):
			// "[2026-...] watcher55 start (target=... tag=vX.Y.Z)"
			if t := extractAfter(line, "tag="); t != "" {
				s.Tag = strings.TrimRight(t, " )")
			}
			if t := extractAfter(line, "target="); t != "" {
				s.Target = strings.SplitN(t, " ", 2)[0]
			}
		case strings.Contains(line, "RUN_ID="):
			// "RUN_ID=12345"; not load-bearing; informational.
		case strings.HasPrefix(line, "[") && strings.Contains(line, "poll"):
			// "[ts] poll N status / conc"
			if i := strings.Index(line, "status="); i >= 0 {
				rest := line[i+len("status="):]
				parts := strings.SplitN(rest, " ", 2)
				if len(parts) > 0 {
					s.Status = parts[0]
				}
			} else if i := strings.Index(line, "poll "); i >= 0 {
				// fallback: "[ts] poll 9 in_progress / "
				rest := strings.TrimSpace(line[i+len("poll "):])
				parts := strings.SplitN(rest, " ", 3)
				if len(parts) >= 2 {
					s.Status = parts[1]
				}
			}
			if i := strings.Index(line, "conclusion="); i >= 0 {
				rest := line[i+len("conclusion="):]
				s.Conclusion = strings.TrimSpace(rest)
			} else if i := strings.LastIndex(line, "/ "); i >= 0 {
				s.Conclusion = strings.TrimSpace(line[i+2:])
			}
		case strings.Contains(line, "tagged "):
			s.Tagged = true
			s.Status = "tagged"
			s.Conclusion = "tagged"
			// "[ts] tagged vX.Y.Z @ <sha>" — capture sha if not set.
			if i := strings.Index(line, "@ "); i >= 0 {
				s.Target = strings.TrimSpace(line[i+2:])
			}
		case strings.Contains(line, "CI not green") || strings.Contains(line, "refusing to tag"):
			s.Status = "refused"
		case strings.Contains(line, "watcher") && strings.Contains(line, "start"):
			// already handled; fall through
		}
	}
	return s
}

func (a *App) runWatchersList() int {
	snaps := listWatchers()
	if len(snaps) == 0 {
		fmt.Fprintln(a.Stdout, "(no /tmp/clawtool-tag-watcher*.log files — no watchers have run on this host yet)")
		return 0
	}
	fmt.Fprintf(a.Stdout, "%-4s  %-12s  %-12s  %-12s  %s\n", "ID", "TAG", "STATUS", "CONCLUSION", "LAST LINE")
	for _, s := range snaps {
		last := s.LastLine
		if len(last) > 70 {
			last = last[:67] + "..."
		}
		fmt.Fprintf(a.Stdout, "%-4d  %-12s  %-12s  %-12s  %s\n",
			s.ID, valueOr(s.Tag, "?"), s.Status, valueOr(s.Conclusion, ""), last)
	}
	return 0
}

func (a *App) runWatchersTail(argv []string) int {
	n := 30
	id := 0
	for i := 0; i < len(argv); i++ {
		v := argv[i]
		switch {
		case v == "--id" && i+1 < len(argv):
			id, _ = strconv.Atoi(argv[i+1])
			i++
		case strings.HasPrefix(v, "--"):
			// unknown flag, ignore
		default:
			parsed, err := strconv.Atoi(v)
			if err == nil {
				n = parsed
			}
		}
	}
	snaps := listWatchers()
	if len(snaps) == 0 {
		fmt.Fprintln(a.Stdout, "(no watchers)")
		return 0
	}
	target := snaps[len(snaps)-1] // latest
	if id > 0 {
		var found bool
		for _, s := range snaps {
			if s.ID == id {
				target = s
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(a.Stderr, "clawtool watchers tail: no watcher with --id %d\n", id)
			return 1
		}
	}
	body, err := os.ReadFile(target.LogPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool watchers tail: %v\n", err)
		return 1
	}
	lines := strings.Split(string(body), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	fmt.Fprintf(a.Stdout, "── watcher %d (%s) ──\n", target.ID, target.LogPath)
	fmt.Fprint(a.Stdout, strings.Join(lines, "\n"))
	return 0
}

// extractWatcherID finds the integer suffix in
// "clawtool-tag-watcher55.log".
func extractWatcherID(name string) int {
	const prefix = "clawtool-tag-watcher"
	i := strings.Index(name, prefix)
	if i < 0 {
		return 0
	}
	rest := name[i+len(prefix):]
	rest = strings.TrimSuffix(rest, ".log")
	n, _ := strconv.Atoi(rest)
	return n
}

// extractAfter returns the substring after `key`. Empty when key
// not found. Used for parsing `tag=v0.22.154` etc.
func extractAfter(line, key string) string {
	i := strings.Index(line, key)
	if i < 0 {
		return ""
	}
	return line[i+len(key):]
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
