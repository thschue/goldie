# 17. Observability through OpenTelemetry, configured by the standard OTEL_* variables

Status: proposed

## Context

Operators want to follow requests and ship goldie's logs into their existing observability stack (#13, #77). The platform contract says every variable uses the GOLDIE_ prefix, that settings come from the registry in internal/platform/settings.go, and that every setting is validated at startup (PC-04). OpenTelemetry defines its own variable names (OTEL_*), which collectors, operators and Helm charts already use, and its Go SDK reads several of them by itself. Until now goldie depended on about 20 modules. The OTLP exporters bring in protobuf and, through the generated collector packages, the gRPC runtime, even for HTTP-only export. That roughly doubles the stripped binary (9.2 MB to 18.2 MB) and raises the module count to about 100.

## Decision

Goldie emits telemetry with the OpenTelemetry Go SDK, from internal/platform only, and installs no global tracer or log provider. Export is OTLP over http/protobuf only; gRPC exporters are not added. Telemetry is configured by the standard OTEL_* variables, which are the one exception to the GOLDIE_ prefix. Telemetry is off unless an OTLP endpoint or an exporter variable turns it on, and OTEL_SDK_DISABLED=true always turns it off. Goldie validates and documents the variables that select and address the exporters: OTEL_SDK_DISABLED, OTEL_EXPORTER_OTLP_{,TRACES_,LOGS_}{ENDPOINT,PROTOCOL,HEADERS}, OTEL_{TRACES,LOGS}_EXPORTER, OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES. These fail fast naming the variable (PC-04), and the HEADERS variables accept a _FILE form (PC-05); for the other names a _FILE form is an error, because the SDK would ignore it. Other OTEL_* variables the SDK reads, such as samplers, batch tuning, timeouts, TLS and compression, keep the SDK's own semantics and are not validated by goldie. The configuration reference says so. Only the public listener is traced; the admin port never is. Logs keep the PC-07 stdout shape and are also bridged to OTLP at the same level. Any goldie-owned telemetry setting keeps the GOLDIE_ prefix. platform-contract.md gains a telemetry clause (PC-12) stating this.

## Consequences

Operators configure goldie like any other OpenTelemetry workload, and the variable names become part of goldie's operator interface, so they are hard to rename. PC-04's fail-fast promise is narrowed for telemetry: a mistyped sampler or batch variable falls back to the SDK default without failing startup. Every role pays the cost of the dependency in binary size and module count, including clients that never export, as ADR 0001 implies. Each upgrade of the OTel v0 modules (log exporter, otelhttp, otelslog) can break their APIs, and the gRPC and protobuf modules must be kept free of known vulnerabilities even though goldie never speaks gRPC. Dropping the gRPC runtime would need a hand-written OTLP/HTTP encoder or an upstream split of the proto module.
