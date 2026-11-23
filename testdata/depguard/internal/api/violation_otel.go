package api

// Deliberate violation: only internal/platform may import go.opentelemetry.io
// (#325). ./run check lints this fixture module and fails unless depguard
// reports it.
import _ "go.opentelemetry.io/otel"
