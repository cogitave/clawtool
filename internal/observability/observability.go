// Package observability — OpenTelemetry instrumentation seam for the
// dispatch surface (ADR-014 carry-over T1, design from the 2026-04-26
// multi-CLI fan-out).
//
// One Observer per `clawtool` process. Disabled = pointer-cheap no-op:
// StartSpan returns the input ctx and a no-op end func, RecordError
// is a void call. Enabled hooks an OTLP/HTTP exporter (Langfuse-
// compatible when the operator wires its public/secret key) into the
// global tracer provider; Supervisor.Send and Transport.startStreamingExec
// open spans on top.
//
// Per ADR-007 we wrap go.opentelemetry.io/otel and friends; we do not
// invent trace context propagation, sampler logic, or exporter
// transport. Adding a new exporter (Datadog, Honeycomb) is a one-file
// extension; the Observer surface stays stable.
package observability

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/cogitave/clawtool/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// EndFunc closes a span. Returned by StartSpan; safe to call on a
// disabled Observer (no-op).
type EndFunc func()

// Observer is the single seam every dispatch goes through. The zero
// value is a usable no-op; Init upgrades it to a live tracer when the
// operator's config opts in.
type Observer struct {
	enabled  bool
	tracer   trace.Tracer
	provider *sdktrace.TracerProvider
}

// New returns a zero-value Observer. Equivalent to a disabled
// observer; safe to use immediately.
func New() *Observer { return &Observer{} }

// Init wires the OTLP/HTTP exporter and tracer provider when
// cfg.Enabled is true. When disabled, returns nil and leaves the
// observer in no-op mode.
//
// Init is idempotent within a single process: a second call is a
// no-op. To re-configure call Shutdown first.
func (o *Observer) Init(ctx context.Context, cfg config.ObservabilityConfig) error {
	if o == nil {
		return errors.New("observer is nil")
	}
	if !cfg.Enabled {
		o.enabled = false
		return nil
	}
	if o.provider != nil {
		// Already initialised; second Init in the same process is a no-op.
		return nil
	}

	exporter, err := newExporter(ctx, cfg)
	if err != nil {
		// Per the spec: bad exporter URL surfaces an error so the
		// caller can log it; the caller chooses whether to keep
		// running with the observer disabled or fail open.
		return fmt.Errorf("init OTLP exporter: %w", err)
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "clawtool"
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return fmt.Errorf("init resource: %w", err)
	}

	rate := cfg.SampleRate
	if rate <= 0 {
		rate = 1.0
	}
	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(provider)

	o.provider = provider
	o.tracer = provider.Tracer("github.com/cogitave/clawtool")
	o.enabled = true
	return nil
}

// newExporter constructs an OTLP/HTTP exporter from the config. When
// LangfuseHost + keys are set, the exporter targets Langfuse's OTel
// ingest endpoint with the standard Basic Auth header; otherwise it
// honours ExporterURL or falls back to the default OTLP collector at
// http://localhost:4318.
func newExporter(ctx context.Context, cfg config.ObservabilityConfig) (*otlptrace.Exporter, error) {
	opts := []otlptracehttp.Option{}
	switch {
	case cfg.LangfuseHost != "" && cfg.LangfusePublicKey != "" && cfg.LangfuseSecretKey != "":
		opts = append(opts, otlptracehttp.WithEndpointURL(cfg.LangfuseHost))
		auth := base64.StdEncoding.EncodeToString(
			[]byte(cfg.LangfusePublicKey + ":" + cfg.LangfuseSecretKey),
		)
		opts = append(opts, otlptracehttp.WithHeaders(map[string]string{
			"Authorization": "Basic " + auth,
		}))
	case cfg.ExporterURL != "":
		opts = append(opts, otlptracehttp.WithEndpointURL(cfg.ExporterURL))
	}
	return otlptrace.New(ctx, otlptracehttp.NewClient(opts...))
}

// StartSpan opens a span named `name`. Returns the derived context
// and an end func. On a disabled observer, returns the input ctx and
// a no-op end. Caller convention: `ctx, end := obs.StartSpan(ctx,
// "agents.Send"); defer end()`.
func (o *Observer) StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, EndFunc) {
	if o == nil || !o.enabled || o.tracer == nil {
		return ctx, func() {}
	}
	ctx, span := o.tracer.Start(ctx, name, trace.WithAttributes(attrs...))
	return ctx, func() { span.End() }
}

// RecordError attaches an error to the span carried in ctx and marks
// the span's status. No-op on a disabled observer or when ctx carries
// no active span.
func (o *Observer) RecordError(ctx context.Context, err error) {
	if o == nil || !o.enabled || err == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// SetAttributes adds attributes to the active span in ctx. No-op when
// disabled or when ctx has no recording span.
func (o *Observer) SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	if o == nil || !o.enabled {
		return
	}
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(attrs...)
}

// Shutdown flushes pending spans and tears down the tracer provider.
// Idempotent. Always safe to call (no-op when disabled).
func (o *Observer) Shutdown(ctx context.Context) error {
	if o == nil || o.provider == nil {
		return nil
	}
	err := o.provider.Shutdown(ctx)
	o.provider = nil
	o.tracer = nil
	o.enabled = false
	return err
}

// Enabled reports whether the observer is wired to a live exporter.
// Useful for tests and for skipping expensive attribute construction
// behind a cheap check.
func (o *Observer) Enabled() bool {
	return o != nil && o.enabled
}
