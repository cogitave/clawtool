package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/catalog"
)

// runSourceCatalog prints every entry in the built-in source
// catalog. Aliased as "available" so users who learnt that verb
// from `npm` / `apt search` find it where they expect.
//
// Layout: grouped by maintained-by (anthropic / community /
// vendor) so a user can tell at a glance which entries come from
// the canonical Anthropic-maintained MCP server set vs community
// additions clawtool has bundled. Per-row output:
//
//	github                        anthropic
//	  GitHub: issues, PRs, code search, repository operations
//	  install:    clawtool source add github
//	  needs:      GITHUB_TOKEN
func (a *App) runSourceCatalog(_ []string) int {
	cat, err := catalog.Builtin()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool source catalog: %v\n", err)
		return 1
	}
	entries := cat.List()
	if len(entries) == 0 {
		fmt.Fprintln(a.Stdout, "(catalog is empty — internal/catalog/builtin.toml missing or unreadable)")
		return 1
	}

	// Group by maintainer.
	groups := map[string][]catalog.NamedEntry{}
	for _, e := range entries {
		key := e.Entry.Maintained
		if key == "" {
			key = "community"
		}
		groups[key] = append(groups[key], e)
	}
	maintainers := make([]string, 0, len(groups))
	for k := range groups {
		maintainers = append(maintainers, k)
	}
	sort.Strings(maintainers)

	w := a.Stdout
	fmt.Fprintf(w, "%d catalog entries — pick what you want, none install by default.\n\n", len(entries))

	for _, m := range maintainers {
		fmt.Fprintf(w, "[%s]\n", m)
		es := groups[m]
		sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
		for _, ne := range es {
			fmt.Fprintf(w, "  %s\n", ne.Name)
			fmt.Fprintf(w, "    %s\n", ne.Entry.Description)
			fmt.Fprintf(w, "    install: clawtool source add %s\n", ne.Name)
			if len(ne.Entry.RequiredEnv) > 0 {
				fmt.Fprintf(w, "    needs:   %s\n", strings.Join(ne.Entry.RequiredEnv, ", "))
			}
			if ne.Entry.Homepage != "" {
				fmt.Fprintf(w, "    home:    %s\n", ne.Entry.Homepage)
			}
			fmt.Fprintln(w)
		}
	}
	return 0
}
