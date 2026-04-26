// Package version exposes the clawtool build version.
package version

const (
	Name    = "clawtool"
	Version = "0.1.0-dev"
)

func String() string {
	return Name + " " + Version
}
