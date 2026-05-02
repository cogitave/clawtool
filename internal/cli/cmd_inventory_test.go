package cli

// Inventory of every top-level clawtool verb plus a curated set of
// read-only subcommands the smoke-test exercises end-to-end.
//
// The list is DERIVED at test time by parsing the binary's own
// `--help` output (see discoverVerbs in cmd_smoke_test.go). The
// constants below are documented fallbacks / shape descriptions so
// a reader can see at a glance what the smoke-test plans to cover
// without booting the binary. The authoritative source is always
// `./bin/clawtool --help`.
//
// To regenerate after adding a verb:
//
//	go build -o bin/clawtool ./cmd/clawtool
//	./bin/clawtool --help
//
// and confirm the new verb shows up in the smoke-test output.

// inventoryFallbackVerbs is the curated list of top-level verbs the
// smoke-test cross-checks against the binary. Anything in this list
// that the binary does NOT recognise (i.e. the dispatcher prints
// "unknown command") is silently dropped at test time — the actual
// inventory is whatever the binary accepts. Captured from
// internal/cli/cli.go's dispatch switch and cmd/clawtool/main.go.
//
// "serve" is excluded by design: it boots the MCP stdio server and
// would block the smoke-test even with --help (parseServeFlags
// returns an error from --help instead of exiting).
var inventoryFallbackVerbs = []string{
	"init",
	"tools",
	"source",
	"agents",
	"agent",
	"bridge",
	"send",
	"autopilot",
	"worktree",
	"task",
	"star",
	"upgrade",
	"onboard",
	"telemetry",
	"setup",
	"hooks",
	"portal",
	"recipe",
	"doctor",
	"overview",
	"skill",
	"mcp",
	"uninstall",
	"sandbox",
	"unattended",
	"a2a",
	"peer",
	"dashboard",
	"rules",
	"daemon",
	"sandbox-worker",
	"egress",
	"claude-bootstrap",
	"bootstrap",
	"spawn",
	"version",
	"help",
}

// readOnlySubcommands is the smoke-test's allow-list of side-effect-
// free subcommands that should produce non-empty stdout with exit 0
// in any working host. Each entry is a full argv slice (without the
// binary itself). Discovered by inspection of internal/cli/*.go and
// confirmed by hand on the autodev/cmd-smoketest branch.
var readOnlySubcommands = [][]string{
	{"version"},
	{"version", "--json"},
	{"agents", "list"},
	{"agent", "list"},
	{"source", "list"},
	{"source", "catalog"},
	{"recipe", "list"},
	{"skill", "list"},
	{"bridge", "list"},
	{"portal", "list"},
	{"mcp", "list"},
	{"sandbox", "list"},
	{"rules", "list"},
	{"telemetry", "status"},
	{"tools", "list"},
	{"worktree", "list"},
	{"task", "list"},
}
