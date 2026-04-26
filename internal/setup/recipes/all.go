// Package recipes is a thin import-aggregator: it pulls in every
// recipe subpackage so their init() functions register with the
// global setup.Registry. Both the CLI (`clawtool recipe …`,
// `clawtool init`) and the MCP server (`mcp__clawtool__Recipe*`)
// import this package so the registry is populated regardless of
// how clawtool is invoked.
//
// Adding a new recipe is one blank import here.
package recipes

import (
	_ "github.com/cogitave/clawtool/internal/setup/recipes/agentclaim"
	_ "github.com/cogitave/clawtool/internal/setup/recipes/commits"
	_ "github.com/cogitave/clawtool/internal/setup/recipes/governance"
	_ "github.com/cogitave/clawtool/internal/setup/recipes/release"
	_ "github.com/cogitave/clawtool/internal/setup/recipes/supplychain"
)
