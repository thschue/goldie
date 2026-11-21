// Package platform holds the cross-cutting runtime concerns every role shares:
// config, logging, the admin port, readiness, drain and telemetry. It imports
// no other goldie module. See docs/platform-contract.md.
package platform

// Version is the goldie version. release-please owns the number; a release
// build overrides it with
// -ldflags "-X github.com/thschue/goldie/internal/platform.Version=<v>".
var Version = "dev"
