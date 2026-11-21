// Package platform holds the cross-cutting runtime concerns every role shares:
// config, logging, the admin port, readiness, drain and telemetry. It imports
// no other goldie module. See docs/platform-contract.md.
package platform

// Build information. release-please owns the number; a release build
// overrides these with
// -ldflags "-X github.com/thschue/goldie/internal/platform.Version=<v>"
// (and likewise Commit and BuildDate). They are fixed at build time, so
// /version never reports the current time.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// VersionInfo is the body of /version (PC-03).
type VersionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// CurrentVersion returns the build information of this binary.
func CurrentVersion() VersionInfo {
	return VersionInfo{Version: Version, Commit: Commit, Date: BuildDate}
}
