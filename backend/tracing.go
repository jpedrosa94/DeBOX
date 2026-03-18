package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"go.mongodb.org/mongo-driver/v2/event"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// newMongoMonitor returns an event.CommandMonitor that creates OTel spans for
// every MongoDB command, attaching them as children of the calling context's span.
func newMongoMonitor() *event.CommandMonitor {
	tracer := otel.Tracer("mongo")
	var inFlight sync.Map // requestID(int64) → trace.Span

	return &event.CommandMonitor{
		Started: func(ctx context.Context, e *event.CommandStartedEvent) {
			_, span := tracer.Start(ctx, "mongo."+e.CommandName,
				trace.WithSpanKind(trace.SpanKindClient),
				trace.WithAttributes(
					attribute.String("db.system", "mongodb"),
					attribute.String("db.name", e.DatabaseName),
					attribute.String("db.operation", e.CommandName),
				),
			)
			inFlight.Store(e.RequestID, span)
		},
		Succeeded: func(_ context.Context, e *event.CommandSucceededEvent) {
			if v, ok := inFlight.LoadAndDelete(e.RequestID); ok {
				v.(trace.Span).End()
			}
		},
		Failed: func(_ context.Context, e *event.CommandFailedEvent) {
			if v, ok := inFlight.LoadAndDelete(e.RequestID); ok {
				span := v.(trace.Span)
				span.RecordError(fmt.Errorf("%s", e.Failure))
				span.End()
			}
		},
	}
}

// initTracer sets up the OpenTelemetry tracer with an OTLP HTTP exporter.
// If endpoint is empty, a no-op tracer is used instead.
// Returns a shutdown function that must be called on exit.
func initTracer(ctx context.Context, endpoint string) func(context.Context) {
	if endpoint == "" {
		slog.Info("tracing disabled (OTEL_ENDPOINT not set)")
		return func(context.Context) {}
	}

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		slog.Warn("failed to create OTLP exporter, tracing disabled", "error", err)
		return func(context.Context) {}
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("debox-backend"),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	slog.Info("tracing enabled", "endpoint", endpoint)
	return func(ctx context.Context) {
		if err := tp.Shutdown(ctx); err != nil {
			slog.Warn("tracer shutdown error", "error", err)
		}
	}
}
