// Package version exposes the clawtool build version.
package version

// x-release-please-start-version
const (
	Name    = "clawtool"
	Version = "0.8.6" // x-release-please-version
)
// x-release-please-end

func String() string {
	return Name + " " + Version
}
