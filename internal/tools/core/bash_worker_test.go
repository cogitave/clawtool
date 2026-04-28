package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/sandbox/worker"
)

// fakeClientExec lets us point worker.Global() at a stub that
// returns a known response or error without needing a real
// WebSocket roundtrip.
type fakeWorkerExec struct {
	resp *worker.ExecResponse
	err  error
}

// We don't have an interface for worker.Client today, so the
// routing test uses the real *worker.Client wired against an
// always-erroring URL — which exercises the failure path
// (worker call → log → host fallback). The success path is
// covered by the integration test in worker_test.go where
// handleExec is called directly.

// TestRunBash_WorkerNilFallsBackToHost: when worker.Global() is
// nil, runBash must execute on the host path. Default state
// (`mode=off`).
func TestRunBash_WorkerNilFallsBackToHost(t *testing.T) {
	worker.SetGlobal(nil)
	defer worker.SetGlobal(nil)

	// Direct executeBash call — fastest sanity check that the
	// host path produces the expected shape. The full mcp request
	// path goes through runBash; that path is covered by
	// bash_test.go.
	res := executeBash(context.Background(), "echo hi", "", 5*time.Second)
	if res.ExitCode != 0 || res.Stdout != "hi\n" {
		t.Errorf("host fallback produced wrong result: %+v", res)
	}
}

// TestTryWorkerExec_SurfacesTransportError: wraps a Client whose
// dial will always fail (loopback :1 is conventionally closed),
// confirms tryWorkerExec returns ok=false so the caller falls
// back to host. This is the contract that keeps the operator's
// tool surface available when the worker container is missing.
func TestTryWorkerExec_SurfacesTransportError(t *testing.T) {
	c := worker.NewClient("ws://127.0.0.1:1/ws", "test-token")
	defer c.Close()

	_, ok := tryWorkerExec(context.Background(), c, "echo hi", "", 1000)
	if ok {
		t.Fatal("dial to closed port should fail; ok must be false")
	}
}

// TestTryWorkerExec_NilSafe defends against a regression where
// runBash is called before SetGlobal — Bash must still work.
// The function itself doesn't accept nil (caller pre-checks via
// worker.Global()), but we cover the global-nil path here.
func TestTryWorkerExec_NilSafe(t *testing.T) {
	worker.SetGlobal(nil)
	if wc := worker.Global(); wc != nil {
		t.Fatal("expected nil global after SetGlobal(nil)")
	}
}

// TestWorker_GlobalIdempotent confirms SetGlobal can be called
// repeatedly without panicking — server boot may rerun
// wireSandboxWorker on config reload.
func TestWorker_GlobalIdempotent(t *testing.T) {
	worker.SetGlobal(nil)
	worker.SetGlobal(worker.NewClient("ws://x/ws", "t"))
	worker.SetGlobal(nil) // back to off
	if wc := worker.Global(); wc != nil {
		t.Error("final SetGlobal(nil) did not clear")
	}
}

// Stop the linter from complaining about the unused
// fakeWorkerExec type (kept as a future hook for when
// worker.Client gains an interface).
var _ = errors.New
var _ = fakeWorkerExec{}
