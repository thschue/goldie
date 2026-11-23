package platform

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

// TraceID is the trace id of the span in ctx as 32 lowercase hex digits, or ""
// when ctx carries no valid span context. It is the one place that reads a
// trace id out of a request context, so the API records trace_id without
// using an OpenTelemetry type (#13).
func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}
