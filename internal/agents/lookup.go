package agents

import "os/exec"

// lookPath is the stdlib exec.LookPath, lifted to a package-private
// indirection so tests can override `binaryOnPath` (in supervisor.go)
// without touching the os/exec runtime.
func lookPath(name string) (string, error) { return exec.LookPath(name) }
