package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
)

func TestDisabled_StartSpanIsNoop(t *testing.T) {
	o := New()
	if err := o.Init(context.Background(), config.ObservabilityConfig{Enabled: false}); err != nil {
		t.Fatalf("Init disabled should not error; got %v", err)
	}
	if o.Enabled() {
		t.Error("disabled observer reports Enabled() = true")
	}
	ctx := context.Background()
	gotCtx, end := o.StartSpan(ctx, "test")
	if gotCtx != ctx {
		t.Error("disabled StartSpan should return input ctx unchanged")
	}
	end()                               // must not panic
	o.RecordError(ctx, errors.New("x")) // no-op
	if err := o.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown disabled should be a no-op; got %v", err)
	}
}

func TestEnabled_SpanLifecycle(t *testing.T) {
	// Use a clearly-bogus URL so Init succeeds (the OTLP/HTTP client
	// is lazily-connected; bad endpoints surface only on first export).
	// We're testing the in-process wiring, not the network path.
	o := New()
	cfg := config.ObservabilityConfig{
		Enabled:     true,
		ExporterURL: "http://127.0.0.1:1", // unreachable, fine for unit
		SampleRate:  1.0,
	}
	if err := o.Init(context.Background(), cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !o.Enabled() {
		t.Fatal("observer should be enabled after Init")
	}

	ctx := context.Background()
	gotCtx, end := o.StartSpan(ctx, "agents.Supervisor.dispatch")
	if gotCtx == ctx {
		t.Error("enabled StartSpan should return a derived ctx, not the input")
	}
	o.RecordError(gotCtx, errors.New("synthetic"))
	end() // closes the span; flush happens on Shutdown

	if err := o.Shutdown(context.Background()); err != nil {
		// Shutdown can fail to flush over the bogus URL but we
		// shouldn't panic — surface non-fatally for the operator.
		t.Logf("Shutdown surfaced expected flush error: %v", err)
	}
	if o.Enabled() {
		t.Error("Shutdown should disable the observer")
	}
}

func TestInit_BadEndpointFailsGracefully(t *testing.T) {
	o := New()
	// Empty endpoint URL is acceptable (the client picks defaults). We
	// exercise the case where Init returns nil but the observer is
	// still queryable — i.e. a bad config doesn't panic-crash boot.
	err := o.Init(context.Background(), config.ObservabilityConfig{
		Enabled: true,
	})
	if err != nil {
		// Some Go OTel versions reject empty endpoint at init time.
		// Either path is acceptable; we just don't want a panic.
		t.Logf("Init with empty endpoint surfaced: %v", err)
		return
	}
	if !o.Enabled() {
		t.Error("Init returned nil but observer is not Enabled()")
	}
	_ = o.Shutdown(context.Background())
}

func TestInit_Idempotent(t *testing.T) {
	o := New()
	cfg := config.ObservabilityConfig{Enabled: true, ExporterURL: "http://127.0.0.1:1", SampleRate: 1.0}
	if err := o.Init(context.Background(), cfg); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if err := o.Init(context.Background(), cfg); err != nil {
		t.Errorf("second Init should be a no-op; got %v", err)
	}
	_ = o.Shutdown(context.Background())
}

func TestNilObserver_AllMethodsSafe(t *testing.T) {
	var o *Observer
	ctx := context.Background()
	gotCtx, end := o.StartSpan(ctx, "x")
	if gotCtx != ctx {
		t.Error("nil StartSpan should pass-through ctx")
	}
	end()
	o.RecordError(ctx, errors.New("x"))
	o.SetAttributes(ctx)
	if err := o.Shutdown(ctx); err != nil {
		t.Errorf("nil Shutdown should be a no-op; got %v", err)
	}
	if o.Enabled() {
		t.Error("nil observer should not be Enabled()")
	}
}
