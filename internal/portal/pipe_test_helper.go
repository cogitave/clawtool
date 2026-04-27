package portal

import "os"

// readCloser / writeCloser narrow the surface the driver tests use.
// Defined in a non-_test file so they're usable from tests in this
// package without exposing an exported API.
type readCloser interface {
	Read(p []byte) (int, error)
	Close() error
}

type writeCloser interface {
	Write(p []byte) (int, error)
	Close() error
}

func osPipe() (*os.File, *os.File, error) { return os.Pipe() }
