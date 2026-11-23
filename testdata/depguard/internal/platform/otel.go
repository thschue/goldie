package platform

// Allowed: internal/platform owns telemetry and is the only package that may
// import go.opentelemetry.io (#325). The depguard fixture check fails if this
// file is reported.
import _ "go.opentelemetry.io/otel"
