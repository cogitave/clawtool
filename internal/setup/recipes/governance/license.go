// Package governance hosts recipes for the `governance` category —
// files & policies that govern collaboration. The license recipe drops
// a canonical SPDX license at repo root; the codeowners recipe drops
// `.github/CODEOWNERS`. Both are pure file-write recipes with no
// external prereqs.
package governance

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/MIT.txt assets/Apache-2.0.txt assets/BSD-3-Clause.txt assets/AGPL-3.0.txt
var licenseAssets embed.FS

// licenseMarker is appended as a trailing HTML comment so a license
// parser that ignores trailing whitespace/comments still validates the
// file as the canonical SPDX text. Removable without re-installing.
const licenseMarker = "<!-- managed-by: clawtool -->"

const licensePath = "LICENSE"

// supportedSPDX is the closed set of SPDX IDs the recipe ships
// canonical text for. Adding more is a one-line embed + this slice.
var supportedSPDX = []string{"MIT", "Apache-2.0", "BSD-3-Clause", "AGPL-3.0"}

type licenseRecipe struct{}

func (licenseRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "license",
		Category:    setup.CategoryGovernance,
		Description: "Drops a canonical SPDX license file (MIT default; Apache-2.0, BSD-3-Clause, AGPL-3.0 supported).",
		Upstream:    "spec:https://spdx.org/licenses/",
		Stability:   setup.StabilityStable,
	}
}

func (licenseRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	path := filepath.Join(repo, licensePath)
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, "LICENSE not present", nil
	}
	if setup.HasMarker(b, licenseMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "LICENSE exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

func (licenseRecipe) Prereqs() []setup.Prereq { return nil }

func (licenseRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	holder, _ := setup.GetOption[string](opts, "holder")
	if strings.TrimSpace(holder) == "" {
		return fmt.Errorf("license recipe requires opts[holder] (string)")
	}
	spdx, ok := setup.GetOption[string](opts, "spdx")
	if !ok || strings.TrimSpace(spdx) == "" {
		spdx = "MIT"
	}
	if !contains(supportedSPDX, spdx) {
		return fmt.Errorf("unsupported SPDX id %q (supported: %s)", spdx, strings.Join(supportedSPDX, ", "))
	}
	year := time.Now().UTC().Year()
	if y, ok := setup.GetOption[int](opts, "year"); ok && y > 0 {
		year = y
	} else if y, ok := setup.GetOption[float64](opts, "year"); ok && y > 0 {
		// JSON-decoded numbers arrive as float64.
		year = int(y)
	}

	path := filepath.Join(repo, licensePath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, licenseMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite (pass force=true to override)", licensePath)
	}

	tplBytes, err := licenseAssets.ReadFile("assets/" + spdx + ".txt")
	if err != nil {
		return fmt.Errorf("read embedded SPDX text for %q: %w", spdx, err)
	}
	rendered := strings.NewReplacer(
		"{{ year }}", fmt.Sprintf("%d", year),
		"{{ holder }}", holder,
	).Replace(string(tplBytes))
	rendered = strings.TrimRight(rendered, "\n") + "\n\n" + licenseMarker + "\n"
	return setup.WriteAtomic(path, []byte(rendered), 0o644)
}

func (licenseRecipe) Verify(_ context.Context, repo string) error {
	path := filepath.Join(repo, licensePath)
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", licensePath)
	}
	if !setup.HasMarker(b, licenseMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", licensePath)
	}
	return nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func init() { setup.Register(licenseRecipe{}) }
