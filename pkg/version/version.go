// Package version holds build-time metadata injected via -ldflags.
// Values are overwritten at build time, e.g.:
//
//	go build -ldflags "-X github.com/BirdRa1n/screenlet-player/pkg/version.Version=v0.1.0"
package version

var (
	// Version is the SemVer tag this binary was built from (e.g. "v0.1.0").
	// Defaults to "dev" for local, non-release builds.
	Version = "dev"

	// Commit is the short git commit SHA the binary was built from.
	Commit = "none"

	// BuildDate is the RFC3339 timestamp of the build.
	BuildDate = "unknown"
)

// String returns a single-line human-readable version string.
func String() string {
	return Version + " (commit " + Commit + ", built " + BuildDate + ")"
}
