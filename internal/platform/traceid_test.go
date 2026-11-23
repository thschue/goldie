package platform

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Acceptance tests for #242: platform.TraceID is the one place that reads a
// trace id out of a request context, so api records trace_id without using an
// OpenTelemetry type (#13 decision). The contract these tests fix:
//
//	func TraceID(ctx context.Context) string
//
// It is the trace id of the span in ctx as 32 lowercase hex digits, and ""
// when ctx carries no valid span context. TraceID does not compile until the
// implementation lands, so this package's tests fail to build until then.

func TestTraceIDIsTheSpanContextsTraceIDInLowerHex(t *testing.T) {
	const want = "4bf92f3577b34da6a3ce929d0e0e4736"
	tid, err := trace.TraceIDFromHex(want)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	for name, sc := range map[string]trace.SpanContext{
		"local":  trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}),
		"remote": trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled, Remote: true}),
	} {
		if got := TraceID(trace.ContextWithSpanContext(context.Background(), sc)); got != want {
			t.Errorf("%s span context: TraceID = %q, want %q", name, got, want)
		}
	}
}

// A span the SDK starts is read the same way: the recorded trace_id is the id
// the exported span carries.
func TestTraceIDIsTheIDOfASpanTheSDKStarted(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("goldie-test").Start(context.Background(), "op")
	defer span.End()
	want := span.SpanContext().TraceID().String()
	if got := TraceID(ctx); got != want {
		t.Errorf("TraceID = %q, want the started span's %q", got, want)
	}
	if len(want) != 32 {
		t.Fatalf("fixture: span trace id %q is not 32 hex digits", want)
	}
}

// Without a span there is no trace to link to: the id is empty, never the
// all-zero id or a made-up one.
func TestTraceIDIsEmptyWithoutAValidSpan(t *testing.T) {
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	for name, ctx := range map[string]context.Context{
		"background":           context.Background(),
		"zero span context":    trace.ContextWithSpanContext(context.Background(), trace.SpanContext{}),
		"span id, no trace id": trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{SpanID: sid})),
	} {
		if got := TraceID(ctx); got != "" {
			t.Errorf("%s: TraceID = %q, want \"\"", name, got)
		}
	}
}
